package main

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/docker/docker-agent/pkg/paths"
	"github.com/docker/docker-agent/pkg/tui/styles"
)

// defaultThemeRef is turf's distinctive built-in default theme. It is a built-in
// cagent theme (not the stock "default"), so turf looks distinct out of the box.
// Change this single constant to re-brand the default look.
const defaultThemeRef = "calm-roots"

// TODO(theming): ship a fully custom turf-branded theme. Today this means
// //go:embed a partial turf.yaml and materialize it to styles.ThemesDir() so the
// on-disk loader merges it onto DefaultTheme; a public in-memory ParseTheme is
// proposed upstream (docker/docker-agent#3180) and would let us skip the file write.
// Deferred by choice — the default stays calm-roots, a green/nature palette.

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
