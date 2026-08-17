package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tui/animation"
	"github.com/docker/docker-agent/pkg/tui/service"
	"github.com/docker/docker-agent/pkg/tui/types"
)

var (
	ansiRE  = regexp.MustCompile("\x1b\\[[0-9;]*m")
	spaceRE = regexp.MustCompile(`[ \t]+`)
)

// plainNorm strips ANSI styling and collapses runs of spaces/tabs so tests can assert on
// rendered structure without depending on exact =-alignment padding or the ANSI resets
// that sit between adjacent styled runs.
func plainNorm(s string) string {
	return spaceRE.ReplaceAllString(ansiRE.ReplaceAllString(s, ""), " ")
}

// shownState / hiddenState drive the two detail levels. StaticSessionState pins
// HideToolResults() to false (detailed); we embed+override for the compact case.
type hiddenState struct{ service.StaticSessionState }

func (hiddenState) HideToolResults() bool { return true }

func renderFor(name, content string, ss service.SessionStateReader) string {
	msg := &types.Message{
		Content:        content,
		ToolStatus:     types.ToolStatusCompleted,
		ToolCall:       tools.ToolCall{Function: tools.FunctionCall{Name: name}},
		ToolDefinition: tools.Tool{Name: name},
	}
	b := turfToolRenderers()[name](animation.NewRuntime(), msg, ss)
	b.SetSize(120, 1)
	return b.View()
}

// renderErr renders a failed tool call: ToolStatusError with the framework's error
// text in Content, exactly as cagent delivers a tool error to the renderer.
func renderErr(name, content string, ss service.SessionStateReader) string {
	return renderErrArgs(name, content, "", ss)
}

// renderErrArgs is renderErr with request arguments (the JSON cagent puts in
// ToolCall.Function.Arguments), so tests can exercise the request-target context the
// error line leads with.
func renderErrArgs(name, content, argsJSON string, ss service.SessionStateReader) string {
	msg := &types.Message{
		Content:        content,
		ToolStatus:     types.ToolStatusError,
		ToolCall:       tools.ToolCall{Function: tools.FunctionCall{Name: name, Arguments: argsJSON}},
		ToolDefinition: tools.Tool{Name: name},
	}
	b := turfToolRenderers()[name](animation.NewRuntime(), msg, ss)
	b.SetSize(120, 10)
	return b.View()
}

func TestResourcePlan_CompactVsDetailed(t *testing.T) {
	const content = `{
		"resource_addr": "random_pet.this",
		"provider": "random",
		"action": "+",
		"action_reason": "resource not in state",
		"before": null,
		"after": {"id": "x", "length": 2, "separator": "-"}
	}`

	compact := renderFor("turf_declare_resource", content, hiddenState{})
	if !strings.Contains(compact, "random_pet.this") || !strings.Contains(compact, "create") {
		t.Fatalf("compact missing addr/action: %q", compact)
	}
	if strings.Contains(compact, "provider") || strings.Contains(compact, "reason:") {
		t.Fatalf("compact should not include detail block: %q", compact)
	}
	if strings.Contains(strings.TrimRight(compact, " "), "\n") {
		t.Fatalf("compact should be a single line: %q", compact)
	}

	detailed := renderFor("turf_declare_resource", content, service.StaticSessionState{})
	if !strings.Contains(detailed, "provider") || !strings.Contains(detailed, "reason:") {
		t.Fatalf("detailed missing detail block: %q", detailed)
	}
	// The expanded view unfolds the before→after diff with values (the quoted
	// values appear only in the diff, not the compact summary).
	for _, want := range []string{"separator", `"-"`, `"x"`} {
		if !strings.Contains(detailed, want) {
			t.Fatalf("expanded diff missing %q: %q", want, detailed)
		}
	}
}

func TestResourcePlan_ReplaceDiff(t *testing.T) {
	// A ~ change should render old → new with both values.
	const content = `{
		"resource_addr": "random_pet.first",
		"provider": "random",
		"action": "~",
		"before": {"prefix": "alpha", "length": 2},
		"after": {"prefix": "gamma", "length": 2}
	}`
	out := renderFor("turf_declare_resource", content, service.StaticSessionState{})
	for _, want := range []string{"prefix", `"alpha"`, "→", `"gamma"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("replace diff missing %q: %q", want, out)
		}
	}
	// length is unchanged and must be omitted from the diff.
	if strings.Contains(out, "length") {
		t.Fatalf("unchanged attr should not appear: %q", out)
	}
}

func TestResourcePlan_TitleAndAction(t *testing.T) {
	const content = `{"resource_addr": "random_pet.my_pet", "action": "+", "after": {"length": 2}}`
	out := renderFor("turf_declare_resource", content, hiddenState{})
	if !strings.Contains(out, "Declare Resource") || !strings.Contains(out, "create") {
		t.Fatalf("expected 'Declare Resource … create' wording: %q", out)
	}
}

func TestEffectApply_ReadyNextBelowNewState(t *testing.T) {
	const content = `{"kind": "create", "state": "done", "resource_addr": "a.b",
		"new_state": {"id": "x"}, "ready": ["+/c.d/create"]}`
	out := renderFor("turf_effect_apply", content, service.StaticSessionState{})
	ns := strings.Index(out, "new state")
	rn := strings.Index(out, "ready to effect")
	if ns < 0 || rn < 0 {
		t.Fatalf("expected both sections: %q", out)
	}
	if ns > rn {
		t.Fatalf("new state should precede ready to effect: %q", out)
	}
}

func TestProviderDescribe_Summary(t *testing.T) {
	const content = `{"type": "random_pet", "description": "Generates random pet names.",
		"properties": {"length": {"type": "number", "usage": "optional"},
		"id": {"type": "string", "usage": "computed", "sensitive": false}},
		"required": []}`
	out := renderFor("turf_provider_describe", content, service.StaticSessionState{})
	for _, want := range []string{"Describe Provider", "random_pet", "attr", "length", "optional", "Generates random pet names"} {
		if !strings.Contains(out, want) {
			t.Fatalf("provider_describe missing %q: %q", want, out)
		}
	}
}

func TestProviderLoad_Summary(t *testing.T) {
	const content = `{"name": "random", "source": "hashicorp/random", "resolved_version": "3.9.0"}`
	out := renderFor("turf_provider_load", content, service.StaticSessionState{})
	for _, want := range []string{"Load Provider", "random", "3.9.0", "hashicorp/random"} {
		if !strings.Contains(out, want) {
			t.Fatalf("provider_load missing %q: %q", want, out)
		}
	}
}

func TestProviderLoad_RequestedConstraintDetail(t *testing.T) {
	// The requested version constraint is an arg (not in the result); it surfaces
	// in the expanded view only when it differs from what actually resolved.
	msg := &types.Message{
		Content:    `{"name": "random", "source": "hashicorp/random", "resolved_version": "3.9.0"}`,
		ToolStatus: types.ToolStatusCompleted,
		ToolCall: tools.ToolCall{Function: tools.FunctionCall{
			Name:      "turf_provider_load",
			Arguments: `{"name": "random", "source": "hashicorp/random", "version": ">= 3.0"}`,
		}},
		ToolDefinition: tools.Tool{Name: "turf_provider_load"},
	}

	b := turfToolRenderers()["turf_provider_load"](animation.NewRuntime(), msg, service.StaticSessionState{})
	b.SetSize(120, 1)
	detailed := b.View()
	if !strings.Contains(detailed, "requested") || !strings.Contains(detailed, ">= 3.0") {
		t.Fatalf("expanded provider_load should show requested constraint: %q", detailed)
	}

	// Compact view (results hidden) must not carry the detail.
	b = turfToolRenderers()["turf_provider_load"](animation.NewRuntime(), msg, hiddenState{})
	b.SetSize(120, 1)
	if compact := b.View(); strings.Contains(compact, "requested") {
		t.Fatalf("compact provider_load should omit requested constraint: %q", compact)
	}
}

func TestProviderSearch_Summary(t *testing.T) {
	const content = `{"providers": [
		{"name": "aws", "version": "5.1.0", "description": "Amazon Web Services"},
		{"name": "awscc", "version": "1.0.0", "description": "AWS Cloud Control"}]}`
	out := renderFor("turf_provider_search", content, service.StaticSessionState{})
	for _, want := range []string{"Search Providers", "2 result(s)", "aws", "5.1.0", "Amazon Web Services"} {
		if !strings.Contains(out, want) {
			t.Fatalf("provider_search missing %q: %q", want, out)
		}
	}
}

func TestWorkspaceList_Summary(t *testing.T) {
	const content = `{"workspaces": ["default", "staging", "prod"]}`
	out := renderFor("turf_workspace_list", content, service.StaticSessionState{})
	for _, want := range []string{"List Workspaces", "3 found", "staging"} {
		if !strings.Contains(out, want) {
			t.Fatalf("workspace_list missing %q: %q", want, out)
		}
	}
	empty := renderFor("turf_workspace_list", `{"workspaces": []}`, hiddenState{})
	if !strings.Contains(empty, "none") {
		t.Fatalf("empty workspace_list should say none: %q", empty)
	}
}

func TestWorkspaceShow_Summary(t *testing.T) {
	const content = `{"workspaces": [{"workspace_alias": "prod", "backend_type": "s3",
		"name": "main", "resource_count": 7, "uncommitted_changes": 2,
		"active_phase_id": "p3", "active_phase_status": "applied"}]}`
	out := renderFor("turf_workspace_show", content, service.StaticSessionState{})
	for _, want := range []string{"Show Workspace", "prod", "7 resource(s)", "2 uncommitted", "s3", "phase p3", "applied"} {
		if !strings.Contains(out, want) {
			t.Fatalf("workspace_show missing %q: %q", want, out)
		}
	}
}

func TestOutputs_SensitiveMasked(t *testing.T) {
	const content = `{"workspace_alias": "", "outputs": {
		"url": {"value": "https://x", "sensitive": false},
		"token": {"value": "__cty_sensitive__", "sensitive": true}}}`
	out := renderFor("turf_outputs", content, service.StaticSessionState{})
	for _, want := range []string{"Read Outputs", "2 declared", "1 sensitive", "url", "(sensitive)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("outputs missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "__cty_sensitive__") {
		t.Fatalf("sensitive sentinel leaked: %q", out)
	}
}

func TestModuleOutputs_Summary(t *testing.T) {
	const content = `{"address": "module.rg", "outputs": {"id": "rg-1", "location": "eastus"},
		"missing_resources": ["azurerm_resource_group.this"]}`
	out := renderFor("turf_module_outputs", content, service.StaticSessionState{})
	for _, want := range []string{"Read Module Outputs", "module.rg", "2 output(s)", "not yet applied", "azurerm_resource_group.this"} {
		if !strings.Contains(out, want) {
			t.Fatalf("module_outputs missing %q: %q", want, out)
		}
	}
}

func TestDatasourceRead_Summary(t *testing.T) {
	const content = `{"resource_addr": "data.aws_ami.latest",
		"state": {"id": "ami-123", "name": "ubuntu"}}`
	out := renderFor("turf_datasource_read", content, service.StaticSessionState{})
	for _, want := range []string{"data", "data.aws_ami.latest", "2 attr(s)", "ami-123"} {
		if !strings.Contains(out, want) {
			t.Fatalf("datasource_read missing %q: %q", want, out)
		}
	}
}

// TestDeclareDatasource_ReadVerdicts pins the renderer to the server's actual
// declareDatasourceResult: the declaration outcome plus a data_source_reads[]
// *classification* per expanded instance. The value never rides this response, so a
// successful declare must not claim to show attributes.
func TestDeclareDatasource_ReadVerdicts(t *testing.T) {
	t.Run("declared and read", func(t *testing.T) {
		const content = `{"phase_id": "p1", "resource_addr": "data.aws_ami.latest",
			"resource_type": "aws_ami", "declared": true,
			"data_source_reads": [{"address": "data.aws_ami.latest", "action": "read"}]}`
		out := plainNorm(renderFor("turf_declare_datasource", content, service.StaticSessionState{}))
		for _, want := range []string{"Declare Data Source", "data.aws_ami.latest", "declared", "read", "reads:"} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q: %q", want, out)
			}
		}
		if strings.Contains(out, "attr(s)") {
			t.Fatalf("declare_datasource returns no value, so it must not report attrs: %q", out)
		}
	})

	t.Run("deferred on an unapplied depends_on target", func(t *testing.T) {
		const content = `{"resource_addr": "data.aws_instances.web", "declared": true,
			"replan": ["aws_lb.front"],
			"data_source_reads": [{"address": "data.aws_instances.web", "action": "deferred",
				"reason": "dependency_pending", "depends_on": ["aws_instance.web"]}]}`
		out := plainNorm(renderFor("turf_declare_datasource", content, service.StaticSessionState{}))
		// The verdict leads the one-line view; the actionable "what to apply to clear
		// it" belongs to the expansion, as declare_resource does with its reason.
		for _, want := range []string{"declared", "deferred", "1 replan", "dependency_pending", "waiting on", "aws_instance.web", "replan:", "aws_lb.front"} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q: %q", want, out)
			}
		}
		compact := plainNorm(renderFor("turf_declare_datasource", content, hiddenState{}))
		if !strings.Contains(compact, "deferred") {
			t.Fatalf("compact view must still carry the verdict: %q", compact)
		}
		if strings.Contains(compact, "waiting on") {
			t.Fatalf("compact view should not include the detail block: %q", compact)
		}
	})

	t.Run("count expansion tallies per action", func(t *testing.T) {
		const content = `{"resource_addr": "data.aws_subnet.each", "declared": true,
			"data_source_reads": [
				{"address": "data.aws_subnet.each[0]", "action": "read"},
				{"address": "data.aws_subnet.each[1]", "action": "read"},
				{"address": "data.aws_subnet.each[2]", "action": "deferred", "reason": "config_unknown"}]}`
		out := plainNorm(renderFor("turf_declare_datasource", content, service.StaticSessionState{}))
		for _, want := range []string{"2 read", "1 deferred", "data.aws_subnet.each[2]", "config_unknown"} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q: %q", want, out)
			}
		}
	})

	t.Run("provider error on the read", func(t *testing.T) {
		const content = `{"resource_addr": "data.aws_ami.bad", "declared": true,
			"data_source_reads": [{"address": "data.aws_ami.bad", "action": "error",
				"error": "no AMI matched the filter"}]}`
		out := plainNorm(renderFor("turf_declare_datasource", content, service.StaticSessionState{}))
		for _, want := range []string{"declared", "error", "no AMI matched the filter"} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q: %q", want, out)
			}
		}
	})

	// The warning arm: the tool succeeded and the declaration stands, but the
	// configuration would not walk so nothing was read. A bare "declared" would read
	// as fully done — especially compact, where the detail block is hidden.
	t.Run("declared but not read", func(t *testing.T) {
		const content = `{"resource_addr": "data.aws_ami.latest", "declared": true,
			"warning": "declared, but the configuration did not walk, so data.aws_ami.latest was not read: module.net not installed — replan once the configuration walks"}`
		compact := plainNorm(renderFor("turf_declare_datasource", content, hiddenState{}))
		for _, want := range []string{"declared", "not read"} {
			if !strings.Contains(compact, want) {
				t.Fatalf("compact missing %q: %q", want, compact)
			}
		}
		detailed := plainNorm(renderFor("turf_declare_datasource", content, service.StaticSessionState{}))
		if !strings.Contains(detailed, "did not walk") {
			t.Fatalf("detailed should carry the warning text: %q", detailed)
		}
	})

	t.Run("removed", func(t *testing.T) {
		const content = `{"resource_addr": "data.aws_ami.latest", "removed": true,
			"replan": ["aws_instance.web"]}`
		out := plainNorm(renderFor("turf_declare_datasource", content, service.StaticSessionState{}))
		for _, want := range []string{"removed", "1 replan", "aws_instance.web"} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q: %q", want, out)
			}
		}
		// Nothing is read on a remove, so there is no verdict to report.
		if strings.Contains(out, "reads:") {
			t.Fatalf("a remove reads nothing and must not show a reads section: %q", out)
		}
	})
}

func TestResourcePlan_ReplaceCBDvsDTC(t *testing.T) {
	// create_before_destroy=true → ± ; false → ∓.
	cbd := `{"resource_addr": "a.b", "action": "replace", "create_before_destroy": true, "before": {"x": 1}, "after": {"x": 2}}`
	dtc := `{"resource_addr": "a.b", "action": "replace", "create_before_destroy": false, "before": {"x": 1}, "after": {"x": 2}}`
	out := renderFor("turf_declare_resource", cbd, hiddenState{})
	if !strings.Contains(out, "replace") || !strings.Contains(out, "±") {
		t.Fatalf("CBD replace should show ±: %q", out)
	}
	if strings.Contains(out, "∓") {
		t.Fatalf("CBD replace should not show ∓: %q", out)
	}
	out = renderFor("turf_declare_resource", dtc, hiddenState{})
	if !strings.Contains(out, "∓") || strings.Contains(out, "±") {
		t.Fatalf("DTC replace should show ∓ not ±: %q", out)
	}
}

func TestModulePlan_TallySplitsReplace(t *testing.T) {
	const content = `{"phase_id": "ph_001", "resources": [
		{"address": "a.x", "action": "replace", "create_before_destroy": true},
		{"address": "a.y", "action": "replace", "create_before_destroy": false},
		{"address": "a.z", "action": "create"}
	]}`
	out := renderFor("turf_declare_module", content, hiddenState{})
	for _, want := range []string{"+1", "±1", "∓1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("tally should split CBD/DTC, missing %q: %q", want, out)
		}
	}
}

func TestModulePlan_Tally(t *testing.T) {
	const content = `{
		"phase_id": "ph_001",
		"resources": [
			{"address": "a.x", "action": "+"},
			{"address": "a.y", "action": "+"},
			{"address": "a.z", "action": "~"}
		],
		"outputs": {"url": "v"}
	}`
	out := renderFor("turf_declare_module", content, hiddenState{})
	if !strings.Contains(out, "+2") || !strings.Contains(out, "~1") {
		t.Fatalf("tally wrong: %q", out)
	}
	if !strings.Contains(out, "1 output") {
		t.Fatalf("missing outputs count: %q", out)
	}
}

// TestPlanSummary_DataSourceReads covers data_source_reads[] on the shared walk
// summary — plan_new, replan and declare_module all return it. Unlike
// declare_datasource, a walk reads every declared data source, so the one-line view
// reports only what did *not* resolve; the expansion lists them all.
func TestPlanSummary_DataSourceReads(t *testing.T) {
	const content = `{"phase_id": "ph_001", "path": "infra/prod",
		"resources": [{"address": "aws_instance.web", "action": "create"}],
		"data_source_reads": [
			{"address": "data.aws_ami.latest", "action": "read"},
			{"address": "data.aws_instances.web", "action": "deferred",
				"reason": "dependency_pending", "depends_on": ["aws_instance.web"]},
			{"address": "data.aws_ami.bad", "action": "error", "error": "no AMI matched"}]}`

	for _, name := range []string{"turf_plan_new", "turf_replan", "turf_declare_module"} {
		t.Run(name, func(t *testing.T) {
			compact := plainNorm(renderFor(name, content, hiddenState{}))
			// "data" qualifies the count so it cannot be read as the resource
			// deferral tally sitting next to it on the same line.
			for _, want := range []string{"+1", "1 data deferred", "1 data error"} {
				if !strings.Contains(compact, want) {
					t.Fatalf("compact missing %q: %q", want, compact)
				}
			}
			// A plain read is the ordinary case and must not spend summary width.
			if strings.Contains(compact, "data read") {
				t.Fatalf("summary should not count plain reads: %q", compact)
			}

			detailed := plainNorm(renderFor(name, content, service.StaticSessionState{}))
			for _, want := range []string{
				"data sources:", "data.aws_ami.latest", "read",
				"data.aws_instances.web", "dependency_pending", "waiting on", "aws_instance.web",
				"data.aws_ami.bad", "no AMI matched",
			} {
				if !strings.Contains(detailed, want) {
					t.Fatalf("detailed missing %q: %q", want, detailed)
				}
			}
		})
	}
}

// An all-read walk is the ordinary case: it should cost the summary line nothing,
// while the expansion still says which data sources were read.
func TestPlanSummary_AllReadCostsNoSummaryWidth(t *testing.T) {
	const content = `{"phase_id": "ph_001", "resources": [{"address": "a.x", "action": "create"}],
		"data_source_reads": [{"address": "data.aws_ami.latest", "action": "read"}]}`
	compact := plainNorm(renderFor("turf_replan", content, hiddenState{}))
	if strings.Contains(compact, "data") {
		t.Fatalf("an all-read walk should add nothing to the summary: %q", compact)
	}
	detailed := plainNorm(renderFor("turf_replan", content, service.StaticSessionState{}))
	if !strings.Contains(detailed, "data sources:") || !strings.Contains(detailed, "data.aws_ami.latest") {
		t.Fatalf("expansion should still list the reads: %q", detailed)
	}
}

func TestOutputsPlan_UnknownAndSensitive(t *testing.T) {
	const content = `{
		"phase_id": "ph_001",
		"outputs": {"pet_name": "__cty_unknown__", "region": "us-east-1", "token": "__cty_sensitive__"},
		"unknown": ["pet_name"]
	}`

	compact := renderFor("turf_declare_outputs", content, hiddenState{})
	for _, want := range []string{"Declare Outputs", "3 declared", "1 known after apply", "1 sensitive"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact missing %q: %q", want, compact)
		}
	}
	if strings.Contains(strings.TrimRight(compact, " "), "\n") {
		t.Fatalf("compact should be a single line: %q", compact)
	}
	if strings.Contains(compact, "us-east-1") {
		t.Fatalf("compact should not dump output values: %q", compact)
	}

	detailed := renderFor("turf_declare_outputs", content, service.StaticSessionState{})
	for _, want := range []string{"pet_name", "known after apply", "region", `"us-east-1"`, "token", "(sensitive)"} {
		if !strings.Contains(detailed, want) {
			t.Fatalf("detailed missing %q: %q", want, detailed)
		}
	}
	// The raw sentinel must be masked, never shown verbatim.
	if strings.Contains(detailed, "__cty_sensitive__") {
		t.Fatalf("sensitive sentinel leaked into detail: %q", detailed)
	}
}

func TestEffectApply_State(t *testing.T) {
	const content = `{"kind": "create", "state": "done", "resource_addr": "random_pet.this", "phase_status": "complete"}`
	out := renderFor("turf_effect_apply", content, hiddenState{})
	if !strings.Contains(out, "random_pet.this") || !strings.Contains(out, "done") {
		t.Fatalf("effect apply missing fields: %q", out)
	}
}

func TestEffectApply_ReadySetFormatted(t *testing.T) {
	// The ready set is formatted (symbol / addr / op split), and the noisy
	// execution URI is dropped from the unfold.
	const content = `{"kind": "destroy", "state": "done", "resource_addr": "random_string.demo2",
		"phase_status": "executing", "ready": ["±/random_string.demo2/create"],
		"execution_uri": "turf://workspaces/demo/phases/ph_002/execution"}`
	out := renderFor("turf_effect_apply", content, service.StaticSessionState{})
	if !strings.Contains(out, "random_string.demo2") || !strings.Contains(out, "create") {
		t.Fatalf("ready set not formatted: %q", out)
	}
	if strings.Contains(out, "±/random_string.demo2/create") {
		t.Fatalf("ready set should be split, not raw: %q", out)
	}
	if strings.Contains(out, "execution:") || strings.Contains(out, "turf://") {
		t.Fatalf("execution URI should be dropped: %q", out)
	}
}

func TestWorkspaceOpen_Summary(t *testing.T) {
	const content = `{"workspace_alias": "default", "backend_type": "inmem", "name": "default",
		"resolved_providers": {"random": {"source": "hashicorp/random", "version": "3.9.0"}}}`
	out := renderFor("turf_workspace_open", content, hiddenState{})
	for _, want := range []string{"default", "inmem", "random"} {
		if !strings.Contains(out, want) {
			t.Fatalf("workspace open missing %q: %q", want, out)
		}
	}
}

func TestSkill_LoadedLineNotBody(t *testing.T) {
	const body = "# Core Infrastructure Skill\n\nWorkspace lifecycle, provider load...\nmore guidance\n"
	out := renderFor("turf_skill_core", body, hiddenState{})
	if !strings.Contains(out, "Core Skill") || !strings.Contains(out, "loaded") {
		t.Fatalf("skill summary wrong: %q", out)
	}
	// The line count rides inline on the compact summary (not just the unfold).
	if !strings.Contains(out, "(4 lines)") {
		t.Fatalf("compact skill should show inline line count: %q", out)
	}
	if strings.Contains(out, "Workspace lifecycle") {
		t.Fatalf("compact skill should not dump the guide body: %q", out)
	}

	// Expanded stays a single line: same title + load status + size, and never the
	// guide title or body — the skill renderer emits no Ctrl+O detail block.
	det := renderFor("turf_skill_core", body, service.StaticSessionState{})
	if !strings.Contains(det, "Core Skill") || !strings.Contains(det, "loaded") || !strings.Contains(det, "(4 lines)") {
		t.Fatalf("expanded skill summary wrong: %q", det)
	}
	if strings.Contains(det, "Core Infrastructure Skill") {
		t.Fatalf("expanded skill should not add the guide title: %q", det)
	}
	if strings.Contains(det, "more guidance") {
		t.Fatalf("expanded skill should not dump the guide body: %q", det)
	}
	if strings.Contains(strings.TrimRight(det, "\n"), "\n") {
		t.Fatalf("expanded skill should be a single line: %q", det)
	}
}

func TestProviderConfigure_Configured(t *testing.T) {
	const content = `{"provider": "aws", "status": "configured"}`
	out := renderFor("turf_provider_configure", content, hiddenState{})
	for _, want := range []string{"configure", "aws", "configured"} {
		if !strings.Contains(out, want) {
			t.Fatalf("provider_configure missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "unknown") {
		t.Fatalf("clean configure should not mention unknowns: %q", out)
	}
}

func TestProviderConfigure_WithUnknowns(t *testing.T) {
	const content = `{"provider": "aws", "alias": "west", "status": "configured_with_unknowns",
		"unknown_keys": ["region", "assume_role.role_arn"]}`
	out := renderFor("turf_provider_configure", content, service.StaticSessionState{})
	for _, want := range []string{"configure", "aws", "configured with unknowns", "alias", "west", "2 unknown key(s)", "region", "assume_role.role_arn"} {
		if !strings.Contains(out, want) {
			t.Fatalf("provider_configure (unknowns) missing %q: %q", want, out)
		}
	}
}

func TestProviderConfigure_UnknownKeysCompact(t *testing.T) {
	// Compact view (results hidden) shows the unknown count but not each key.
	const content = `{"provider": "azurerm", "status": "configured_with_unknowns",
		"unknown_keys": ["client_secret"]}`
	compact := renderFor("turf_provider_configure", content, hiddenState{})
	if !strings.Contains(compact, "1 unknown key(s)") {
		t.Fatalf("compact should show unknown count: %q", compact)
	}
	if strings.Contains(strings.TrimRight(compact, " "), "\n") {
		t.Fatalf("compact should be a single line: %q", compact)
	}
	if strings.Contains(compact, "client_secret") {
		t.Fatalf("compact should not list individual unknown keys: %q", compact)
	}
}

func TestRenderers_BadJSONFallsBack(t *testing.T) {
	for name := range turfToolRenderers() {
		out := renderFor(name, "not json", service.StaticSessionState{})
		if strings.TrimSpace(out) == "" {
			t.Fatalf("%s produced empty output on bad JSON", name)
		}
	}
}

// TestEveryRendererLeadsWithTitle locks in the "English-readable tool name is
// consistently displayed" invariant: every renderer's output leads with the
// friendly turfToolInfo title, resolved the same way the /tools dialog resolves it.
func TestEveryRendererLeadsWithTitle(t *testing.T) {
	for name := range turfToolRenderers() {
		want := turfToolTitle(strings.TrimPrefix(name, appName+"_"))
		out := renderFor(name, "{}", service.StaticSessionState{})
		if !strings.Contains(out, want) {
			t.Errorf("%s should lead with title %q: %q", name, want, out)
		}
	}
}

// TestEveryTurfToolHasRenderer guards coverage: every turf tool declared in
// turfToolInfo (the /tools-dialog source of truth) must have a custom renderer, so
// no tool silently falls back to a raw JSON dump. A new server tool added to
// turfToolInfo without a renderer fails here.
func TestEveryTurfToolHasRenderer(t *testing.T) {
	renderers := turfToolRenderers()
	for bare := range turfToolInfo {
		name := appName + "_" + bare
		if _, ok := renderers[name]; !ok {
			t.Errorf("turf tool %q has a title but no renderer", name)
		}
	}
}

// TestErrorLine_CompactTruncatedExpandedMultiline locks in the fix and its detail
// toggle: a failed tool call surfaces the framework's error text (msg.Content) instead
// of a bare title, wrapping across lines when expanded and truncating to one line
// (with an ellipsis) when compact.
func TestErrorLine_CompactTruncatedExpandedMultiline(t *testing.T) {
	const tail = "ZZZ_TAIL_TOKEN"
	errText := "Cannot invoke action: configuration has unresolved references. " +
		strings.Repeat("filler word ", 15) + tail
	title := turfToolTitle("action_invoke")

	// Expanded (turf default): leads with the title, wraps across lines, full text.
	shown := renderErr("turf_action_invoke", errText, service.StaticSessionState{})
	if !strings.Contains(shown, title) {
		t.Fatalf("errored line should lead with title %q: %q", title, shown)
	}
	if !strings.Contains(strings.TrimRight(shown, " \n"), "\n") {
		t.Fatalf("expanded error should wrap across multiple lines: %q", shown)
	}
	if !strings.Contains(plainNorm(shown), tail) {
		t.Fatalf("expanded error should show the full text incl. tail: %q", shown)
	}

	// Compact (Ctrl+O): single line, truncated before the tail, ellipsis shown.
	compact := renderErr("turf_action_invoke", errText, hiddenState{})
	if !strings.Contains(compact, title) {
		t.Fatalf("compact errored line should lead with title %q: %q", title, compact)
	}
	if strings.Contains(strings.TrimRight(compact, " \n"), "\n") {
		t.Fatalf("compact error should be a single line: %q", compact)
	}
	if strings.Contains(compact, tail) {
		t.Fatalf("compact error should be truncated before the tail: %q", compact)
	}
	if !strings.Contains(compact, "…") {
		t.Fatalf("compact truncation should show an ellipsis: %q", compact)
	}
}

// TestErrorLine_EmptyContentFallsBackToFailed preserves prior behavior when the
// framework reports a failure with no text: the line still reads "failed".
func TestErrorLine_EmptyContentFallsBackToFailed(t *testing.T) {
	out := renderErr("turf_skill_core", "", service.StaticSessionState{})
	if !strings.Contains(out, "failed") {
		t.Fatalf("empty-content error should render \"failed\": %q", out)
	}
}

// TestErrorLine_ShowsRequestTarget locks in the request-context fix: a failed call leads
// with the target it acted on (resolved from the tool-call args), in both the expanded
// and the compact one-line form, so the error is as scannable as a success line.
func TestErrorLine_ShowsRequestTarget(t *testing.T) {
	const addr = "kubernetes_deployment_v1.web"
	errText := "Failed to evaluate config: metadata: namespace: unresolved references"
	args := `{"resource_addr":"` + addr + `","workspace_alias":"k8s"}`

	shown := renderErrArgs("turf_declare_resource", errText, args, service.StaticSessionState{})
	if !strings.Contains(plainNorm(shown), addr) {
		t.Fatalf("expanded error should lead with the request target %q: %q", addr, shown)
	}

	compact := renderErrArgs("turf_declare_resource", errText, args, hiddenState{})
	if !strings.Contains(plainNorm(compact), addr) {
		t.Fatalf("compact error should keep the request target %q visible: %q", addr, compact)
	}
	if strings.Contains(strings.TrimRight(compact, " \n"), "\n") {
		t.Fatalf("compact error should stay a single line: %q", compact)
	}
}

// TestErrorTarget_FallsBackToWorkspaceAlias covers the universal fallback: a tool with no
// specific target arg (plan_cancel) still surfaces the workspace alias on failure — the
// screenshot's "Cancel Draft" case.
func TestErrorTarget_FallsBackToWorkspaceAlias(t *testing.T) {
	// The error text deliberately omits the alias, so a passing test proves the target
	// prefix (not the message) carries it.
	out := renderErrArgs("turf_plan_cancel", "no draft phase to cancel",
		`{"workspace_alias":"prod-db"}`, service.StaticSessionState{})
	if !strings.Contains(plainNorm(out), "prod-db") {
		t.Fatalf("plan_cancel error should surface the workspace alias: %q", out)
	}
}

// TestEveryRendererReportsErrors guards that the fallback swap reached every renderer
// (including the two skill renderers that don't parse JSON): a failed call always
// surfaces the framework's error text, never a silent bare title.
func TestEveryRendererReportsErrors(t *testing.T) {
	const errText = "boom: something went wrong"
	for name := range turfToolRenderers() {
		out := renderErr(name, errText, service.StaticSessionState{})
		if !strings.Contains(out, errText) {
			t.Errorf("%s should surface the framework error text: %q", name, out)
		}
	}
}

// TestThink_SuppressesThoughtBody locks in the think override: cagent's built-in
// think view dumps the entire running thought log ("Thoughts:\n…"); turf's renderer
// collapses every call to a quiet "Think" line and never echoes the log, in both the
// compact and expanded (Ctrl+O) detail levels. A framework error is still surfaced.
func TestThink_SuppressesThoughtBody(t *testing.T) {
	const content = "Thoughts:\nfirst secret thought\nsecond verbose thought"
	renderThinkFor := func(ss service.SessionStateReader) string {
		msg := &types.Message{
			Content:        content,
			ToolStatus:     types.ToolStatusCompleted,
			ToolCall:       tools.ToolCall{Function: tools.FunctionCall{Name: "think"}},
			ToolDefinition: tools.Tool{Name: "think"},
		}
		b := builtinToolRenderers()["think"](animation.NewRuntime(), msg, ss)
		b.SetSize(120, 10)
		return b.View()
	}

	for _, ss := range []service.SessionStateReader{service.StaticSessionState{}, hiddenState{}} {
		out := renderThinkFor(ss)
		if !strings.Contains(out, "Think") {
			t.Fatalf("think line should show the Think label: %q", out)
		}
		for _, leaked := range []string{"Thoughts", "secret thought", "verbose thought"} {
			if strings.Contains(out, leaked) {
				t.Fatalf("think renderer leaked the thought body %q: %q", leaked, out)
			}
		}
		if strings.Contains(strings.TrimRight(out, " \n"), "\n") {
			t.Fatalf("suppressed think line should be a single line: %q", out)
		}
	}

	// A framework error is terse and diagnostic, so it is still surfaced.
	errMsg := &types.Message{
		Content:        "boom: think failed",
		ToolStatus:     types.ToolStatusError,
		ToolCall:       tools.ToolCall{Function: tools.FunctionCall{Name: "think"}},
		ToolDefinition: tools.Tool{Name: "think"},
	}
	b := builtinToolRenderers()["think"](animation.NewRuntime(), errMsg, service.StaticSessionState{})
	b.SetSize(120, 10)
	if out := b.View(); !strings.Contains(out, "boom: think failed") {
		t.Fatalf("think renderer should surface a framework error: %q", out)
	}
}

func TestNewRenderers_Smoke(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wants   []string
	}{
		{"turf_workspace_delete", `{"name":"staging","resource_count":0,"deleted":true}`,
			[]string{"Delete Workspace", "staging", "deleted"}},
		{"turf_plan_cancel", `{"phase_id":"ph_003","status":"cancelled","message":"Draft discarded."}`,
			[]string{"Cancel Draft", "ph_003", "cancelled", "Draft discarded."}},
		{"turf_config_init", `{"path":"infra/prod","backend":{"type":"s3"},"workspace":{"name":"main"},
			"required_providers":{"aws":{"source":"hashicorp/aws","version":"5.1.0"}},
			"variables":[{"name":"region","required":true}],"outputs":[{"name":"url"}]}`,
			[]string{"Init Config", "main", "infra/prod", "1 provider(s)", "1 variable(s)", "1 output(s)", "region", "url"}},
		{"turf_plan_new", `{"phase_id":"ph_001","config_dir":"infra/prod","path":"infra/prod","resources":[]}`,
			[]string{"ph_001", "opened", "infra/prod"}},
		{"turf_module_init", `{"source":"Azure/x/azurerm","version":"0.4.0",
			"required_providers":{"azurerm":{"source":"hashicorp/azurerm"}}}`,
			[]string{"Init Module", "Azure/x/azurerm", "v0.4.0", "1 provider(s)"}},
		{"turf_declare_action", `{"action_addr":"action.aws_lambda_invoke.warm",
			"action_type":"aws_lambda_invoke","name":"warm","declared":true}`,
			[]string{"Declare Action", "action.aws_lambda_invoke.warm", "declared"}},
		{"turf_declare_action", `{"action_addr":"action.aws_lambda_invoke.warm",
			"action_type":"aws_lambda_invoke","name":"warm","removed":true}`,
			[]string{"Declare Action", "action.aws_lambda_invoke.warm", "removed"}},
		{"turf_action_invoke", `{"action_type":"aws_lambda_invoke","provider":"aws",
			"status":"completed","progress":["invoked"]}`,
			[]string{"Invoke Action", "aws_lambda_invoke", "completed", "invoked"}},
		{"turf_effect_cancel", `{"effect_id":"ph/x/a.b/create","state":"cancelled",
			"cascaded":["~/c.d/update"],"ready":["+/e.f/create"]}`,
			[]string{"Cancel Effect", "cancelled", "1 cascaded", "c.d", "e.f"}},
		{"turf_resource_import", `{"resource_addr":"aws_s3_bucket.data","import_id":"my-bucket",
			"imported_state":{"id":"my-bucket","arn":"arn:x"}}`,
			[]string{"Import Resource", "aws_s3_bucket.data", "imported", "my-bucket", "2 attr(s)", "arn"}},
		{"turf_resource_refresh", `{"resource_addr":"aws_s3_bucket.data","exists":true,
			"refreshed_state":{"id":"my-bucket"}}`,
			[]string{"Refresh Resource", "aws_s3_bucket.data", "refreshed", "1 attr(s)"}},
		{"turf_resource_refresh", `{"resource_addr":"aws_s3_bucket.gone","exists":false}`,
			[]string{"Refresh Resource", "aws_s3_bucket.gone", "no longer exists"}},
	}
	for _, c := range cases {
		out := renderFor(c.name, c.content, service.StaticSessionState{})
		for _, w := range c.wants {
			if !strings.Contains(out, w) {
				t.Errorf("%s missing %q: %q", c.name, w, out)
			}
		}
	}
}

// TestRenderValueNesting exercises the shared value renderer directly: nested maps and
// lists expand across =-aligned lines, short scalar lists stay inline, sentinels are
// masked, and no raw JSON leaks — the behavior every kv-based block now inherits.
func TestRenderValueNesting(t *testing.T) {
	m := map[string]any{
		"bucket": "mint-hyena",
		"tags":   map[string]any{"env": "prod", "team": "infra"},
		"ports":  []any{float64(80), float64(443)},
		"arn":    "__cty_unknown__",
		"pw":     "__cty_sensitive__",
		"empty":  map[string]any{},
		"rules":  []any{map[string]any{"id": "r1"}},
	}
	out := plainNorm(strings.Join(kvLines(m, "", 0), "\n"))
	for _, want := range []string{
		`bucket = "mint-hyena"`,
		"tags = {",
		`env = "prod"`,
		`team = "infra"`,
		"ports = [80, 443]", // short scalar list stays inline
		"empty = {}",
		"rules = [", // list holding a map expands
		`id = "r1"`,
		"(known after apply)",
		"(sensitive)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("nesting missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `{"`) || strings.Contains(out, "[{") {
		t.Fatalf("nesting leaked raw JSON:\n%s", out)
	}
	if strings.Contains(out, "__cty_") {
		t.Fatalf("nesting leaked sentinel:\n%s", out)
	}
}

// TestActionInvoke_ResolvedConfigExpandsNested is the motivating case: a dry-run resolved
// config with nested values renders as a multi-line block, not raw JSON.
func TestActionInvoke_ResolvedConfigExpandsNested(t *testing.T) {
	const content = `{"action_type":"tfcoremock_simple_resource","provider":"tfcoremock",
		"status":"dry_run",
		"config":{"bucket":"mint-hyena","tags":{"env":"prod","team":"infra"},
		"ports":[80,443],"arn":"__cty_unknown__","kms":"__cty_sensitive__"}}`
	out := plainNorm(renderFor("turf_action_invoke", content, service.StaticSessionState{}))
	for _, want := range []string{"resolved config", "tags = {", `env = "prod"`, "ports = [80, 443]", "(known after apply)", "(sensitive)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("resolved config missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, `{"`) {
		t.Fatalf("resolved config leaked raw JSON: %q", out)
	}
	if strings.Contains(out, "__cty_") {
		t.Fatalf("resolved config leaked sentinel: %q", out)
	}
}

// TestResourcePlan_CollectionAttrExpands proves the fix reaches the diff path too: a
// collection-valued attribute expands instead of dumping JSON.
func TestResourcePlan_CollectionAttrExpands(t *testing.T) {
	const content = `{"resource_addr":"aws_s3_bucket.b","action":"+","before":null,
		"after":{"bucket":"b","tags":{"env":"prod"}}}`
	out := plainNorm(renderFor("turf_declare_resource", content, service.StaticSessionState{}))
	for _, want := range []string{"tags = {", `env = "prod"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("collection attr not expanded, missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, `{"`) {
		t.Fatalf("collection attr leaked raw JSON: %q", out)
	}
}

// TestAttrDiff_MarkersPropagate confirms a create/destroy marks every line (+/-) — not
// just the top-level attr — and that top-level keys are =-aligned, matching tofu plan.
func TestAttrDiff_MarkersPropagate(t *testing.T) {
	after := map[string]any{"id": "x", "tags": map[string]any{"env": "prod"}}
	create := plainNorm(strings.Join(attrDiff(nil, after, ""), "\n"))
	for _, want := range []string{"+ id", "+ tags = {", `+ env = "prod"`} { // nested line carries +
		if !strings.Contains(create, want) {
			t.Fatalf("create diff missing marker %q:\n%s", want, create)
		}
	}

	before := map[string]any{"tags": map[string]any{"env": "prod"}}
	destroy := plainNorm(strings.Join(attrDiff(before, nil, ""), "\n"))
	for _, want := range []string{"- tags = {", `- env = "prod"`} {
		if !strings.Contains(destroy, want) {
			t.Fatalf("destroy diff missing marker %q:\n%s", want, destroy)
		}
	}

	// A scalar change stays inline as old → new (no expansion).
	chg := plainNorm(strings.Join(attrDiff(
		map[string]any{"n": float64(1)}, map[string]any{"n": float64(2)}, ""), "\n"))
	if !strings.Contains(chg, "~ n = 1 → 2") {
		t.Fatalf("scalar change should be inline old → new: %q", chg)
	}
}
