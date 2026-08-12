package main

import (
	"context"
	"strings"
	"testing"

	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/docker/docker-agent/pkg/model/provider/providers"
)

// stubEnv is a deterministic environment.Provider for credential-based selection
// tests — absent keys read as ("", false), so no real environment leaks in.
type stubEnv map[string]string

func (s stubEnv) Get(_ context.Context, name string) (string, bool) {
	v, ok := s[name]
	return v, ok
}

func TestPickModel(t *testing.T) {
	cases := []struct {
		name string
		flag string
		cfg  turfConfig
		want string
	}{
		{"flag wins", "anthropic/claude-sonnet-4-6", turfConfig{Model: "smart"}, "anthropic/claude-sonnet-4-6"},
		{"config when no flag", "", turfConfig{Model: "smart"}, "smart"},
		{"auto when nothing set", "", turfConfig{}, "auto"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickModel(tc.flag, tc.cfg); got != tc.want {
				t.Errorf("pickModel(%q, %+v) = %q, want %q", tc.flag, tc.cfg, got, tc.want)
			}
		})
	}
}

func TestSelectModelConfigAuto(t *testing.T) {
	ctx := context.Background()
	// Only an OpenAI key is present; anthropic is checked first but skipped.
	env := stubEnv{"OPENAI_API_KEY": "test"}
	mc, err := selectModelConfig(ctx, turfConfig{}, autoModelRef, env)
	if err != nil {
		t.Fatalf("selectModelConfig(auto): %v", err)
	}
	if mc.Provider != "openai" {
		t.Errorf("auto provider = %q, want openai (first with credentials)", mc.Provider)
	}
}

func TestSelectModelConfigFirstAvailable(t *testing.T) {
	ctx := context.Background()
	cfg := turfConfig{Models: map[string]latest.ModelConfig{
		"smart": {FirstAvailable: []string{"anthropic/claude-x", "google/gemini-y", "dmr/ai/z"}},
	}}

	t.Run("picks first credentialed candidate", func(t *testing.T) {
		env := stubEnv{"GOOGLE_API_KEY": "test"} // anthropic missing → google chosen
		mc, err := selectModelConfig(ctx, cfg, "smart", env)
		if err != nil {
			t.Fatalf("selectModelConfig(smart): %v", err)
		}
		if mc.Provider != "google" || mc.Model != "gemini-y" {
			t.Errorf("resolved = %q/%q, want google/gemini-y", mc.Provider, mc.Model)
		}
	})

	t.Run("falls back to keyless dmr", func(t *testing.T) {
		env := stubEnv{} // no keys → dmr (needs none) is the final fallback
		mc, err := selectModelConfig(ctx, cfg, "smart", env)
		if err != nil {
			t.Fatalf("selectModelConfig(smart): %v", err)
		}
		if mc.Provider != "dmr" || mc.Model != "ai/z" {
			t.Errorf("resolved = %q/%q, want dmr/ai/z", mc.Provider, mc.Model)
		}
	})
}

// TestSelectModelConfigFirstAvailableNamedCandidate pins the mechanism turf's
// local-model docs are built on: a first_available candidate may NAME another
// entry in models: (rather than being an inline "provider/model" ref), and the
// named entry's provider_opts survive selection.
//
// This matters because a local model is unusable without
// provider_opts.context_size — turf's prompt is ~27k tokens and Docker Model
// Runner's default window is far smaller — and an inline ref cannot carry
// per-model options. Naming the entry is therefore the ONLY way to keep the
// keyless-fallback idiom while configuring the window, which is exactly what
// README.md and the turf-examples gallery now tell users to write.
func TestSelectModelConfigFirstAvailableNamedCandidate(t *testing.T) {
	ctx := context.Background()
	cfg := turfConfig{Models: map[string]latest.ModelConfig{
		"default": {FirstAvailable: []string{"anthropic/claude-x", "local"}},
		"local": {
			Provider:     "dmr",
			Model:        "ai/qwen3",
			ProviderOpts: map[string]any{"context_size": 65536},
		},
	}}

	env := stubEnv{} // no keys → the keyless local entry wins
	mc, err := selectModelConfig(ctx, cfg, "default", env)
	if err != nil {
		t.Fatalf("selectModelConfig(default): %v", err)
	}
	if mc.Provider != "dmr" || mc.Model != "ai/qwen3" {
		t.Fatalf("resolved = %q/%q, want dmr/ai/qwen3", mc.Provider, mc.Model)
	}
	// Read it back the way cagent does everywhere it consumes the setting (the
	// DMR _configure call, the max_tokens clamp, resolveContextLimit).
	if got := latest.ContextSizeFromProviderOpts(mc.ProviderOpts); got != 65536 {
		t.Errorf("context_size = %d, want 65536 (provider_opts lost through first_available)", got)
	}
}

func TestSelectModelConfigInline(t *testing.T) {
	ctx := context.Background()
	env := stubEnv{}
	mc, err := selectModelConfig(ctx, turfConfig{}, "openai/gpt-4o", env)
	if err != nil {
		t.Fatalf("selectModelConfig(inline): %v", err)
	}
	if mc.Provider != "openai" || mc.Model != "gpt-4o" {
		t.Errorf("resolved = %q/%q, want openai/gpt-4o", mc.Provider, mc.Model)
	}
}

func TestSelectModelConfigInvalid(t *testing.T) {
	ctx := context.Background()
	_, err := selectModelConfig(ctx, turfConfig{}, "claude", stubEnv{})
	if err == nil {
		t.Fatal("expected an error for a bare reference, got nil")
	}
	if !strings.Contains(err.Error(), "invalid model") {
		t.Errorf("error = %q, want it to mention 'invalid model'", err.Error())
	}
}

// TestResolveModelBaseURL confirms --base-url is threaded onto the resolved
// config before the provider is constructed.
func TestResolveModelBaseURL(t *testing.T) {
	ctx := context.Background()
	// OpenAI client constructs with the default (empty) token_key; a dummy value
	// keeps it hermetic regardless.
	t.Setenv("OPENAI_API_KEY", "test-key")
	reg := providers.NewDefaultRegistry()
	const baseURL = "http://localhost:8000/v1"
	_, resolved, err := resolveModel(ctx, turfConfig{}, "openai/gpt-4o", baseURL, reg)
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	if resolved.BaseURL != baseURL {
		t.Errorf("resolved.BaseURL = %q, want %q", resolved.BaseURL, baseURL)
	}
}

// TestResolveModelCustomProvider confirms turf.yaml's `providers:` map is handed
// to the factory at construction (via options.WithProviders) so a model that
// references a custom provider by NAME resolves to that provider's definition.
// Without that wiring the factory treats the provider name as an unknown provider
// TYPE and fails with "unknown provider type".
func TestResolveModelCustomProvider(t *testing.T) {
	ctx := context.Background()
	t.Setenv("MY_HOUSE_KEY", "test-key")
	cfg := turfConfig{
		Providers: map[string]latest.ProviderConfig{
			"myhouse": {Provider: "openai", BaseURL: "http://localhost:9/v1", TokenKey: "MY_HOUSE_KEY"},
		},
		Models: map[string]latest.ModelConfig{
			"house": {Provider: "myhouse", Model: "some-model"},
		},
	}
	_, resolved, err := resolveModel(ctx, cfg, "house", "", providers.NewDefaultRegistry())
	if err != nil {
		t.Fatalf("resolveModel(house): %v", err)
	}
	if resolved.Provider != "myhouse" || resolved.Model != "some-model" {
		t.Errorf("resolved = %q/%q, want myhouse/some-model", resolved.Provider, resolved.Model)
	}
}

func TestBuildModelSwitcherConfig(t *testing.T) {
	ctx := context.Background()
	cfg := turfConfig{
		Models:    map[string]latest.ModelConfig{"fast": {Provider: "google", Model: "gemini-3.5-flash"}},
		Providers: map[string]latest.ProviderConfig{"custom": {BaseURL: "http://x"}},
	}
	sw := buildModelSwitcherConfig(ctx, cfg, providers.NewDefaultRegistry(), "fast")
	if sw.AgentDefaultModels[turfAgentName] != "fast" {
		t.Errorf("AgentDefaultModels[%q] = %q, want fast", turfAgentName, sw.AgentDefaultModels[turfAgentName])
	}
	if _, ok := sw.Models["fast"]; !ok {
		t.Error("switcher Models should include the configured 'fast' model")
	}
	if _, ok := sw.Providers["custom"]; !ok {
		t.Error("switcher Providers should pass through custom providers")
	}
	if sw.ProviderRegistry == nil || sw.EnvProvider == nil {
		t.Error("switcher must carry a registry and env provider")
	}
}
