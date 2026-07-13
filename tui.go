package main

import (
	"context"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/docker/docker-agent/pkg/app"
	"github.com/docker/docker-agent/pkg/cli"
	"github.com/docker/docker-agent/pkg/leantui"
	"github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/tui"
	tuiinput "github.com/docker/docker-agent/pkg/tui/input"
)

func runTUI(ctx context.Context, rt runtime.Runtime, sess *session.Session, firstMessage string, lean bool, worktreeBranch string) error {
	// Apply turf's theme before constructing either TUI. cagent's tui.New /
	// leantui.Run do not auto-apply the persisted/default theme, so turf does it
	// here; leantui reads the same styles package the theme configures.
	applyTurfTheme()

	// Callers decide lean vs full: chat honors --lean, while up/destroy always
	// pass true (the sidebar/tabs are noise for a one-shot deploy/teardown).
	if lean {
		return runLeanTUI(ctx, rt, sess, firstMessage, worktreeBranch)
	}

	var opts []app.Opt
	if firstMessage != "" {
		opts = append(opts, app.WithFirstMessage(firstMessage))
	}
	if gen := rt.TitleGenerator(ctx); gen != nil {
		opts = append(opts, app.WithTitleGenerator(gen))
	}
	a := app.New(ctx, rt, sess, opts...)

	wd, _ := os.Getwd()
	cleanup := func() {}
	// Single-session turf: no spawner (no multi-tab session spawning). Brand the
	// status bar / window title as turf with turf's own version, not cagent's.
	model := tui.New(ctx, nil, a, wd, cleanup,
		tui.WithAppName(appName),
		// The status-bar title renders as "<appName> <version>"; fold the active
		// worktree branch into the version slot so the user sees they're working
		// on a throwaway branch (e.g. "turf 1.2.3 · worktree-focused_turing").
		tui.WithVersion(versionWithBranch(worktreeBranch)),
		// Paint turf's tools as compact, theme-colored conversation lines instead
		// of raw JSON. Keys are the "turf_"-prefixed exposed names (see
		// mcptoolset.go / agent.go). Detail level follows Ctrl+O (HideToolResults).
		tui.WithToolRenderers(turfToolRenderers()),
	)

	// Coalesce raw mouse-wheel events into the WheelCoalescedMsg the TUI acts on.
	// cagent's reusable pkg/tui only handles the *coalesced* message; the raw
	// tea.MouseWheelMsg → WheelCoalescedMsg wiring lives in cagent's own cmd/root
	// entrypoint, not in pkg/tui. Because turf builds the program itself, it must
	// replicate that wiring — otherwise raw wheel events reach the transcript
	// component, which ignores them, and the scroll wheel does nothing.
	coalescer := tuiinput.NewWheelCoalescer()
	filter := func(_ tea.Model, msg tea.Msg) tea.Msg {
		wheelMsg, ok := msg.(tea.MouseWheelMsg)
		if !ok {
			return msg
		}
		if coalescer.Handle(wheelMsg) {
			return nil
		}
		return msg
	}

	p := tea.NewProgram(model, tea.WithContext(ctx), tea.WithFilter(filter))
	coalescer.SetSender(p.Send)
	if m, ok := model.(interface{ SetProgram(p *tea.Program) }); ok {
		m.SetProgram(p)
	}

	_, err := p.Run()
	return err
}

// runLeanTUI drives cagent's standalone lean TUI (pkg/leantui) instead of the
// full bubbletea TUI, used when --lean is set. Lean mode renders to the normal
// terminal buffer (no alternate screen), drops the sidebar/tab bar/overlays, and
// owns its own input loop — so, unlike runTUI, it builds no tea.Program and needs
// no wheel coalescer. It reuses the same global tool-renderer registry as the
// full TUI, but never calls tui.New (where tui.WithToolRenderers registers them),
// so turf's per-tool renderers are registered directly here. leantui also reads
// the first message from Config.FirstMessage rather than app.WithFirstMessage, so
// the prompt is threaded in explicitly. Mirrors cagent's own runLeanTUI, minus
// the worktree/attachment/queued-message plumbing turf doesn't use.
// turfBanner is turf's ASCII-art welcome banner for the lean TUI, in the same
// figlet "ANSI Shadow" style as cagent's default. Passed via leantui.Config.Banner
// so lean mode reads "TURF" instead of cagent's built-in "docker agent" art (the
// banner is hardcoded art, not derived from AppName).
var turfBanner = []string{
	`████████╗██╗   ██╗██████╗ ███████╗`,
	`╚══██╔══╝██║   ██║██╔══██╗██╔════╝`,
	`   ██║   ██║   ██║██████╔╝█████╗  `,
	`   ██║   ██║   ██║██╔══██╗██╔══╝  `,
	`   ██║   ╚██████╔╝██║  ██║██║     `,
	`   ╚═╝    ╚═════╝ ╚═╝  ╚═╝╚═╝     `,
}

func runLeanTUI(ctx context.Context, rt runtime.Runtime, sess *session.Session, firstMessage string, worktreeBranch string) error {
	registerTurfToolRenderers()

	var opts []app.Opt
	if gen := rt.TitleGenerator(ctx); gen != nil {
		opts = append(opts, app.WithTitleGenerator(gen))
	}
	a := app.New(ctx, rt, sess, opts...)
	a.Start(ctx) // lean mode drives the app itself; the full TUI's model does this in Init

	wd, _ := os.Getwd()
	cfg := leantui.Config{
		App:        a,
		WorkingDir: wd,
		Cleanup:    func() {},
		// Lean mode's status footer has no version slot, so fold the active
		// worktree branch into the app name instead (e.g. "turf · worktree-...").
		AppName: appNameWithBranch(worktreeBranch),
		Banner:  turfBanner,
	}
	if firstMessage != "" {
		cfg.FirstMessage = &firstMessage // leantui reads this, not app.WithFirstMessage
	}
	return leantui.Run(ctx, cfg)
}

func runExec(ctx context.Context, rt runtime.Runtime, sess *session.Session, autoApprove bool) error {
	out := cli.NewPrinter(os.Stdout)
	return cli.Run(ctx, out, cli.Config{
		AppName:     "turf",
		AutoApprove: autoApprove,
	}, rt, sess, []string{"exec", "Please proceed."})
}
