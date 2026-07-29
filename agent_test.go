package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestNewModelProvider covers the routing newModelProvider performs now that it
// goes through cagent's full provider registry instead of a hand-rolled switch
// over openai/anthropic/google. The cases exercise the paths that matter for
// turf: a keyless local model (dmr), a base-URL override, and a malformed model
// reference. None dial the network — the dmr case pins MODEL_RUNNER_HOST so DMR
// resolves its endpoint without probing, and the openai case never issues a
// request.
func TestNewModelProvider(t *testing.T) {
	ctx := context.Background()

	t.Run("dmr routes to the local Model Runner provider", func(t *testing.T) {
		// Pin the endpoint so DMR skips its docker-model status query and
		// connectivity probing (see resolveDMRBaseURL); keeps the test offline.
		t.Setenv("MODEL_RUNNER_HOST", "http://127.0.0.1:12434")

		p, err := newModelProvider(ctx, "dmr/ai/qwen3", "")
		if err != nil {
			t.Fatalf("newModelProvider(dmr/ai/qwen3): %v", err)
		}
		cfg := p.BaseConfig()
		if cfg.ModelConfig.Provider != "dmr" {
			t.Errorf("provider = %q, want dmr", cfg.ModelConfig.Provider)
		}
		if cfg.ModelConfig.Model != "ai/qwen3" {
			t.Errorf("model = %q, want ai/qwen3", cfg.ModelConfig.Model)
		}
		// The resolved endpoint comes from MODEL_RUNNER_HOST — proof the request
		// reached the DMR factory, not the OpenAI client that the old default
		// branch used for any unrecognized provider.
		if !strings.Contains(cfg.BaseURL, "127.0.0.1:12434") {
			t.Errorf("resolved BaseURL = %q, want it to contain the MODEL_RUNNER_HOST endpoint", cfg.BaseURL)
		}
	})

	t.Run("base URL threads through to the model config", func(t *testing.T) {
		// The OpenAI client needs no key at construction with the default
		// (empty) token_key; a dummy value keeps the test hermetic regardless.
		t.Setenv("OPENAI_API_KEY", "test-key")

		const baseURL = "http://localhost:8000/v1"
		p, err := newModelProvider(ctx, "openai/gpt-4o", baseURL)
		if err != nil {
			t.Fatalf("newModelProvider(openai/gpt-4o, %s): %v", baseURL, err)
		}
		if got := p.BaseConfig().ModelConfig.BaseURL; got != baseURL {
			t.Errorf("ModelConfig.BaseURL = %q, want %q", got, baseURL)
		}
	})

	t.Run("malformed model reference is rejected", func(t *testing.T) {
		_, err := newModelProvider(ctx, "claude", "")
		if err == nil {
			t.Fatal("newModelProvider(claude): expected an error, got nil")
		}
		if !strings.Contains(err.Error(), "invalid model") {
			t.Errorf("error = %q, want it to mention 'invalid model'", err.Error())
		}
	})
}

// TestModelProviderError locks in the actionable guidance shown when the model
// fails to start (most often a missing API key at launch): it must name the
// model, keep the underlying cause, feature the keyless local option, and link
// the provider docs.
func TestModelProviderError(t *testing.T) {
	cause := errors.New("GOOGLE_API_KEY or GEMINI_API_KEY environment variable is required")
	msg := modelProviderError("google/gemini-pro-latest", cause).Error()

	for _, want := range []string{
		"google/gemini-pro-latest", // names the selected model
		cause.Error(),              // preserves the underlying cause
		"dmr/ai/qwen3",             // features the keyless local option
		"https://docs.docker.com/ai/docker-agent/providers/overview/", // links the docs
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("modelProviderError message missing %q\n---\n%s", want, msg)
		}
	}
}
