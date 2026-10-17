package main

import (
	"maps"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/docker/docker-agent/pkg/tui/commands"
	"github.com/docker/docker-agent/pkg/tui/messages"
)

func TestServerPromptArgs(t *testing.T) {
	for _, tc := range []struct {
		name         string
		instructions string
		autoApprove  bool
		want         map[string]string
	}{
		{"path only", "", false, map[string]string{"config_path": "/w"}},
		{"blank instructions dropped", "  \t", false, map[string]string{"config_path": "/w"}},
		{"instructions trimmed", " use env/staging.tfvars ", false, map[string]string{"config_path": "/w", "instructions": "use env/staging.tfvars"}},
		{"auto approve", "", true, map[string]string{"config_path": "/w", "auto_approve": "true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := serverPromptArgs("/w", tc.instructions, tc.autoApprove); !maps.Equal(got, tc.want) {
				t.Errorf("serverPromptArgs = %v, want %v", got, tc.want)
			}
		})
	}
}

// F9 (rc.1 acceptance): the stock slash command handed "/destroy staging: use
// env/staging.tfvars" to config_path. The TUI's /up and /destroy must build the
// same arguments `turf up` and `turf destroy` do, and leave other prompts alone.
func TestWithTurfPromptArgs(t *testing.T) {
	stock := func(name string) commands.Item {
		return commands.Item{
			ID:           "mcp.prompt." + name,
			SlashCommand: "/" + name,
			Execute: func(arg string) tea.Cmd {
				return func() tea.Msg { return "stock " + name + " " + arg }
			},
		}
	}
	cats := withTurfPromptArgs([]commands.Category{
		{Name: "Session", Commands: []commands.Item{{ID: "session.clear", Execute: func(string) tea.Cmd {
			return func() tea.Msg { return "clear" }
		}}}},
		{Name: "MCP Prompts", Commands: []commands.Item{stock("up"), stock("destroy"), stock("guide")}},
	}, "/work/env")

	run := func(id, arg string) tea.Msg {
		t.Helper()
		for _, c := range cats {
			for _, it := range c.Commands {
				if it.ID == id {
					return it.Execute(arg)()
				}
			}
		}
		t.Fatalf("no item %s", id)
		return nil
	}

	for _, tc := range []struct {
		id, arg string
		want    messages.MCPPromptMsg
	}{
		{"mcp.prompt.up", "staging: use env/staging.tfvars", messages.MCPPromptMsg{
			PromptName: "up",
			Arguments:  map[string]string{"config_path": "/work/env", "instructions": "staging: use env/staging.tfvars"},
		}},
		{"mcp.prompt.up", "", messages.MCPPromptMsg{
			PromptName: "up",
			Arguments:  map[string]string{"config_path": "/work/env"},
		}},
		{"mcp.prompt.destroy", "only module.staging", messages.MCPPromptMsg{
			PromptName: "destroy",
			Arguments:  map[string]string{"config_path": "/work/env", "instructions": "only module.staging"},
		}},
	} {
		got, ok := run(tc.id, tc.arg).(messages.MCPPromptMsg)
		if !ok || got.PromptName != tc.want.PromptName || !maps.Equal(got.Arguments, tc.want.Arguments) {
			t.Errorf("%s %q = %#v, want %#v", tc.id, tc.arg, got, tc.want)
		}
	}

	if got := run("mcp.prompt.guide", "aicr"); got != "stock guide aicr" {
		t.Errorf("/guide = %v, want the stock mapping", got)
	}
	if got := run("session.clear", ""); got != "clear" {
		t.Errorf("session.clear = %v, want it untouched", got)
	}
}
