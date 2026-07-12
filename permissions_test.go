package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tools/mcp"
)

func toolSet(names []string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

// isDestructive reports whether a tool's MCP annotations mark it destructive.
// DestructiveHint is *bool: nil (unspecified) is treated as not-destructive —
// turf's server sets an explicit true on the tools that mutate live infra.
func isDestructive(t tools.Tool) bool {
	return t.Annotations.DestructiveHint != nil && *t.Annotations.DestructiveHint
}

// destructiveButPreApproved names the tools that are server-annotated destructive
// yet deliberately pre-approved, because they authorize no new change — they only
// persist/finalize the result of already-approved effects. Kept as an explicit,
// reviewable exception so the "no destructive tool is auto-approved" guard still
// holds for every other tool.
var destructiveButPreApproved = map[string]struct{}{
	"turf_workspace_close": {}, // flush state + release lock; bookend of workspace_open
}

// TestPermissionListsConsistent checks the curated pre-approval lists are
// internally coherent, independent of whether turf-mcp-server is present: every
// entry carries the turf_ prefix and no tool is listed twice (a tool must be
// classified exactly once, as pre-approved or always-confirm).
func TestPermissionListsConsistent(t *testing.T) {
	seen := map[string]string{}
	check := func(list []string, label string) {
		for _, n := range list {
			if !strings.HasPrefix(n, "turf_") {
				t.Errorf("%s entry %q is missing the turf_ prefix", label, n)
			}
			if prev, ok := seen[n]; ok {
				t.Errorf("%q is listed more than once (%s and %s); classify it exactly once", n, prev, label)
			}
			seen[n] = label
		}
	}
	check(preApprovedTurfTools, "Allow")
	check(alwaysConfirmTurfTools, "Ask")
}

// TestPermissionListsMatchServerAnnotations is an integration drift guard: when
// turf-mcp-server is resolvable, it starts the server, reads the real tool
// annotations, and asserts the policy matches reality. It skips (does not fail)
// when the binary is absent, mirroring examples_test.go. It enforces:
//   - the safety-critical direction: no pre-approved tool is annotated destructive;
//   - every listed tool actually exists on the server (no stale entries); and
//   - every server tool is classified in exactly one list (catches drift when the
//     server adds a tool).
func TestPermissionListsMatchServerAnnotations(t *testing.T) {
	path, err := resolveMCPServer()
	if err != nil {
		t.Skipf("turf-mcp-server not resolvable: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ts := mcp.NewToolsetCommand("turf", path, []string{"--transport", "stdio"}, os.Environ(), "")
	if err := ts.Start(ctx); err != nil {
		t.Fatalf("start turf-mcp-server: %v", err)
	}
	defer func() { _ = ts.Stop(context.Background()) }()

	toolList, err := ts.Tools(ctx)
	if err != nil {
		t.Fatalf("list turf-mcp-server tools: %v", err)
	}

	byName := make(map[string]tools.Tool, len(toolList))
	for _, tl := range toolList {
		byName[tl.Name] = tl
	}
	allow := toolSet(preApprovedTurfTools)
	ask := toolSet(alwaysConfirmTurfTools)

	for _, n := range preApprovedTurfTools {
		tl, ok := byName[n]
		if !ok {
			t.Errorf("pre-approved %q is not a server tool (stale entry)", n)
			continue
		}
		if _, exempt := destructiveButPreApproved[n]; !exempt && isDestructive(tl) {
			t.Errorf("pre-approved %q is annotated destructive — it must not be auto-approved", n)
		}
	}
	for _, n := range alwaysConfirmTurfTools {
		if _, ok := byName[n]; !ok {
			t.Errorf("always-confirm %q is not a server tool (stale entry)", n)
		}
	}

	// Keep the exception set honest: each entry must be pre-approved and still
	// actually destructive on the server (else the exception is stale — drop it).
	for n := range destructiveButPreApproved {
		if _, ok := allow[n]; !ok {
			t.Errorf("%q is in destructiveButPreApproved but not in preApprovedTurfTools", n)
		}
		if tl, ok := byName[n]; ok && !isDestructive(tl) {
			t.Errorf("%q is exempted as destructive-but-pre-approved, but the server no longer marks it destructive — drop the exception", n)
		}
	}

	for name := range byName {
		_, inAllow := allow[name]
		_, inAsk := ask[name]
		switch {
		case inAllow && inAsk:
			t.Errorf("server tool %q is in both lists", name)
		case !inAllow && !inAsk:
			t.Errorf("server tool %q is unclassified — add it to preApprovedTurfTools or alwaysConfirmTurfTools", name)
		}
	}
}
