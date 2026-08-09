package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/goccy/go-yaml"
)

// turfConfigFileName is the basename of turf's config file in both the global
// (<TURF_HOME>) and project (<cwd>/.turf) locations.
const turfConfigFileName = "turf.yaml"

// turfConfig is turf's on-disk configuration overlay. It is deliberately a
// turf-owned schema — NOT a cagent agent manifest — so turf keeps ownership of
// its agent identity (persona, the turf MCP toolset + its turf_ prefix, the
// permission gates, sequential tool calls) in code and exposes only the sections
// it chooses. Phase 1 exposes model configuration.
//
// The map VALUES reuse cagent's public latest.ModelConfig / latest.ProviderConfig
// types, so the YAML is byte-for-byte what Docker's docs document — including the
// credential-based `first_available:` fallback list, named models, and tuning
// fields (temperature, max_tokens, base_url, token_key, …). goccy/go-yaml honors
// the json tags on those types, so `first_available` and friends unmarshal
// correctly even though they carry no yaml tag.
type turfConfig struct {
	// Model is the default selector: a named model from Models, an inline
	// "provider/model", or "auto" (first provider whose credentials are set).
	Model string `yaml:"model,omitempty"`
	// Models is the named-model catalog (each may be a first_available list, a
	// fully-specified model, or an inline shorthand).
	Models map[string]latest.ModelConfig `yaml:"models,omitempty"`
	// Providers are custom provider definitions (base_url, api_type, token_key)
	// referenced by models by name.
	Providers map[string]latest.ProviderConfig `yaml:"providers,omitempty"`
	// ModelsGateway routes all model calls through a gateway URL (credentials
	// supplied by the gateway).
	ModelsGateway string `yaml:"models_gateway,omitempty"`

	// MCPs are external MCP servers wired as additional agent toolsets, each
	// under its own name. cagent prefixes a toolset's tools with "<name>_", so a
	// server named `scalr` exposes `scalr_*` and `opa` exposes `opa_*`, disjoint
	// from turf's own `turf_*`. The remaining reserved overlay sections
	// (inline/remote skills, permission overlays) are still future work; see
	// CLAUDE.md.
	MCPs map[string]mcpServerConfig `yaml:"mcps,omitempty"`
}

// mcpServerConfig declares one external MCP server for the `mcps:` overlay.
// Exactly one transport is used: stdio (a local subprocess via Command+Args) or
// remote (URL). Env is forwarded to a stdio subprocess — so a `docker run --env
// NAME` (no value) can forward NAME from turf's own environment into the
// container, which is how a token reaches the server without being written here.
// Headers/Transport apply to the remote transport ("streamable" — the default —
// or "sse").
type mcpServerConfig struct {
	Command   string            `yaml:"command,omitempty"`
	Args      []string          `yaml:"args,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
	URL       string            `yaml:"url,omitempty"`
	Transport string            `yaml:"transport,omitempty"`
	Headers   map[string]string `yaml:"headers,omitempty"`
}

// loadTurfConfig reads and merges turf's config file from two locations, in
// increasing precedence (project overrides global) — mirroring loadTurfSkills:
//
//  1. <TURF_HOME>/turf.yaml   — per-user global config
//  2. <cwd>/.turf/turf.yaml   — project config, versioned with the infra config,
//     under the same .turf/ dir that already holds skills/
//
// The working-dir location is exactly <cwd>/.turf/turf.yaml — no walk up the
// tree — so behavior is predictable from where turf is launched. A missing file
// is not an error (it contributes nothing); a malformed file is.
func loadTurfConfig(cwd string) (turfConfig, error) {
	merged := turfConfig{
		Models:    map[string]latest.ModelConfig{},
		Providers: map[string]latest.ProviderConfig{},
		MCPs:      map[string]mcpServerConfig{},
	}
	for _, path := range []string{
		filepath.Join(turfHome(), turfConfigFileName),
		filepath.Join(cwd, ".turf", turfConfigFileName),
	} {
		c, ok, err := readTurfConfigFile(path)
		if err != nil {
			return turfConfig{}, err
		}
		if ok {
			mergeTurfConfig(&merged, c)
		}
	}
	return merged, nil
}

// readTurfConfigFile reads and parses one config file. ok is false (with a nil
// error) when the file does not exist.
func readTurfConfigFile(path string) (turfConfig, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return turfConfig{}, false, nil
		}
		return turfConfig{}, false, fmt.Errorf("reading turf config %s: %w", path, err)
	}
	var c turfConfig
	if err := yaml.Unmarshal(data, &c); err != nil {
		return turfConfig{}, false, fmt.Errorf("parsing turf config %s: %w", path, err)
	}
	return c, true, nil
}

// mergeTurfConfig overlays src onto dst. Scalars override when set; the Models
// and Providers maps merge per key (a src entry replaces a dst entry of the same
// name).
func mergeTurfConfig(dst *turfConfig, src turfConfig) {
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.ModelsGateway != "" {
		dst.ModelsGateway = src.ModelsGateway
	}
	maps.Copy(dst.Models, src.Models)
	maps.Copy(dst.Providers, src.Providers)
	maps.Copy(dst.MCPs, src.MCPs)
}
