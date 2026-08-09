package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/docker/docker-agent/pkg/environment"
)

// writeFile is a tiny helper that creates parent dirs and writes a file.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestLoadTurfConfigMissing confirms that with neither file present, loading
// yields an empty (non-nil-map) config and no error.
func TestLoadTurfConfigMissing(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("TURF_HOME", home)

	cfg, err := loadTurfConfig(cwd)
	if err != nil {
		t.Fatalf("loadTurfConfig: %v", err)
	}
	if cfg.Model != "" || len(cfg.Models) != 0 || len(cfg.Providers) != 0 {
		t.Errorf("expected empty config, got %+v", cfg)
	}
}

// TestLoadTurfConfigParsesModelSchema confirms goccy honors the json tags on
// latest.ModelConfig, so first_available and tuning fields unmarshal.
func TestLoadTurfConfigParsesModelSchema(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("TURF_HOME", home)

	writeFile(t, filepath.Join(home, turfConfigFileName), `
model: smart
models:
  smart:
    first_available:
      - anthropic/claude-sonnet-4-6
      - dmr/ai/qwen3
  fast:
    provider: google
    model: gemini-3.5-flash
    max_tokens: 32000
`)

	cfg, err := loadTurfConfig(cwd)
	if err != nil {
		t.Fatalf("loadTurfConfig: %v", err)
	}
	if cfg.Model != "smart" {
		t.Errorf("Model = %q, want smart", cfg.Model)
	}
	if got := cfg.Models["smart"].FirstAvailable; len(got) != 2 || got[0] != "anthropic/claude-sonnet-4-6" {
		t.Errorf("smart.first_available = %v, want the 2-entry list", got)
	}
	fast := cfg.Models["fast"]
	if fast.Provider != "google" || fast.Model != "gemini-3.5-flash" {
		t.Errorf("fast provider/model = %q/%q", fast.Provider, fast.Model)
	}
	if fast.MaxTokens == nil || *fast.MaxTokens != 32000 {
		t.Errorf("fast.max_tokens = %v, want 32000", fast.MaxTokens)
	}
}

// TestLoadTurfConfigMergePrecedence confirms the project file overrides the
// global one: the scalar `model:` wins, a same-named model is replaced, and
// distinct models from both files coexist.
func TestLoadTurfConfigMergePrecedence(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("TURF_HOME", home)

	writeFile(t, filepath.Join(home, turfConfigFileName), `
model: global-default
models:
  shared:
    provider: google
    model: from-global
  global-only:
    provider: openai
    model: gpt-5
`)
	writeFile(t, filepath.Join(cwd, ".turf", turfConfigFileName), `
model: project-default
models:
  shared:
    provider: anthropic
    model: from-project
  project-only:
    provider: dmr
    model: ai/qwen3
`)

	cfg, err := loadTurfConfig(cwd)
	if err != nil {
		t.Fatalf("loadTurfConfig: %v", err)
	}
	if cfg.Model != "project-default" {
		t.Errorf("Model = %q, want project-default (project overrides global)", cfg.Model)
	}
	if got := cfg.Models["shared"]; got.Provider != "anthropic" || got.Model != "from-project" {
		t.Errorf("shared = %q/%q, want anthropic/from-project (project wins)", got.Provider, got.Model)
	}
	if _, ok := cfg.Models["global-only"]; !ok {
		t.Error("global-only model should survive the merge")
	}
	if _, ok := cfg.Models["project-only"]; !ok {
		t.Error("project-only model should be present")
	}
}

// TestLoadTurfConfigParsesMCPs confirms the `mcps:` overlay parses both the
// stdio (command/args/env) and remote (url/transport) shapes, and merges with
// project precedence per server name (a same-named project entry replaces the
// global one wholesale; distinct servers from both files coexist).
func TestLoadTurfConfigParsesMCPs(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("TURF_HOME", home)

	writeFile(t, filepath.Join(home, turfConfigFileName), `
mcps:
  scalr:
    command: docker
    args: [run, --rm, -i, scalr/mcp-server:latest]
    env:
      SCALR_API_URL: https://global.scalr.io
  remote-only:
    url: https://example.com/mcp
    transport: sse
`)
	writeFile(t, filepath.Join(cwd, ".turf", turfConfigFileName), `
mcps:
  scalr:
    command: docker
    args: [run, --rm, -i, --env, SCALR_API_TOKEN, scalr/mcp-server:latest]
  opa:
    command: docker
    args: [run, --rm, -i, orygn/opa-mcp:latest]
`)

	cfg, err := loadTurfConfig(cwd)
	if err != nil {
		t.Fatalf("loadTurfConfig: %v", err)
	}
	// The project 'scalr' replaces the global one wholesale (per-key map merge).
	scalr := cfg.MCPs["scalr"]
	if scalr.Command != "docker" || len(scalr.Args) != 6 || scalr.Args[3] != "--env" {
		t.Errorf("scalr.args = %v, want the project (6-arg) form", scalr.Args)
	}
	if len(scalr.Env) != 0 {
		t.Errorf("scalr.env = %v, want empty (project entry replaced global wholesale)", scalr.Env)
	}
	// The project-only server and the global-only remote server both survive.
	if _, ok := cfg.MCPs["opa"]; !ok {
		t.Error("opa (project-only) should be present after the merge")
	}
	if ro := cfg.MCPs["remote-only"]; ro.URL != "https://example.com/mcp" || ro.Transport != "sse" {
		t.Errorf("remote-only = %+v, want url+sse to survive the merge", ro)
	}
}

// TestMCPStdioEnvExpandsAndAmbientWins confirms mcpStdioEnv (1) expands
// ${VAR} in declared env: values and (2) appends the ambient environment LAST,
// so on a colliding key exec's last-wins semantics pick the ambient value —
// matching cagent's MCP config loader.
func TestMCPStdioEnvExpandsAndAmbientWins(t *testing.T) {
	provider := environment.NewMapEnvProvider(map[string]string{"HOST": "example.scalr.io"})
	m := mcpServerConfig{Env: map[string]string{
		"SCALR_API_URL": "https://${HOST}/api",
		"SHARED":        "from-config",
	}}
	ambient := []string{"PATH=/usr/bin", "SHARED=from-ambient"}

	env, err := mcpStdioEnv(context.Background(), m, provider, ambient)
	if err != nil {
		t.Fatalf("mcpStdioEnv: %v", err)
	}

	// (1) ${HOST} resolved from the provider.
	if !slices.Contains(env, "SCALR_API_URL=https://example.scalr.io/api") {
		t.Errorf("expected expanded SCALR_API_URL, got %v", env)
	}
	// (2) declared SHARED precedes ambient SHARED, so the ambient value wins.
	iDecl := slices.Index(env, "SHARED=from-config")
	iAmb := slices.Index(env, "SHARED=from-ambient")
	if iDecl < 0 || iAmb < 0 || iDecl > iAmb {
		t.Errorf("ambient SHARED must come after declared (decl=%d amb=%d): %v", iDecl, iAmb, env)
	}
	// Ambient entries are carried through to the subprocess.
	if !slices.Contains(env, "PATH=/usr/bin") {
		t.Errorf("ambient PATH should be present, got %v", env)
	}
}

// TestLoadTurfConfigMalformed confirms a malformed file surfaces an error.
func TestLoadTurfConfigMalformed(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("TURF_HOME", home)
	writeFile(t, filepath.Join(home, turfConfigFileName), "model: [unterminated\n")

	if _, err := loadTurfConfig(cwd); err == nil {
		t.Fatal("expected an error for malformed YAML, got nil")
	}
}
