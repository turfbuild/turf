package main

import (
	"context"
	"encoding/json"

	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tools/builtin/userprompt"
)

// userPromptToolset picks the user_prompt the agent gets, by who can answer it:
//
//   - --yes/--auto-approve: the user pre-approved this run, so every prompt is
//     answered yes, attended or not (an interactive run with --yes shows no
//     dialog either — that is the flag's point).
//   - an attended terminal: cagent's real tool, which the TUI renders as a dialog.
//   - anything else (up/destroy over a pipe, exec, CI) without --yes: every prompt
//     is declined. Nobody can say yes, and silence is not consent — so a destroy,
//     a replace, an import or an invoke stops at its approval point instead of
//     going ahead. Approval without a person takes --yes.
//
// cagent's real tool is no answer for the unattended case: it routes through the
// runtime's elicitation handler, which declines with no client to answer but tells
// the agent nothing about why, or how the run could be approved.
func userPromptToolset(interactive, autoApprove bool) tools.ToolSet {
	switch {
	case autoApprove:
		return unattendedUserPrompt{approve: true}
	case interactive:
		return userprompt.New()
	default:
		return unattendedUserPrompt{}
	}
}

// unattendedUserPrompt is a synthetic stand-in for cagent's built-in user_prompt
// tool, for a run with nobody to show a dialog to, or whose user already said yes:
// it answers every prompt directly — yes when approve is set (--yes), no otherwise.
//
// It deliberately does NOT implement tools.Elicitable (no SetElicitationHandler
// method), so the runtime's ConfigureHandlers cannot replace its answer with the
// elicitation handler's.
type unattendedUserPrompt struct {
	approve bool
}

// userPromptArgs mirrors the argument schema of cagent's user_prompt tool so
// the model sees an identical signature and calls it the same way.
type userPromptArgs struct {
	Message string         `json:"message" jsonschema:"The message/question to display to the user"`
	Title   string         `json:"title,omitempty" jsonschema:"Optional title for the dialog window"`
	Schema  map[string]any `json:"schema,omitempty" jsonschema:"JSON Schema defining the expected response structure"`
}

// userPromptResponse mirrors cagent's user_prompt response shape (action plus
// optional content).
type userPromptResponse struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
}

// unattendedDeclineReason is what a declined prompt tells the agent, so it can
// tell the user how the run could be approved.
const unattendedDeclineReason = "Nobody is at a terminal to answer, and this run was not started with --yes, so nothing that needs the user's yes can be approved. Stop there: say what is waiting for approval, and that re-running with --yes approves it, or that the user can pick it up in an interactive turf."

func (p unattendedUserPrompt) Instructions() string {
	if p.approve {
		return `## User Prompt Tool

Ask the user a question when you need confirmation or a decision (e.g. approving a plan
before turf_apply). This run was started with --yes, so every prompt is auto-confirmed —
the response is always "accept" — so use it to record the approval checkpoint and then proceed.`
	}
	return `## User Prompt Tool

Ask the user a question when you need confirmation or a decision (e.g. approving a plan
before turf_apply). This run is unattended and was not started with --yes: nobody can
answer, so every prompt is declined. Treat that as a "no": apply nothing that needs the
user's yes, and end by saying what is waiting for approval and that re-running with --yes
approves it.`
}

func (p unattendedUserPrompt) Tools(context.Context) ([]tools.Tool, error) {
	description := "Ask the user a question and wait for their response. This run is unattended and was not started with --yes, so every prompt is declined."
	title := "User Prompt (declined: unattended)"
	if p.approve {
		description = "Ask the user a question and wait for their response. This run was started with --yes, so the response is auto-confirmed (accept) and the agent may proceed."
		title = "User Prompt (auto-confirmed)"
	}
	return []tools.Tool{
		{
			Name:         "user_prompt",
			Category:     "user_prompt",
			Description:  description,
			Parameters:   tools.MustSchemaFor[userPromptArgs](),
			OutputSchema: tools.MustSchemaFor[userPromptResponse](),
			Handler:      tools.NewHandler(p.answer),
			Annotations: tools.ToolAnnotations{
				ReadOnlyHint: true,
				Title:        title,
			},
		},
	}, nil
}

// answer replies to any prompt without asking anyone. The content carries an
// explicit auto_confirmed or unattended marker, so the synthetic answer is
// unmistakable in the streamed transcript and logs.
func (p unattendedUserPrompt) answer(_ context.Context, _ userPromptArgs) (*tools.ToolCallResult, error) {
	resp := userPromptResponse{
		Action:  "decline",
		Content: map[string]any{"unattended": true, "reason": unattendedDeclineReason},
	}
	if p.approve {
		resp = userPromptResponse{
			Action:  "accept",
			Content: map[string]any{"response": "yes", "auto_confirmed": true},
		}
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return tools.ResultSuccess(string(out)), nil
}
