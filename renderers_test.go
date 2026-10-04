package main

import (
	"encoding/json"
	"regexp"
	"sort"
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
	return renderWithArgs(name, content, "", ss, 1)
}

func renderWithArgs(name, content, argsJSON string, ss service.SessionStateReader, height int) string {
	msg := &types.Message{
		Content:        content,
		ToolStatus:     types.ToolStatusCompleted,
		ToolCall:       tools.ToolCall{Function: tools.FunctionCall{Name: name, Arguments: argsJSON}},
		ToolDefinition: tools.Tool{Name: name},
	}
	b := turfToolRenderers()[name](animation.NewRuntime(), msg, ss)
	b.SetSize(120, height)
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

// TestPlanSummary_CompactVsDetailed: the compact line is the tally alone; the
// expansion unfolds each row's before→after diff with its values.
func TestPlanSummary_CompactVsDetailed(t *testing.T) {
	const content = `{"phase_id": "ph_001", "resources": [{
		"address": "random_pet.this",
		"provider": "random",
		"action": "+",
		"before": null,
		"after": {"id": "x", "length": 2, "separator": "-"}
	}]}`

	compact := renderFor("turf_replan", content, hiddenState{})
	if !strings.Contains(compact, "+1") {
		t.Fatalf("compact missing the tally: %q", compact)
	}
	if strings.Contains(compact, "random_pet.this") || strings.Contains(compact, "separator") {
		t.Fatalf("compact should not include the detail block: %q", compact)
	}
	if strings.Contains(strings.TrimRight(compact, " "), "\n") {
		t.Fatalf("compact should be a single line: %q", compact)
	}

	detailed := renderFor("turf_replan", content, service.StaticSessionState{})
	// The expanded view unfolds the before→after diff with values (the quoted
	// values appear only in the diff, not the compact summary).
	for _, want := range []string{"random_pet.this", "separator", `"-"`, `"x"`} {
		if !strings.Contains(detailed, want) {
			t.Fatalf("expanded diff missing %q: %q", want, detailed)
		}
	}
}

func TestPlanSummary_UpdateDiff(t *testing.T) {
	// A ~ change should render old → new with both values.
	const content = `{"phase_id": "ph_001", "resources": [{
		"address": "random_pet.first",
		"provider": "random",
		"action": "~",
		"before": {"prefix": "alpha", "length": 2},
		"after": {"prefix": "gamma", "length": 2}
	}]}`
	out := renderFor("turf_replan", content, service.StaticSessionState{})
	for _, want := range []string{"prefix", `"alpha"`, "→", `"gamma"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("update diff missing %q: %q", want, out)
		}
	}
	// length is unchanged and must be omitted from the diff.
	if strings.Contains(out, "length") {
		t.Fatalf("unchanged attr should not appear: %q", out)
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

// TestPlanSummary_ReplaceCBDvsDTC: a replace row's header carries ± for
// create-before-destroy and ∓ for the default destroy-then-create.
func TestPlanSummary_ReplaceCBDvsDTC(t *testing.T) {
	row := func(cbd string) string {
		return `{"phase_id": "ph_001", "resources": [{"address": "a.b", "action": "replace",` +
			cbd + ` "before": {"x": 1}, "after": {"x": 2}}]}`
	}
	out := plainNorm(renderFor("turf_replan", row(`"create_before_destroy": true,`), service.StaticSessionState{}))
	if !strings.Contains(out, "± a.b") || strings.Contains(out, "∓") {
		t.Fatalf("CBD replace should show ± not ∓: %q", out)
	}
	out = plainNorm(renderFor("turf_replan", row(`"create_before_destroy": false,`), service.StaticSessionState{}))
	if !strings.Contains(out, "∓ a.b") || strings.Contains(out, "±") {
		t.Fatalf("DTC replace should show ∓ not ±: %q", out)
	}

	// The load-bearing case: the server sends create_before_destroy as a bare bool
	// with omitempty, so the ordinary delete-then-create replace arrives with the
	// key ABSENT. Reading absent as ± would render every default replace wrong.
	out = plainNorm(renderFor("turf_replan", row(""), service.StaticSessionState{}))
	if !strings.Contains(out, "∓ a.b") || strings.Contains(out, "±") {
		t.Fatalf("replace with no create_before_destroy is DTC (∓), got: %q", out)
	}
}

// TestPlanSummary_MovedRecords covers the state relocations a phase's `moved {}`
// blocks produce. They are not resource changes, so they get their own summary
// segment and their own detail section rather than a tally bucket — and they are
// reported by plan_new/replan even when the re-plan finds nothing left to move.
func TestPlanSummary_MovedRecords(t *testing.T) {
	const content = `{"phase_id": "ph_001", "path": "infra/prod",
		"resources": [{"address": "aws_s3_bucket.assets", "action": "noop"}],
		"moved": [
			{"from": "aws_s3_bucket.old", "to": "aws_s3_bucket.assets"},
			{"from": "module.net.aws_vpc.a", "to": "module.network.aws_vpc.a"}
		]}`
	out := plainNorm(renderFor("turf_plan_new", content, service.StaticSessionState{}))
	if !strings.Contains(out, "⇄2 moved") {
		t.Fatalf("summary should count relocations: %q", out)
	}
	for _, want := range []string{"moved:", "aws_s3_bucket.old", "→", "module.network.aws_vpc.a"} {
		if !strings.Contains(out, want) {
			t.Errorf("expanded block missing %q: %q", want, out)
		}
	}
	// Compact stays a one-liner: the count is in the summary, the pairs are not.
	compact := plainNorm(renderFor("turf_plan_new", content, hiddenState{}))
	if strings.Contains(compact, "aws_s3_bucket.old") {
		t.Errorf("compact line should not list relocations: %q", compact)
	}
}

// TestPlanSummary_DeposedRow covers the destroy of an object deposed under an
// address by an interrupted create-before-destroy replace. Two rows can share one
// address, told apart only by deposed_key, so the header has to carry it.
func TestPlanSummary_DeposedRow(t *testing.T) {
	const content = `{"phase_id": "ph_001", "path": "infra/prod", "resources": [
		{"address": "aws_instance.web", "action": "create", "after": {"ami": "ami-1"}},
		{"address": "aws_instance.web", "action": "delete", "deposed_key": "abc12345",
		 "before": {"ami": "ami-0"}}
	]}`
	out := plainNorm(renderFor("turf_plan_new", content, service.StaticSessionState{}))
	if !strings.Contains(out, "aws_instance.web (deposed abc12345)") {
		t.Fatalf("deposed row should name its deposed key: %q", out)
	}
	// It is an ordinary destroy for tally purposes.
	if !strings.Contains(out, "-1") || !strings.Contains(out, "+1") {
		t.Errorf("deposed destroy should count in the - bucket: %q", out)
	}
}

// TestPlanSummary_AdoptedRow covers a change that adopts an existing object named
// by an `import {}` block. Its before half is the real remote object, so it plans
// as a no-op — a bucket planTally omits — and without its own segment the whole
// plan would headline as "no changes".
func TestPlanSummary_AdoptedRow(t *testing.T) {
	const content = `{"phase_id": "ph_001", "path": "infra/prod", "resources": [
		{"address": "tfcoremock_simple_resource.adopted", "action": "noop",
		 "importing": {"id": "res-42"}},
		{"address": "aws_s3_bucket.byid", "action": "noop",
		 "importing": {"identity": {"bucket": "mint-hyena"}}}
	]}`
	out := plainNorm(renderFor("turf_plan_new", content, service.StaticSessionState{}))
	if !strings.Contains(out, "↧2 adopted") {
		t.Fatalf("adoption must survive the no-op tally: %q", out)
	}
	for _, want := range []string{"adopt id=res-42", "adopt identity"} {
		if !strings.Contains(out, want) {
			t.Errorf("row header missing %q: %q", want, out)
		}
	}
}

// TestEffectApply_DeposedOutputsMessage covers the three result fields the apply
// line used to drop. The message matters most for a move effect: the from→to
// relocation reaches the wire only there.
func TestEffectApply_DeposedOutputsMessage(t *testing.T) {
	const content = `{"kind": "move", "state": "applied", "resource_addr": "random_pet.renamed",
		"ready": [], "message": "moved random_pet.old to random_pet.renamed in state"}`
	out := plainNorm(renderFor("turf_effect_apply", content, service.StaticSessionState{}))
	for _, want := range []string{"random_pet.renamed", "move", "applied", "moved random_pet.old to"} {
		if !strings.Contains(out, want) {
			t.Errorf("move effect missing %q: %q", want, out)
		}
	}

	const applied = `{"kind": "create", "state": "applied", "resource_addr": "aws_instance.web",
		"new_state": {"id": "i-1"},
		"deposed_state": {"id": "i-0"},
		"outputs": {"url": {"value": "https://x"}, "pw": {"value": "s3cret", "sensitive": true}}}`
	out = plainNorm(renderFor("turf_effect_apply", applied, service.StaticSessionState{}))
	for _, want := range []string{"2 output(s)", "deposed state:", `id = "i-0"`, "outputs:", `url = "https://x"`} {
		if !strings.Contains(out, want) {
			t.Errorf("apply detail missing %q: %q", want, out)
		}
	}
	// The per-output sensitive flag IS the mask here — a revealed secret must not
	// reach the timeline just because it arrived in the clear.
	if strings.Contains(out, "s3cret") {
		t.Errorf("sensitive output leaked into the timeline: %q", out)
	}
	if !strings.Contains(out, "(sensitive)") {
		t.Errorf("sensitive output should render masked: %q", out)
	}
}

// TestResourceImport_IdentityAndWarning covers an import located by a
// provider-declared identity, and the warning that says the import was recorded
// but never flushed to the state backend — which must not read as a clean success.
func TestResourceImport_IdentityAndWarning(t *testing.T) {
	const content = `{"resource_addr": "aws_s3_bucket.data",
		"imported_state": {"id": "my-bucket"},
		"identity": {"bucket": "my-bucket", "region": "us-east-1"},
		"warning": "state could not be flushed to the backend"}`
	out := plainNorm(renderFor("turf_resource_import", content, service.StaticSessionState{}))
	for _, want := range []string{"imported", "identity", "not flushed", "could not be flushed", `bucket = "my-bucket"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q: %q", want, out)
		}
	}
	// No import_id was given or returned, so nothing should claim one.
	if strings.Contains(out, "id my-bucket") {
		t.Errorf("identity import should not report an id locator: %q", out)
	}
}

// TestFormatEffectID_MoveAndDeposed pins the two effect-ID grammars the moved and
// deposed work introduced: a "→" action label, and an address segment that now
// carries a space and parentheses.
func TestFormatEffectID_MoveAndDeposed(t *testing.T) {
	out := plainNorm(formatEffectID("→/random_pet.renamed/move"))
	for _, want := range []string{"→", "random_pet.renamed", "move"} {
		if !strings.Contains(out, want) {
			t.Errorf("move effect id missing %q: %q", want, out)
		}
	}
	out = plainNorm(formatEffectID("-/aws_instance.web (deposed abc12345)/destroy"))
	for _, want := range []string{"-", "aws_instance.web (deposed abc12345)", "destroy"} {
		if !strings.Contains(out, want) {
			t.Errorf("deposed effect id missing %q: %q", want, out)
		}
	}
}

func TestPlanSummary_TallySplitsReplace(t *testing.T) {
	const content = `{"phase_id": "ph_001", "resources": [
		{"address": "a.x", "action": "replace", "create_before_destroy": true},
		{"address": "a.y", "action": "replace", "create_before_destroy": false},
		{"address": "a.z", "action": "create"}
	]}`
	out := renderFor("turf_replan", content, hiddenState{})
	for _, want := range []string{"+1", "±1", "∓1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("tally should split CBD/DTC, missing %q: %q", want, out)
		}
	}
}

func TestPlanSummary_Tally(t *testing.T) {
	const content = `{
		"phase_id": "ph_001",
		"resources": [
			{"address": "a.x", "action": "+"},
			{"address": "a.y", "action": "+"},
			{"address": "a.z", "action": "~"}
		],
		"outputs": {"url": "v"}
	}`
	out := renderFor("turf_replan", content, hiddenState{})
	if !strings.Contains(out, "+2") || !strings.Contains(out, "~1") {
		t.Fatalf("tally wrong: %q", out)
	}
	if !strings.Contains(out, "1 output") {
		t.Fatalf("missing outputs count: %q", out)
	}
}

// TestPlanSummary_DataSourceReads covers data_source_reads[] on the shared walk
// summary — plan_new and replan both return it. A walk reads every declared data
// source, so the one-line view reports only what did *not* resolve; the expansion
// lists them all.
func TestPlanSummary_DataSourceReads(t *testing.T) {
	const content = `{"phase_id": "ph_001", "path": "infra/prod",
		"resources": [{"address": "aws_instance.web", "action": "create"}],
		"data_source_reads": [
			{"address": "data.aws_ami.latest", "action": "read"},
			{"address": "data.aws_instances.web", "action": "deferred",
				"reason": "dependency_pending", "depends_on": ["aws_instance.web"]},
			{"address": "data.aws_ami.bad", "action": "error", "error": "no AMI matched"}]}`

	for _, name := range []string{"turf_plan_new", "turf_replan"} {
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

// A workspace is opened against a CONFIGURATION, and that binding — not a provider
// list — is what the line reports. The server renamed `name` to `workspace_name` and
// stopped echoing resolved_providers (the walk configures providers now), so this
// pins the field turf actually has to read: reverting the rename leaves the line
// with an empty name, silently, which is how the drift went unnoticed.
func TestWorkspaceOpen_Summary(t *testing.T) {
	// workspace_alias is EMPTY here on purpose: this is the default workspace with a
	// named state slot. The server normalizes an input alias of "default" to "", so
	// the two never disagree that way on the wire (session.NormalizeAlias).
	const content = `{"workspace_alias": "", "backend_type": "inmem",
		"workspace_name": "prod", "config_alias": "app", "config_path": "infra/prod",
		"state_path": "infra/prod/terraform.tfstate",
		"backend_config": {"bucket": "tf-state"},
		"resources": ["random_pet.this"],
		"warnings": ["state records provider hashicorp/null, which the configuration no longer declares"]}`

	compact := renderFor("turf_workspace_open", content, hiddenState{})
	for _, want := range []string{"prod", "inmem", "infra/prod"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("workspace open missing %q: %q", want, compact)
		}
	}

	detailed := plainNorm(renderFor("turf_workspace_open", content, service.StaticSessionState{}))
	for _, want := range []string{
		"state:", "terraform.tfstate",
		"backend config:", "bucket", "tf-state",
		"1 resource(s) in state", "random_pet.this",
		"⚠", "no longer declares",
	} {
		if !strings.Contains(detailed, want) {
			t.Fatalf("workspace open detail missing %q: %q", want, detailed)
		}
	}
}

// Naming a workspace on the timeline is one rule everywhere (workspaceName): the
// session alias wins, the OpenTofu state slot is the fallback. The alias is set
// exactly when several workspaces are open — and there the slot is usually the
// literal "default" for all of them, so preferring it would name them identically.
func TestWorkspaceOpen_PrefersAliasOverStateSlot(t *testing.T) {
	out := renderFor("turf_workspace_open",
		`{"workspace_alias": "prod", "workspace_name": "default",
		  "backend_type": "s3", "config_path": "infra/app"}`,
		hiddenState{})
	if !strings.Contains(out, "prod") {
		t.Fatalf("the line should lead with the alias the user passes to every later call: %q", out)
	}

	// And workspace_show must agree for the very same workspace.
	show := renderFor("turf_workspace_show",
		`{"workspaces":[{"workspace_alias":"prod","workspace_name":"default",
		  "backend_type":"s3","resource_count":1}]}`,
		hiddenState{})
	if !strings.Contains(show, "prod") {
		t.Fatalf("workspace_show disagrees with workspace_open: %q", show)
	}
}

// The same rename hit workspace_show and workspace_delete, and the session-title
// curator reads workspace_open's name too — so pin all three together.
func TestWorkspaceNameFieldSurvives(t *testing.T) {
	// workspaceName prefers the session alias; the renamed field is what the line
	// falls back to when a workspace was opened without one.
	show := renderFor("turf_workspace_show",
		`{"workspaces":[{"backend_type":"s3","workspace_name":"prod",
			"resource_count":3,"uncommitted_changes":0}]}`,
		hiddenState{})
	if !strings.Contains(show, "prod") {
		t.Fatalf("workspace show lost the workspace name: %q", show)
	}

	del := renderFor("turf_workspace_delete",
		`{"workspace_name":"staging","resource_count":0,"deleted":true}`, hiddenState{})
	if !strings.Contains(del, "staging") {
		t.Fatalf("workspace delete lost the workspace name: %q", del)
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
	errText := "Failed to read resource: the server could not find the requested resource"
	args := `{"resource_addr":"` + addr + `","workspace_alias":"k8s"}`

	shown := renderErrArgs("turf_resource_refresh", errText, args, service.StaticSessionState{})
	if !strings.Contains(plainNorm(shown), addr) {
		t.Fatalf("expanded error should lead with the request target %q: %q", addr, shown)
	}

	compact := renderErrArgs("turf_resource_refresh", errText, args, hiddenState{})
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
		{"turf_workspace_delete", `{"workspace_name":"staging","resource_count":0,"deleted":true}`,
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
		{"turf_action_trigger", `{"name":"reboot_first","target":"aws_instance.web",
			"events":["before_update"],"actions":["action.aws_ec2_reboot.web"],"on_failure":"halt",
			"hcl":"action_trigger \"reboot_first\" {\n  target = aws_instance.web\n}"}`,
			[]string{"Attach Phase Action", "reboot_first", "attached", "aws_instance.web", "before_update",
				"action.aws_ec2_reboot.web", `action_trigger "reboot_first" {`, "target = aws_instance.web"}},
		{"turf_action_untrigger", `{"name":"reboot_first"}`,
			[]string{"Detach Phase Action", "reboot_first", "detached"}},
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

// TestPlanSummary_CollectionAttrExpands proves the fix reaches the diff path too: a
// collection-valued attribute expands instead of dumping JSON.
func TestPlanSummary_CollectionAttrExpands(t *testing.T) {
	const content = `{"phase_id":"ph_001","resources":[{"address":"aws_s3_bucket.b","action":"+",
		"before":null,"after":{"bucket":"b","tags":{"env":"prod"}}}]}`
	out := plainNorm(renderFor("turf_replan", content, service.StaticSessionState{}))
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
	create := plainNorm(strings.Join(attrDiff(nil, after, nil, nil, ""), "\n"))
	for _, want := range []string{"+ id", "+ tags = {", `+ env = "prod"`} { // nested line carries +
		if !strings.Contains(create, want) {
			t.Fatalf("create diff missing marker %q:\n%s", want, create)
		}
	}

	before := map[string]any{"tags": map[string]any{"env": "prod"}}
	destroy := plainNorm(strings.Join(attrDiff(before, nil, nil, nil, ""), "\n"))
	for _, want := range []string{"- tags = {", `- env = "prod"`} {
		if !strings.Contains(destroy, want) {
			t.Fatalf("destroy diff missing marker %q:\n%s", want, destroy)
		}
	}

	// A scalar change stays inline as old → new (no expansion).
	chg := plainNorm(strings.Join(attrDiff(
		map[string]any{"n": float64(1)}, map[string]any{"n": float64(2)}, nil, nil, ""), "\n"))
	if !strings.Contains(chg, "~ n = 1 → 2") {
		t.Fatalf("scalar change should be inline old → new: %q", chg)
	}
}

// --- sensitivity masks -------------------------------------------------------
//
// The turf server returns OpenTofu's before_sensitive/after_sensitive/sensitive_values
// masks alongside the values, and can be asked (show_sensitive) to put the real secret
// on the wire. These tests pin the property that matters: whatever arrives, no turf
// renderer paints a secret into the timeline.

// secretAbsent mirrors the server's own e2e helper — a redacted attribute is worthless
// if the same value rides along somewhere else in the same line.
func secretAbsent(t *testing.T, out, secret, what string) {
	t.Helper()
	if strings.Contains(out, secret) {
		t.Fatalf("%s leaked the secret %q:\n%s", what, secret, out)
	}
}

// TestAttrDiff_MaskRedactsRevealedValues: with show_sensitive the values arrive in the
// clear, so the mask is the only thing standing between the secret and the screen. It
// has to hold at every shape a mask can take — a scalar attr, a key inside a nested
// object, a list element — and on every diff branch.
func TestAttrDiff_MaskRedactsRevealedValues(t *testing.T) {
	const secret = "hunter2"
	vals := map[string]any{
		"id":      "x",
		"pw":      secret,
		"keepers": map[string]any{"env": "prod", "token": secret},
		"certs":   []any{"public", secret},
	}
	// The OTF shape: true at a sensitive node, non-sensitive entries omitted, lists
	// positional with null where nothing is sensitive.
	mask := map[string]any{
		"pw":      true,
		"keepers": map[string]any{"token": true},
		"certs":   []any{nil, true},
	}

	create := plainNorm(strings.Join(attrDiff(nil, vals, nil, mask, ""), "\n"))
	secretAbsent(t, create, secret, "create diff")
	for _, want := range []string{"+ pw = (sensitive)", "+ token = (sensitive)", `+ env = "prod"`} {
		if !strings.Contains(create, want) {
			t.Fatalf("create diff missing %q:\n%s", want, create)
		}
	}

	destroy := plainNorm(strings.Join(attrDiff(vals, nil, mask, nil, ""), "\n"))
	secretAbsent(t, destroy, secret, "destroy diff")
	if !strings.Contains(destroy, "- pw = (sensitive)") {
		t.Fatalf("destroy diff missing masked scalar:\n%s", destroy)
	}

	// Sensitivity newly applied to an attribute that was in the clear: at most one side
	// is masked, so this stays the ordinary inline old → new.
	added := plainNorm(strings.Join(attrDiff(
		map[string]any{"pw": "old"}, map[string]any{"pw": secret},
		nil, map[string]any{"pw": true}, ""), "\n"))
	secretAbsent(t, added, secret, "newly-sensitive diff")
	if !strings.Contains(added, `~ pw = "old" → (sensitive)`) {
		t.Fatalf("newly-sensitive attr should read old → (sensitive): %q", added)
	}

	// A changed collection expands the new value, and the mask still bites inside it.
	coll := plainNorm(strings.Join(attrDiff(
		map[string]any{"keepers": map[string]any{"token": "before"}},
		map[string]any{"keepers": map[string]any{"token": secret}},
		map[string]any{"keepers": map[string]any{"token": true}},
		map[string]any{"keepers": map[string]any{"token": true}}, ""), "\n"))
	secretAbsent(t, coll, secret, "collection diff")
}

// TestAttrDiff_MaskedChange covers the two halves of an unprintable change: redacted,
// the sentinel is identical on both sides and there is nothing to report; revealed, the
// renderer can prove it moved and says so without printing either half.
func TestAttrDiff_MaskedChange(t *testing.T) {
	mask := map[string]any{"pw": true}

	// Redacted (the default): both sides are the same sentinel, so the row drops out —
	// unchanged from before masks existed. The renderer genuinely cannot tell.
	hidden := plainNorm(strings.Join(attrDiff(
		map[string]any{"n": float64(1), "pw": "__cty_sensitive__"},
		map[string]any{"n": float64(2), "pw": "__cty_sensitive__"},
		mask, mask, ""), "\n"))
	if strings.Contains(hidden, "pw") {
		t.Fatalf("an indistinguishable sensitive attr should not render a row: %q", hidden)
	}
	if !strings.Contains(hidden, "~ n = 1 → 2") {
		t.Fatalf("the ordinary attr should still diff: %q", hidden)
	}

	// Revealed: two different plaintexts, so the change is real. Same shape as any
	// other scalar change, both halves masked.
	shown := plainNorm(strings.Join(attrDiff(
		map[string]any{"pw": "old-secret"},
		map[string]any{"pw": "new-secret"},
		mask, mask, ""), "\n"))
	secretAbsent(t, shown, "old-secret", "masked change")
	secretAbsent(t, shown, "new-secret", "masked change")
	if !strings.Contains(shown, "~ pw = (sensitive) → (sensitive)") {
		t.Fatalf("a proven change to an unprintable value should read (sensitive) → (sensitive): %q", shown)
	}

	// A revealed *collection* takes the same shape rather than expanding and losing
	// the arrow.
	coll := plainNorm(strings.Join(attrDiff(
		map[string]any{"pw": map[string]any{"k": "old-secret"}},
		map[string]any{"pw": map[string]any{"k": "new-secret"}},
		mask, mask, ""), "\n"))
	secretAbsent(t, coll, "old-secret", "masked collection change")
	if !strings.Contains(coll, "~ pw = (sensitive) → (sensitive)") {
		t.Fatalf("a masked collection change should take the same shape: %q", coll)
	}
}

// TestKvLinesMasked_CollapsesSensitiveSubtrees is the plain-view half: a mask covering a
// whole map or list must stop the expansion entirely, not just redact the leaves.
func TestKvLinesMasked_CollapsesSensitiveSubtrees(t *testing.T) {
	const secret = "hunter2"
	m := map[string]any{
		"id":      "x",
		"config":  map[string]any{"user": "root", "pass": secret},
		"whole":   map[string]any{"a": secret, "b": secret},
		"ports":   []any{float64(80), secret},
		"nothing": "plain",
	}
	mask := map[string]any{
		"config": map[string]any{"pass": true},
		"whole":  true,
		"ports":  []any{nil, true},
	}
	out := plainNorm(strings.Join(kvLinesMasked(m, mask, "", 0), "\n"))
	secretAbsent(t, out, secret, "kvLinesMasked")
	for _, want := range []string{
		`id = "x"`,
		"config = {",
		`user = "root"`,
		"pass = (sensitive)",
		"whole = (sensitive)", // the whole subtree collapses; no brace, no keys
		"ports = [80, (sensitive)]",
		`nothing = "plain"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("kvLinesMasked missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "whole = {") {
		t.Fatalf("a wholly-sensitive map must not expand:\n%s", out)
	}
}

// TestRenderers_MasksSurviveToTheTimeline drives the real renderers with the payload a
// show_sensitive call produces — plaintext values plus their mask — and asserts nothing
// reaches the rendered line. This is the end the mask plumbing exists for.
func TestRenderers_MasksSurviveToTheTimeline(t *testing.T) {
	const secret = "hunter2"
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"turf_replan", `{
			"resources": [{
				"address": "random_password.pw", "action": "create",
				"after": {"result": "` + secret + `", "length": 16},
				"after_sensitive": {"result": true}
			}]
		}`, "+ result = (sensitive)"},
		{"turf_plan_new", `{
			"resources": [{
				"address": "random_pet.p", "action": "update",
				"before": {"keepers": {"pw": "` + secret + `"}},
				"after": {"keepers": {"pw": "` + secret + `-next"}},
				"before_sensitive": {"keepers": {"pw": true}},
				"after_sensitive": {"keepers": {"pw": true}}
			}]
		}`, "pw = (sensitive)"},
		{"turf_effect_apply", `{
			"kind": "create", "state": "done", "resource_addr": "random_password.pw",
			"new_state": {"result": "` + secret + `"},
			"sensitive_values": {"result": true}
		}`, "result = (sensitive)"},
		{"turf_outputs", `{
			"workspace_alias": "default",
			"outputs": {"pw": {"value": "` + secret + `", "sensitive": true},
			            "name": {"value": "plain", "sensitive": false}}
		}`, "pw = (sensitive)"},
		{"turf_datasource_read", `{
			"resource_addr": "data.vault_generic_secret.db",
			"state": {"data": {"password": "` + secret + `"}},
			"sensitive_values": {"data": true}
		}`, "data = (sensitive)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := renderFor(tc.name, tc.content, service.StaticSessionState{})
			secretAbsent(t, out, secret, tc.name)
			if !strings.Contains(plainNorm(out), tc.want) {
				t.Fatalf("%s missing %q:\n%s", tc.name, tc.want, out)
			}
		})
	}
}

// TestTurfToolTargetArgsMatchServerSchemas is the drift guard turfToolTargetArgs
// went without — and it is exactly the map that rotted when the server renamed
// workspace_open/workspace_delete's `name` argument to `workspace_name`. A stale
// entry here is silent: errorTarget simply finds nothing, and a failed tool call
// loses the lead context that tells the reader WHAT failed.
//
// It asserts every arg name is a real property of that tool's live input schema.
// The reverse direction is deliberately not asserted: a tool may take many args
// and the map names only the one that identifies the target.
//
// Note this guards REQUEST arguments only. The server publishes no result schema
// over MCP, so a renamed RESULT field stays unguarded — parseContent decodes it as
// a zero value and the line quietly degrades. The per-tool renderer tests, whose
// JSON literals are hand-written against the server's structs, are the only cover
// there.
func TestTurfToolTargetArgsMatchServerSchemas(t *testing.T) {
	byName := serverTools(t)

	for bare, args := range turfToolTargetArgs {
		name := "turf_" + bare
		tl, ok := byName[name]
		if !ok {
			t.Errorf("turfToolTargetArgs names %q, which is not a server tool (stale entry)", name)
			continue
		}
		props := schemaProperties(t, tl)
		if props == nil {
			t.Errorf("%s: could not read input schema properties", name)
			continue
		}
		for _, arg := range args {
			if _, ok := props[arg]; !ok {
				t.Errorf("%s: target arg %q is not in the server's input schema (renamed or removed) — known args: %s",
					name, arg, strings.Join(sortedKeys(props), ", "))
			}
		}
	}
}

// schemaProperties pulls the `properties` object out of a tool's JSON input schema.
// Tool.Parameters is an `any` holding whatever the MCP layer decoded, so this goes
// through JSON rather than guessing at a concrete Go type.
func schemaProperties(t *testing.T, tl tools.Tool) map[string]any {
	t.Helper()

	raw, err := json.Marshal(tl.Parameters)
	if err != nil {
		return nil
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if json.Unmarshal(raw, &schema) != nil {
		return nil
	}
	return schema.Properties
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- ephemeral resources ------------------------------------------------------

// Every walk re-opens every declared ephemeral resource, so a plan that opened
// them all has nothing to report on its summary line — tallying "3 opened" there
// would read as a change that did not happen. What did NOT open is the signal.
func TestPlanSummary_EphemeralOpensOnlyReportIssues(t *testing.T) {
	allOpened := `{"phase_id":"ph_1","path":"infra","resources":[],"ephemeral_opens":[
		{"address":"ephemeral.vault_kv_secret_v2.a","action":"opened"},
		{"address":"ephemeral.vault_kv_secret_v2.b","action":"opened"}]}`

	compact := plainNorm(renderFor("turf_replan", allOpened, hiddenState{}))
	if strings.Contains(compact, "opened") || strings.Contains(compact, "ephemeral") {
		t.Fatalf("a fully-opened walk should say nothing on the summary line: %q", compact)
	}
	// It still belongs in the expansion — that is where you go to see the opens.
	detailed := plainNorm(renderFor("turf_replan", allOpened, service.StaticSessionState{}))
	for _, want := range []string{"ephemeral:", "vault_kv_secret_v2.a", "opened"} {
		if !strings.Contains(detailed, want) {
			t.Fatalf("detail missing %q: %q", want, detailed)
		}
	}

	oneDeferred := `{"phase_id":"ph_1","path":"infra","resources":[],"ephemeral_opens":[
		{"address":"ephemeral.vault_kv_secret_v2.a","action":"opened"},
		{"address":"ephemeral.vault_kv_secret_v2.b","action":"deferred","reason":"config_unknown"}]}`
	out := plainNorm(renderFor("turf_replan", oneDeferred, hiddenState{}))
	if !strings.Contains(out, "1 ephemeral deferred") {
		t.Fatalf("summary should surface the deferral: %q", out)
	}
}

// --- provider instance status -------------------------------------------------

// The walk, not workspace_open, configures providers now — and an unresolved
// instance is usually the reason a plan deferred, so the line has to say so.
func TestPlanSummary_ProviderStatus(t *testing.T) {
	const content = `{"phase_id":"ph_1","path":"infra","resources":[],"providers":[
		{"ref":"random","status":"configured"},
		{"ref":"aws.west","status":"configured_with_unknowns",
		 "unknown_paths":["assume_role.role_arn"],
		 "feeding_addresses":["aws_iam_role.deploy"],
		 "teardown_config":"prior_state"},
		{"ref":"vault","status":"deferred","last_error":"dial tcp: connection refused"}]}`

	compact := plainNorm(renderFor("turf_plan_new", content, hiddenState{}))
	if !strings.Contains(compact, "2 provider(s) unresolved") {
		t.Fatalf("summary should count only the unresolved instances: %q", compact)
	}

	detailed := plainNorm(renderFor("turf_plan_new", content, service.StaticSessionState{}))
	for _, want := range []string{
		"providers:", "random", "configured",
		"aws.west", "configured_with_unknowns",
		"unknown", "assume_role.role_arn",
		"feeds from", "aws_iam_role.deploy",
		"teardown prior_state",
		"vault", "deferred", "connection refused",
	} {
		if !strings.Contains(detailed, want) {
			t.Fatalf("provider detail missing %q: %q", want, detailed)
		}
	}
}

// A walk where every provider configured is the ordinary case and must cost the
// summary line nothing.
func TestPlanSummary_ProviderStatusSilentWhenAllConfigured(t *testing.T) {
	const content = `{"phase_id":"ph_1","path":"infra","resources":[],
		"providers":[{"ref":"random","status":"configured"}]}`
	out := plainNorm(renderFor("turf_plan_new", content, hiddenState{}))
	if strings.Contains(out, "unresolved") {
		t.Fatalf("all-configured walk should not claim an issue: %q", out)
	}
}

// --- config_init discovery ----------------------------------------------------

// The server states `defaulted` positively and omits the false, so ABSENCE means
// the directory really declares a backend. Reading it the other way round would
// label every configuration wrong.
func TestConfigInit_BackendDefaultedPolarity(t *testing.T) {
	declared := plainNorm(renderFor("turf_config_init",
		`{"path":"infra","backend":{"type":"s3"},"workspace":{"name":"main"}}`,
		hiddenState{}))
	if !strings.Contains(declared, "backend s3") || strings.Contains(declared, "(default)") {
		t.Fatalf("a declared backend must not read as defaulted: %q", declared)
	}

	synthesized := plainNorm(renderFor("turf_config_init",
		`{"path":"infra","backend":{"type":"local","defaulted":true},"workspace":{"name":"main"}}`,
		hiddenState{}))
	if !strings.Contains(synthesized, "backend local (default)") {
		t.Fatalf("the synthesized default must say so: %q", synthesized)
	}
}

// Drift is what the NEXT plan will do about this directory — orphan destroys and
// pending creates — so it earns a summary segment, in the plan's own glyph idiom.
func TestConfigInit_Drift(t *testing.T) {
	const content = `{"path":"infra","backend":{"type":"local","defaulted":true},
		"workspace":{"name":"main"},"scratch":true,
		"drift":[{"workspace_alias":"default",
			"in_state_not_declared":["aws_s3_bucket.old"],
			"declared_not_in_state":["aws_s3_bucket.new","random_pet.this"]}]}`

	compact := plainNorm(renderFor("turf_config_init", content, hiddenState{}))
	for _, want := range []string{"scratch", "+2", "-1", "drift"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact missing %q: %q", want, compact)
		}
	}

	detailed := plainNorm(renderFor("turf_config_init", content, service.StaticSessionState{}))
	for _, want := range []string{"drift:", "default", "+ aws_s3_bucket.new", "- aws_s3_bucket.old"} {
		if !strings.Contains(detailed, want) {
			t.Fatalf("drift detail missing %q: %q", want, detailed)
		}
	}
}

// An inferred requirement was never written down and carries no version
// constraint; an ephemeral variable may never be stored. Both are things the
// reader has to be able to tell apart from the declared/ordinary case.
func TestConfigInit_InferredAndEphemeralBadges(t *testing.T) {
	const content = `{"path":"infra","workspace":{"name":"main"},
		"required_providers":{"random":{"source":"hashicorp/random","inferred":true},
			"aws":{"source":"hashicorp/aws","version":"5.1.0"}},
		"variables":[{"name":"token","required":true,"ephemeral":true},
			{"name":"region","required":true}]}`

	detailed := plainNorm(renderFor("turf_config_init", content, service.StaticSessionState{}))
	for _, want := range []string{"random hashicorp/random inferred", "token", "ephemeral"} {
		if !strings.Contains(detailed, want) {
			t.Fatalf("init detail missing %q: %q", want, detailed)
		}
	}
	// The declared provider and the ordinary variable must NOT pick up a badge.
	if strings.Contains(detailed, "hashicorp/aws 5.1.0 inferred") {
		t.Fatalf("a declared requirement must not read as inferred: %q", detailed)
	}
}

// --- config_show index --------------------------------------------------------

// Every entry's address spells out its type — random_pet.this, module.vpc — but
// the backend's address is the bare word "backend", so the type it declares
// rides on the entry instead. Dropping it leaves the one entry whose identity
// the address cannot carry saying nothing at all.
func TestConfigShow_BackendCarriesItsType(t *testing.T) {
	const content = `{"config_alias":"infra","path":"infra","entries":[
		{"address":"backend","kind":"backend","type":"s3","file":"backend.tf"},
		{"address":"random_pet.this","kind":"resource","file":"main.tf"}]}`

	out := plainNorm(renderFor("turf_config_show", content, service.StaticSessionState{}))
	if !strings.Contains(out, "backend s3") {
		t.Fatalf("backend entry must name its type: %q", out)
	}
	// An ordinary entry has no type field and must not grow a trailing space
	// where one would go.
	if !strings.Contains(out, "main.tf · resource") {
		t.Fatalf("non-backend entry should read unchanged: %q", out)
	}
	// A multi-entry index summarizes as the bare entry count.
	if !strings.Contains(out, "2 declared address(es)") {
		t.Fatalf("index summary should count the entries: %q", out)
	}
}

// A single-entry query is summarized on the compact line, which is the whole
// answer when results are hidden — so the type has to reach that line too.
func TestConfigShow_SingleBackendSummary(t *testing.T) {
	const content = `{"config_alias":"infra","path":"infra","entries":[
		{"address":"backend","kind":"backend","type":"local","file":"backend.tf",
		 "note":"declared in backend.tf; edit the file and replan to change it"}]}`

	compact := plainNorm(renderFor("turf_config_show", content, hiddenState{}))
	if !strings.Contains(compact, "backend · backend local") {
		t.Fatalf("compact single-entry summary must carry the type: %q", compact)
	}
}

// --- remaining field signal ---------------------------------------------------

// A tainted object is replaced by the next plan and a deposed one is still real
// infrastructure — neither is visible in a plain address listing.
func TestStateList_TaintedAndDeposed(t *testing.T) {
	const content = `{"workspace_alias":"default","resources":[
		{"address":"aws_instance.web","type":"aws_instance","mode":"managed","tainted":true,
		 "deposed":[{"key":"abc12345"}]},
		{"address":"random_pet.this","type":"random_pet","mode":"managed"}]}`

	compact := plainNorm(renderFor("turf_state_list", content, hiddenState{}))
	for _, want := range []string{"2 resource(s)", "1 tainted", "1 deposed"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact missing %q: %q", want, compact)
		}
	}
	detailed := plainNorm(renderFor("turf_state_list", content, service.StaticSessionState{}))
	if !strings.Contains(detailed, "aws_instance.web") || !strings.Contains(detailed, "tainted") {
		t.Fatalf("detail missing the tainted row: %q", detailed)
	}
}
