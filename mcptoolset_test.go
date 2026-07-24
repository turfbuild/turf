package main

import (
	"strings"
	"testing"

	"github.com/docker/docker-agent/pkg/tools"
)

// allTurfPermissionTools is the full set of turf tools the CLI knows about. Every
// turf tool is pre-approved, so preApprovedTurfTools is that complete set on its
// own (agentConfirmTurfTools is a subset of it). turfToolInfo must cover exactly
// this set.
func allTurfPermissionTools() []string {
	return append([]string{}, preApprovedTurfTools...)
}

// TestTurfToolInfoMatchesPermissionLists is a drift guard tying turfToolInfo to
// the permission policy in permissions.go: every known turf tool must have a
// turfToolInfo entry with a non-empty group and title, and every entry in
// turfToolInfo must be a known tool (no typos / stale entries). When the server
// gains a tool, it must be added to both places — this fails loudly if not.
func TestTurfToolInfoMatchesPermissionLists(t *testing.T) {
	known := toolSet(allTurfPermissionTools()) // prefixed names, e.g. "turf_plan_approve"

	// Every known tool has group + title metadata.
	for _, prefixed := range allTurfPermissionTools() {
		bare := strings.TrimPrefix(prefixed, appName+"_")
		info, ok := turfToolInfo[bare]
		if !ok {
			t.Errorf("tool %q has no turfToolInfo entry (add its group + title)", prefixed)
			continue
		}
		if info.group == "" {
			t.Errorf("tool %q has an empty group", prefixed)
		}
		if info.title == "" {
			t.Errorf("tool %q has an empty title", prefixed)
		}
	}

	// Every turfToolInfo entry corresponds to a known tool.
	for bare := range turfToolInfo {
		if _, ok := known[appName+"_"+bare]; !ok {
			t.Errorf("turfToolInfo has %q, which is not a known turf tool (stale entry or typo)", bare)
		}
	}
}

// TestTurfCategoryFallback: an unknown tool lands in the plain "turf" bucket and
// keeps its bare name for display, rather than being dropped or panicking.
func TestTurfCategoryFallback(t *testing.T) {
	if got := turfCategory("some_unknown_tool"); got != appName {
		t.Errorf("turfCategory(unknown) = %q, want %q", got, appName)
	}
	if got := turfToolTitle("some_unknown_tool"); got != "some_unknown_tool" {
		t.Errorf("turfToolTitle(unknown) = %q, want the bare name", got)
	}
}

// TestDecorateTurfTool checks the exact per-tool transform used by Tools():
// the display Title becomes the friendly label, the Category is the functional
// sub-group, and the model-facing Name is left untouched.
func TestDecorateTurfTool(t *testing.T) {
	tool := tools.Tool{Name: "turf_plan_approve"}
	decorateTurfTool(&tool)

	if tool.Name != "turf_plan_approve" {
		t.Errorf("Name mutated to %q; it must stay the prefixed, model-facing name", tool.Name)
	}
	if want := "Approve Plan"; tool.Annotations.Title != want {
		t.Errorf("Title = %q, want %q (friendly display label)", tool.Annotations.Title, want)
	}
	if want := appName + " · plan"; tool.Category != want {
		t.Errorf("Category = %q, want %q", tool.Category, want)
	}
}
