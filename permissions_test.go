package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/docker/docker-agent/pkg/permissions"
	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tools/builtin/filesystem"
	"github.com/docker/docker-agent/pkg/tools/builtin/memory"
	"github.com/docker/docker-agent/pkg/tools/builtin/todo"
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

// destructiveNoConfirm names the tools that are server-annotated destructive yet
// deliberately neither permission-gated nor persona-confirmed, because they
// authorize no new change — they only persist/finalize the result of
// already-approved effects. Kept as an explicit, reviewable exception so the
// "every destructive tool is accounted for" guard still holds for every other
// destructive tool.
var destructiveNoConfirm = map[string]struct{}{
	"turf_workspace_close": {}, // flush state + release lock; bookend of workspace_open
}

// TestPermissionListsConsistent checks the curated lists are internally coherent,
// independent of whether turf-mcp-server is present: every entry carries the
// turf_ prefix, preApprovedTurfTools has no duplicates, and agentConfirmTurfTools
// (a documentation/drift subset — all turf tools are pre-approved) is fully
// contained in preApprovedTurfTools.
func TestPermissionListsConsistent(t *testing.T) {
	allow := toolSet(preApprovedTurfTools)

	seen := map[string]struct{}{}
	for _, n := range preApprovedTurfTools {
		if !strings.HasPrefix(n, "turf_") {
			t.Errorf("pre-approved entry %q is missing the turf_ prefix", n)
		}
		if _, dup := seen[n]; dup {
			t.Errorf("pre-approved %q is listed more than once", n)
		}
		seen[n] = struct{}{}
	}

	for _, n := range agentConfirmTurfTools {
		if !strings.HasPrefix(n, "turf_") {
			t.Errorf("agent-confirm entry %q is missing the turf_ prefix", n)
		}
		if _, ok := allow[n]; !ok {
			t.Errorf("agent-confirm %q must also be pre-approved (all turf tools are)", n)
		}
	}
}

// assertBuiltinList ties one per-toolset pre-approval list to that toolset's own
// tool-name constants, so an upstream rename (or a new tool) fails CI until the list
// is consciously updated. It also rejects duplicates and accidental turf_ entries
// (those belong in preApprovedTurfTools).
func assertBuiltinList(t *testing.T, toolset string, got, wantNames []string) {
	t.Helper()
	want := toolSet(wantNames)
	seen := map[string]struct{}{}
	for _, n := range got {
		if strings.HasPrefix(n, "turf_") {
			t.Errorf("%s pre-approval %q looks like a turf_ server tool — it belongs in preApprovedTurfTools", toolset, n)
		}
		if _, dup := seen[n]; dup {
			t.Errorf("%s pre-approval %q is listed more than once", toolset, n)
		}
		seen[n] = struct{}{}
	}
	for n := range want {
		if _, ok := seen[n]; !ok {
			t.Errorf("%s tool %q is not pre-approved — add it to the pre-approval list", toolset, n)
		}
	}
	for n := range seen {
		if _, ok := want[n]; !ok {
			t.Errorf("%s pre-approval %q is not a real %s toolset tool (stale entry)", toolset, n, toolset)
		}
	}
}

// TestBuiltinPreApprovalMatchesToolsets ties each per-toolset pre-approval list to
// the toolset package's own tool-name constants, so a rename/addition upstream
// forces a conscious update to the turf lists.
func TestBuiltinPreApprovalMatchesToolsets(t *testing.T) {
	assertBuiltinList(t, "memory", preApprovedMemoryTools, []string{
		memory.ToolNameAddMemory,
		memory.ToolNameGetMemories,
		memory.ToolNameDeleteMemory,
		memory.ToolNameSearchMemories,
		memory.ToolNameUpdateMemory,
	})
	assertBuiltinList(t, "filesystem", preApprovedFilesystemTools, []string{
		filesystem.ToolNameReadFile,
		filesystem.ToolNameReadMultipleFiles,
		filesystem.ToolNameEditFile,
		filesystem.ToolNameWriteFile,
		filesystem.ToolNameDirectoryTree,
		filesystem.ToolNameListDirectory,
		filesystem.ToolNameSearchFilesContent,
		filesystem.ToolNameMkdir,
		filesystem.ToolNameRmdir,
	})
	assertBuiltinList(t, "todo", preApprovedTodoTools, []string{
		todo.ToolNameCreateTodo,
		todo.ToolNameCreateTodos,
		todo.ToolNameUpdateTodos,
		todo.ToolNameListTodos,
	})
}

// TestPreApprovedToolsChecker pins the end-to-end pre-approval behavior of the
// team-level permission checker (see createAgentRuntime): representative turf,
// memory, filesystem (incl. a writer), and todo tools are Allowed, while an unknown
// tool is not (falls through to Ask). This exercises the same
// permissions.NewChecker(preApprovedTools()) the runtime installs, without a live
// server.
func TestPreApprovedToolsChecker(t *testing.T) {
	checker := permissions.NewChecker(&latest.PermissionsConfig{Allow: preApprovedTools()})

	allowed := []string{
		"turf_plan_approve", "turf_effect_apply",
		"add_memory", "update_memory", "delete_memory",
		"write_file", "edit_file", "create_directory", "remove_directory",
		"create_todo", "update_todos",
	}
	for _, name := range allowed {
		if got := checker.Check(name); got != permissions.Allow {
			t.Errorf("Check(%q) = %v, want Allow (pre-approved)", name, got)
		}
	}

	for _, notAllowed := range []string{"some_unknown_mcp_tool", "run_skill"} {
		if got := checker.Check(notAllowed); got == permissions.Allow {
			t.Errorf("Check(%q) = Allow, want a non-Allow decision (only pre-approved tools are allowed)", notAllowed)
		}
	}
}

// TestPreApprovedToolsIsUnion guards that preApprovedTools is exactly the union of
// the turf and builtin lists (turf first) and that mutating the result does not
// corrupt the source slices.
func TestPreApprovedToolsIsUnion(t *testing.T) {
	builtins := preApprovedBuiltinTools()
	all := preApprovedTools()
	if len(all) != len(preApprovedTurfTools)+len(builtins) {
		t.Fatalf("preApprovedTools has %d entries, want %d (turf) + %d (builtin)",
			len(all), len(preApprovedTurfTools), len(builtins))
	}
	set := toolSet(all)
	for _, n := range preApprovedTurfTools {
		if _, ok := set[n]; !ok {
			t.Errorf("preApprovedTools is missing turf tool %q", n)
		}
	}
	for _, n := range builtins {
		if _, ok := set[n]; !ok {
			t.Errorf("preApprovedTools is missing builtin tool %q", n)
		}
	}
	// Mutating the returned slice must not touch preApprovedTurfTools.
	first := preApprovedTurfTools[0]
	all[0] = "MUTATED"
	if preApprovedTurfTools[0] != first {
		t.Error("preApprovedTools() shares backing storage with preApprovedTurfTools")
	}
}

// TestPermissionListsMatchServerAnnotations is an integration drift guard: when
// turf-mcp-server is resolvable, it starts the server, reads the real tool
// annotations, and asserts the policy matches reality. It skips (does not fail)
// when the binary is absent, mirroring examples_test.go. It enforces:
//   - every server tool is pre-approved (fail-safe completeness: a new tool would
//     otherwise be unclassified — it must be consciously added to Allow);
//   - every pre-approved / agent-confirm tool actually exists on the server (no
//     stale entries); and
//   - every server-destructive tool is accounted for — either the persona confirms
//     it (agentConfirmTurfTools) or it is an explicit destructiveNoConfirm carve-out
//     — so a newly-destructive server tool forces a confirmation decision.
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
	confirm := toolSet(agentConfirmTurfTools)

	// No stale entries: everything the CLI lists must be a real server tool.
	for _, n := range preApprovedTurfTools {
		if _, ok := byName[n]; !ok {
			t.Errorf("pre-approved %q is not a server tool (stale entry)", n)
		}
	}
	for _, n := range agentConfirmTurfTools {
		if _, ok := byName[n]; !ok {
			t.Errorf("agent-confirm %q is not a server tool (stale entry)", n)
		}
	}

	// Keep the exception set honest: each entry must be pre-approved and still
	// actually destructive on the server (else the exception is stale — drop it).
	for n := range destructiveNoConfirm {
		if _, ok := allow[n]; !ok {
			t.Errorf("%q is in destructiveNoConfirm but not in preApprovedTurfTools", n)
		}
		if tl, ok := byName[n]; ok && !isDestructive(tl) {
			t.Errorf("%q is exempted as destructive-but-no-confirm, but the server no longer marks it destructive — drop the exception", n)
		}
	}

	// Fail-safe completeness + destructive coverage, iterating every server tool.
	for name, tl := range byName {
		if _, ok := allow[name]; !ok {
			t.Errorf("server tool %q is not pre-approved — add it to preApprovedTurfTools (and turfToolInfo)", name)
		}
		if isDestructive(tl) {
			_, confirmed := confirm[name]
			_, exempt := destructiveNoConfirm[name]
			if !confirmed && !exempt {
				t.Errorf("server tool %q is destructive but not accounted for — add it to agentConfirmTurfTools (persona confirms it) or destructiveNoConfirm", name)
			}
		}
	}
}
