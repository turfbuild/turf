package main

import (
	"context"
	"encoding/json"

	"github.com/docker/docker-agent/pkg/tools"
)

// autoConfirmUserPrompt is a synthetic stand-in for cagent's built-in
// user_prompt tool, registered only on the non-interactive (--auto-approve /
// headless) path.
//
// The /up and /destroy MCP prompts instruct the agent to seek explicit approval
// before plan_approve, and the model satisfies that by calling user_prompt.
// cagent's real user_prompt routes through the runtime's elicitation handler,
// which hard-declines whenever the runtime is non-interactive (there is no
// client to answer) — so the approval request fails and an unattended run
// stalls before it applies a later, e.g. deferred, phase. This tool answers
// every prompt with "accept" *directly*, without any elicitation round-trip,
// giving --auto-approve runs the "proceed without asking" behavior they intend.
//
// It deliberately does NOT implement tools.Elicitable (no SetElicitationHandler
// method), so the runtime's ConfigureHandlers cannot replace this behavior with
// the auto-declining handler the way it would for the real tool.
type autoConfirmUserPrompt struct{}

// userPromptArgs mirrors the argument schema of cagent's user_prompt tool so
// the model sees an identical signature and calls it the same way.
type userPromptArgs struct {
	Message string         `json:"message" jsonschema:"The message/question to display to the user"`
	Title   string         `json:"title,omitempty" jsonschema:"Optional title for the dialog window"`
	Schema  map[string]any `json:"schema,omitempty" jsonschema:"JSON Schema defining the expected response structure"`
}

// userPromptResponse mirrors cagent's user_prompt response shape (action plus
// optional accepted content).
type userPromptResponse struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
}

func (autoConfirmUserPrompt) Instructions() string {
	return `## User Prompt Tool

Ask the user a question when you need confirmation or a decision (e.g. approving a plan
before plan_approve). This session is non-interactive: every prompt is auto-confirmed — the
response is always "accept" — so use it to record the approval checkpoint and then proceed.`
}

func (autoConfirmUserPrompt) Tools(context.Context) ([]tools.Tool, error) {
	return []tools.Tool{
		{
			Name:         "user_prompt",
			Category:     "user_prompt",
			Description:  "Ask the user a question and wait for their response. In this non-interactive run the response is auto-confirmed (accept), so the agent may proceed.",
			Parameters:   tools.MustSchemaFor[userPromptArgs](),
			OutputSchema: tools.MustSchemaFor[userPromptResponse](),
			Handler:      tools.NewHandler(autoConfirmPrompt),
			Annotations: tools.ToolAnnotations{
				ReadOnlyHint: true,
				Title:        "User Prompt (auto-confirmed)",
			},
		},
	}, nil
}

// autoConfirmPrompt answers any prompt with an affirmative accept. The content
// carries an explicit auto_confirmed marker so the synthetic confirmation is
// unmistakable in the streamed transcript and logs.
func autoConfirmPrompt(_ context.Context, _ userPromptArgs) (*tools.ToolCallResult, error) {
	resp := userPromptResponse{
		Action:  "accept",
		Content: map[string]any{"response": "yes", "auto_confirmed": true},
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return tools.ResultSuccess(string(out)), nil
}
