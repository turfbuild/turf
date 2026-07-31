package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/docker/docker-agent/pkg/tui/styles"
)

// turfTerrainThemes are the terrain themes turf ships from assets/themes. meadow
// is also the brand default (defaultThemeRef).
var turfTerrainThemes = []string{"meadow", "stadium", "sonora", "lunar"}

// TestTurfThemesRegistered verifies each embedded terrain theme registers with
// cagent's theme engine: it appears in ListThemeRefs and loads (parses) cleanly
// with a display name. This guards the assets/themes YAML and the fs.Sub wiring.
func TestTurfThemesRegistered(t *testing.T) {
	registerTurfThemes()

	refs, err := styles.ListThemeRefs()
	if err != nil {
		t.Fatalf("ListThemeRefs() error: %v", err)
	}

	for _, ref := range turfTerrainThemes {
		if !slices.Contains(refs, ref) {
			t.Errorf("theme %q not in ListThemeRefs()", ref)
		}
		theme, err := styles.LoadTheme(ref)
		if err != nil {
			t.Errorf("LoadTheme(%q) error: %v", ref, err)
			continue
		}
		if theme.Name == "" {
			t.Errorf("LoadTheme(%q): empty Name", ref)
		}
	}

	// The brand default is one of the terrain themes.
	if !slices.Contains(turfTerrainThemes, defaultThemeRef) {
		t.Errorf("defaultThemeRef %q is not a shipped terrain theme", defaultThemeRef)
	}
}

// TestApplyTurfTheme verifies that applyTurfTheme redirects cagent's theme engine
// at turf's home and applies the brand default, without touching ~/.cagent.
func TestApplyTurfTheme(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TURF_HOME", home)

	applyTurfTheme()

	// Themes resolve from <TURF_HOME>/themes, and the dir is created.
	wantThemes := filepath.Join(home, "themes")
	if got := styles.ThemesDir(); got != wantThemes {
		t.Fatalf("ThemesDir() = %q, want %q", got, wantThemes)
	}
	if _, err := os.Stat(wantThemes); err != nil {
		t.Fatalf("themes dir not created: %v", err)
	}

	// With no persisted choice, turf's brand default is applied.
	if got := styles.CurrentTheme().Ref; got != defaultThemeRef {
		t.Fatalf("applied theme ref = %q, want %q", got, defaultThemeRef)
	}
}

// TestApplyTurfThemeFlagOverride verifies that --theme (flagTheme) overrides the
// brand default for the run.
func TestApplyTurfThemeFlagOverride(t *testing.T) {
	t.Setenv("TURF_HOME", t.TempDir())

	const override = "tokyo-night"
	flagTheme = override
	t.Cleanup(func() { flagTheme = "" })

	applyTurfTheme()

	if got := styles.CurrentTheme().Ref; got != override {
		t.Fatalf("applied theme ref = %q, want %q", got, override)
	}
}

// TestTurfHome confirms the config-home resolution honors TURF_HOME.
func TestTurfHome(t *testing.T) {
	t.Setenv("TURF_HOME", "/custom/turf")
	if got := turfHome(); got != "/custom/turf" {
		t.Fatalf("turfHome() = %q, want /custom/turf", got)
	}
}
