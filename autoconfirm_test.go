package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tools/builtin/userprompt"
)

// TestUserPromptToolsetByAttendance pins who answers the agent's user_prompt: a
// person at a terminal (the real dialog), --yes (always yes), or nobody (always
// no). The last row is the rule that approval without a person takes --yes: a
// piped `turf destroy` must not destroy anything.
func TestUserPromptToolsetByAttendance(t *testing.T) {
	cases := []struct {
		name                     string
		interactive, autoApprove bool
		wantDialog               bool
		wantAction, wantTitle    string
	}{
		{name: "attended", interactive: true, wantDialog: true},
		{name: "attended with --yes", interactive: true, autoApprove: true, wantAction: "accept", wantTitle: "User Prompt (auto-confirmed)"},
		{name: "unattended with --yes", autoApprove: true, wantAction: "accept", wantTitle: "User Prompt (auto-confirmed)"},
		{name: "unattended", wantAction: "decline", wantTitle: "User Prompt (declined: unattended)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := userPromptToolset(c.interactive, c.autoApprove)
			if c.wantDialog {
				if _, ok := ts.(*userprompt.ToolSet); !ok {
					t.Fatalf("got %T, want cagent's real user_prompt (the TUI dialog)", ts)
				}
				return
			}
			ts2, err := ts.Tools(context.Background())
			if err != nil || len(ts2) != 1 {
				t.Fatalf("Tools() = %v, %v; want one tool", ts2, err)
			}
			tool := ts2[0]
			if tool.Name != "user_prompt" || tool.Annotations.Title != c.wantTitle {
				t.Errorf("tool = %q titled %q, want user_prompt titled %q", tool.Name, tool.Annotations.Title, c.wantTitle)
			}
			call := tools.ToolCall{Function: tools.FunctionCall{Name: "user_prompt", Arguments: `{"message":"Apply this destroy plan?","schema":{"type":"string","enum":["yes","no"]}}`}}
			res, err := tool.Handler(context.Background(), call, nil)
			if err != nil {
				t.Fatalf("handler: %v", err)
			}
			var resp userPromptResponse
			if err := json.Unmarshal([]byte(res.Output), &resp); err != nil {
				t.Fatalf("output %q: %v", res.Output, err)
			}
			if resp.Action != c.wantAction {
				t.Errorf("action = %q, want %q (output %s)", resp.Action, c.wantAction, res.Output)
			}
			switch c.wantAction {
			case "accept":
				if resp.Content["response"] != "yes" || resp.Content["auto_confirmed"] != true {
					t.Errorf("accept content = %v, want response yes and auto_confirmed", resp.Content)
				}
			case "decline":
				reason, _ := resp.Content["reason"].(string)
				if resp.Content["unattended"] != true || !strings.Contains(reason, "--yes") {
					t.Errorf("decline content = %v, want unattended and a reason naming --yes", resp.Content)
				}
			}
		})
	}
}

// TestUnattendedInstructionsMatchTheAnswer keeps the instructions the model reads
// in step with what the tool answers: a declining tool that told the model "every
// prompt is auto-confirmed" would invite it to proceed.
func TestUnattendedInstructionsMatchTheAnswer(t *testing.T) {
	yes := unattendedUserPrompt{approve: true}.Instructions()
	no := unattendedUserPrompt{}.Instructions()
	if !strings.Contains(yes, "auto-confirmed") || strings.Contains(yes, "declined") {
		t.Errorf("--yes instructions should say prompts are auto-confirmed:\n%s", yes)
	}
	if !strings.Contains(no, "declined") || strings.Contains(no, "auto-confirmed") || !strings.Contains(no, "--yes") {
		t.Errorf("unattended instructions should say prompts are declined and name --yes:\n%s", no)
	}
}
