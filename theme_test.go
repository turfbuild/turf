package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker-agent/pkg/tui/styles"
)

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
