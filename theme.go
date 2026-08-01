package main

import (
	"embed"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/docker/docker-agent/pkg/paths"
	"github.com/docker/docker-agent/pkg/tui/styles"
)

// defaultThemeRef is turf's distinctive built-in default theme — "meadow", one of
// turf's own terrain themes shipped from assets/themes (see registerTurfThemes),
// so turf looks distinct out of the box. Change this single constant to re-brand
// the default look.
const defaultThemeRef = "meadow"

// turfThemes embeds turf's own terrain themes. They are authored as partial
// theme YAML (merged onto cagent's stock DefaultTheme) under an assets/themes
// directory; registerTurfThemes exposes them to cagent's theme engine so they
// resolve via LoadTheme, appear in the /theme picker, and can be persisted —
// exactly like cagent's bundled built-ins.
//
//go:embed assets/themes/*.yaml
var turfThemes embed.FS

// registerThemesOnce guards registerTurfThemes so registration happens exactly
// once per process even though applyTurfTheme may be invoked more than once
// (e.g. across tests).
var registerThemesOnce sync.Once

// registerTurfThemes contributes turf's embedded terrain themes to cagent's
// theme engine. cagent's RegisterBuiltinThemes expects theme files under a
// top-level "themes/" path, so we hand it a sub-FS rooted at assets/ (which then
// presents "themes/<ref>.yaml"). Best-effort: a failure is logged, not fatal —
// turf falls back to whatever themes remain resolvable.
func registerTurfThemes() {
	registerThemesOnce.Do(func() {
		sub, err := fs.Sub(turfThemes, "assets")
		if err != nil {
			slog.Warn("turf: could not root embedded themes FS", "error", err)
			return
		}
		if err := styles.RegisterBuiltinThemes(sub); err != nil {
			slog.Warn("turf: could not register built-in themes", "error", err)
		}
	})
}

// turfHome returns turf's config/data home: $TURF_HOME, else ~/.turf.
func turfHome() string {
	if v := os.Getenv("TURF_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".turf"
	}
	return filepath.Join(home, ".turf")
}

// applyTurfTheme points cagent's process-global theme engine at turf's own config
// home (~/.turf, never ~/.cagent) and applies a theme. cagent's tui.New does NOT
// auto-apply the persisted/default theme — its own CLI entrypoint did — so turf must
// apply one itself before the TUI renders. Never hard-fails: on any error it falls
// back to the stock default so the TUI is always styled.
func applyTurfTheme() {
	// Make turf's own terrain themes resolvable before any ref is loaded below.
	registerTurfThemes()

	home := turfHome()
	// Redirect theme loading (styles.ThemesDir() == GetDataDir()/themes), the /theme
	// picker, hot-reload, and persistence at turf's home instead of cagent's.
	paths.SetDataDir(home)   // user themes load from <home>/themes
	paths.SetConfigDir(home) // /theme persistence writes <home>/config.yaml, not ~/.config/cagent

	// Best-effort: ensure <home>/themes exists so a user can drop a YAML in.
	if err := os.MkdirAll(styles.ThemesDir(), 0o755); err != nil {
		slog.Warn("turf: could not create themes directory", "dir", styles.ThemesDir(), "error", err)
	}

	// Precedence (low to high): (a) turf's brand default, (b) a real saved /theme
	// choice, (c) an explicit --theme launch override for this run.
	// GetPersistedThemeRef() returns DefaultThemeRef ("default") when unset, so treat
	// that as "no real choice" and fall through to turf's default instead.
	ref := defaultThemeRef
	if persisted := styles.GetPersistedThemeRef(); persisted != "" && persisted != styles.DefaultThemeRef {
		ref = persisted
	}
	if flagTheme != "" {
		ref = flagTheme
	}

	theme, err := styles.LoadTheme(ref)
	if err != nil {
		// (c) Last resort: stock default so the TUI never starts unstyled.
		slog.Warn("turf: failed to load theme, using stock default", "theme", ref, "error", err)
		theme = styles.DefaultTheme()
	}
	styles.ApplyTheme(theme)
}
