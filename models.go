package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/docker/docker-agent/pkg/config"
	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/docker/docker-agent/pkg/environment"
	"github.com/docker/docker-agent/pkg/model/provider"
	"github.com/docker/docker-agent/pkg/model/provider/dmr"
	"github.com/docker/docker-agent/pkg/model/provider/options"
	"github.com/docker/docker-agent/pkg/model/provider/providers"
	"github.com/docker/docker-agent/pkg/modelsdev"
	"github.com/docker/docker-agent/pkg/runtime"
)

const (
	// autoModelRef selects the first provider whose credentials are configured
	// (anthropic → openai → google → … → dmr). It is turf's built-in default, so
	// turf runs out of the box with whatever key the user has, and is the
	// zero-config form of "first available".
	autoModelRef = "auto"
	// defaultModelRef is used when neither --model/TURF_MODEL nor a config file
	// `model:` is set.
	defaultModelRef = autoModelRef
	// turfAgentName is the single agent's name; it keys the model-switcher's
	// per-agent default and scopes first_available resolution, so it must match
	// the name agent.New is given in createAgentRuntime. Deliberately a constant:
	// branding does not rename the agent (see brandingConfig).
	turfAgentName = "turf"
)

// pickModel resolves which model reference to use, in precedence order:
//
//	--model / TURF_MODEL  >  turf.yaml `model:`  >  built-in default ("auto")
//
// flagModel already folds "CLI flag > TURF_MODEL env" (the flag defaults to the
// env value in root.go), so a non-empty flagModel is the user's explicit choice.
func pickModel(flagModel string, cfg turfConfig) string {
	if flagModel != "" {
		return flagModel
	}
	if cfg.Model != "" {
		return cfg.Model
	}
	return defaultModelRef
}

// resolveModel turns a model reference (a named model from turf.yaml, an inline
// "provider/model", or "auto") into a concrete provider, reusing cagent's
// first-available and auto selection logic — turf adds no selection logic of its
// own. It returns the constructed provider and the concrete ModelConfig it
// resolved to (for logging / display). The registry is passed in so the same one
// backs both the agent's model and the /model switcher.
func resolveModel(ctx context.Context, cfg turfConfig, pick, baseURL string, reg *provider.Registry) (provider.Provider, latest.ModelConfig, error) {
	env := environment.NewDefaultProvider()
	resolved, err := selectModelConfig(ctx, cfg, pick, env)
	if err != nil {
		return nil, latest.ModelConfig{}, err
	}
	// --base-url / TURF_MODEL_BASE_URL overrides the endpoint (vLLM, LM Studio,
	// gateways); DMR/Ollama resolve their own local endpoints and need none.
	if baseURL != "" {
		resolved.BaseURL = baseURL
	}
	// Construct with the same provider-scoped options cagent's teamloader uses
	// (pkg/teamloader/teamloader.go). WithProviders hands the factory turf.yaml's
	// custom `providers:` map so a model referencing a provider by NAME resolves
	// to that definition's type/base_url/token_key (without it the factory sees
	// the provider name as an unknown provider TYPE and fails). WithGateway routes
	// calls through `models_gateway:` at construction — cfg.ModelsGateway is
	// otherwise only consulted during selection (Auto/first-available).
	llm, err := reg.New(ctx, &resolved, env,
		options.WithProviders(cfg.Providers),
		options.WithGateway(cfg.ModelsGateway),
	)
	if err != nil {
		return nil, latest.ModelConfig{}, err
	}
	return llm, resolved, nil
}

// selectModelConfig maps a reference to a concrete ModelConfig.
func selectModelConfig(ctx context.Context, cfg turfConfig, pick string, env environment.Provider) (latest.ModelConfig, error) {
	// "auto": first provider whose credentials are configured (dmr as the final,
	// keyless fallback). dmr.ListModels lets it prefer an already-pulled local
	// model over forcing a default pull.
	if pick == autoModelRef {
		return config.AutoModelConfig(ctx, cfg.ModelsGateway, env, nil, dmr.ListModels), nil
	}
	// A named model from turf.yaml. Resolve any first_available in a synthetic
	// single-agent Config whose only agent references `pick`, so resolution is
	// SCOPED to this model: an unrelated, currently-unsatisfiable first_available
	// entry elsewhere in the file does not fail the run (reachableFirstAvailable
	// only visits models reachable from an agent).
	if _, ok := cfg.Models[pick]; ok {
		syn := &latest.Config{
			Models:    cloneModels(cfg.Models),
			Providers: cfg.Providers,
			Agents:    latest.Agents{{Name: turfAgentName, Model: pick}},
		}
		if err := config.ResolveFirstAvailableModels(ctx, syn, cfg.ModelsGateway, env); err != nil {
			return latest.ModelConfig{}, err
		}
		return syn.Models[pick], nil
	}
	// Inline "provider/model". An unknown provider or a bare token errors here
	// (rather than silently falling through), surfacing typos.
	mc, err := latest.ParseModelRef(pick)
	if err != nil {
		return latest.ModelConfig{}, fmt.Errorf("invalid model %q: not a model defined in turf.yaml, not %q, and not a valid provider/model reference: %w", pick, autoModelRef, err)
	}
	return mc, nil
}

// buildModelSwitcherConfig wires the data the runtime needs to power the TUI
// /model picker (SupportsModelSwitching becomes true). Beyond the configured
// named models, the picker auto-adds locally-pulled DMR models and the models.dev
// catalog filtered by available credentials, so it is useful even when turf.yaml
// declares no models. ModelsStore is left nil — the runtime builds its own lazy
// models.dev store on first /model open — except under TURF_OFFLINE, where it
// is a store that never fetches (offlineModelsStore).
func buildModelSwitcherConfig(ctx context.Context, cfg turfConfig, reg *provider.Registry, pick string) *runtime.ModelSwitcherConfig {
	env := environment.NewDefaultProvider()
	// Best-effort: resolve each first_available entry so the picker shows a
	// concrete provider/model. Errors are ignored here — only the selected model
	// must be resolvable (that is enforced in resolveModel); other named entries
	// whose credentials are absent simply display as-is.
	models := cloneModels(cfg.Models)
	syn := &latest.Config{Models: models, Providers: cfg.Providers}
	_ = config.ResolveFirstAvailableModels(ctx, syn, cfg.ModelsGateway, env)
	msc := &runtime.ModelSwitcherConfig{
		Models:             syn.Models,
		Providers:          cfg.Providers,
		ModelsGateway:      cfg.ModelsGateway,
		EnvProvider:        env,
		ProviderRegistry:   reg,
		AgentDefaultModels: map[string]string{turfAgentName: pick},
	}
	if offline(os.Getenv) {
		// A nil store on error leaves the runtime's own, which fetches: the
		// catalog is a convenience, and failing to pin it must not stop a run.
		if store, err := offlineModelsStore(); err == nil {
			msc.ModelsStore = store
		}
	}
	return msc
}

// offlineEnv is the air-gap switch, which the MCP server and the engine read
// too: nothing Turf does on its own account then reaches the internet.
const offlineEnv = "TURF_OFFLINE"

// offline reports TURF_OFFLINE: any value but empty, 0, false, off or no.
func offline(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(offlineEnv))) {
	case "", "0", "false", "off", "no":
		return false
	}
	return true
}

var errModelsCatalogOffline = errors.New("the models.dev catalog is not fetched: " + offlineEnv + " is set")

// offlineModelsStore is a models.dev store that never reaches models.dev: a
// lookup is answered from the on-disk cache when there is one, else from the
// catalog snapshot built into the binary. Model metadata (context sizes, the
// /model picker's list) is then as old as that, which a turf.yaml model entry
// overrides where it matters.
func offlineModelsStore() (*modelsdev.Store, error) {
	return modelsdev.NewStore(
		modelsdev.WithKnownProvider(func(string) bool { return false }),
		modelsdev.WithFetcher(func(context.Context, string) (*modelsdev.Database, string, error) {
			return nil, "", errModelsCatalogOffline
		}),
	)
}

// cloneModels returns a shallow copy of a model map so in-place resolution
// (which mutates the map) never scribbles on the caller's config.
func cloneModels(in map[string]latest.ModelConfig) map[string]latest.ModelConfig {
	out := make(map[string]latest.ModelConfig, len(in))
	maps.Copy(out, in)
	return out
}

// newModelProvider constructs a provider directly from a model reference with no
// turf.yaml context — a thin wrapper over resolveModel for call sites (and tests)
// that only ever pass an inline "provider/model" (or a bare, invalid) reference.
func newModelProvider(ctx context.Context, modelRef, baseURL string) (provider.Provider, error) {
	p, _, err := resolveModel(ctx, turfConfig{}, modelRef, baseURL, providers.NewDefaultRegistry())
	return p, err
}
