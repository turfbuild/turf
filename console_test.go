package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// turf console hands its arguments to turf-mcp-server engine console, the
// server that keeps the token, and passes its answer through.
func TestConsoleRunsTheServer(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "turf-mcp-server")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"args: $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := flagMCPServer
	flagMCPServer = fake
	t.Cleanup(func() { flagMCPServer = old })

	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "args: engine console"},
		{[]string{"--print", "http://127.0.0.1:8873/w/abc"}, "args: engine console --print http://127.0.0.1:8873/w/abc"},
	} {
		cmd := newConsoleCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(tc.args)
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if got := strings.TrimSpace(out.String()); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.args, got, tc.want)
		}
	}
}

// A server that fails is an error, not a silent success.
func TestConsoleServerFails(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "turf-mcp-server")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'no local Turf engine has run' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := flagMCPServer
	flagMCPServer = fake
	t.Cleanup(func() { flagMCPServer = old })
	cmd := newConsoleCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(nil)
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(out.String(), "no local Turf engine") {
		t.Errorf("err %v, output %q", err, out.String())
	}
}
