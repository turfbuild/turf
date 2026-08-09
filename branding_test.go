package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// withBranding installs a branding overlay for one test and restores the
// unbranded zero value afterwards. branding is package-level state (see
// branding.go), so every test that touches it must go through this.
func withBranding(t *testing.T, b brandingConfig) {
	t.Helper()
	prev := branding
	t.Cleanup(func() { branding = prev })
	setBranding(b)
}

// TestLoadTurfConfigParsesBranding confirms the branding: section parses every
// key, and that a project file overriding one key inherits the rest of a global
// brand rather than replacing the section wholesale.
func TestLoadTurfConfigParsesBranding(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("TURF_HOME", home)

	writeFile(t, filepath.Join(home, turfConfigFileName), `
branding:
  theme: surf
  banner: global-banner.txt
  welcome_message: |
    Welcome to Global.
  additional_instructions: |
    Prefer the global backend.
`)
	writeFile(t, filepath.Join(cwd, ".turf", turfConfigFileName), `
branding:
  theme: lunar
`)

	cfg, err := loadTurfConfig(cwd)
	if err != nil {
		t.Fatalf("loadTurfConfig: %v", err)
	}
	b := cfg.Branding
	if b.Theme != "lunar" {
		t.Errorf("theme = %q, want the project override %q", b.Theme, "lunar")
	}
	if !strings.Contains(b.WelcomeMessage, "Welcome to Global.") {
		t.Errorf("welcome_message = %q, want the inherited global text", b.WelcomeMessage)
	}
	if !strings.Contains(b.AdditionalInstructions, "Prefer the global backend.") {
		t.Errorf("additional_instructions = %q, want the inherited global text", b.AdditionalInstructions)
	}
	// The banner path must still resolve against the file that declared it —
	// <TURF_HOME> — even though the project file won on `theme`.
	if want := filepath.Join(home, "global-banner.txt"); b.Banner != want {
		t.Errorf("banner = %q, want %q (resolved against the declaring file's dir)", b.Banner, want)
	}
}

// TestLoadTurfConfigBrandingAbsoluteBanner confirms an absolute banner path is
// left alone rather than being joined onto the config dir.
func TestLoadTurfConfigBrandingAbsoluteBanner(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("TURF_HOME", home)

	abs := filepath.Join(t.TempDir(), "art.txt")
	writeFile(t, filepath.Join(home, turfConfigFileName), "branding:\n  banner: "+abs+"\n")

	cfg, err := loadTurfConfig(cwd)
	if err != nil {
		t.Fatalf("loadTurfConfig: %v", err)
	}
	if cfg.Branding.Banner != abs {
		t.Errorf("banner = %q, want the absolute path %q unchanged", cfg.Branding.Banner, abs)
	}
}

// TestLoadTurfConfigBrandingBannerIsAbsolute confirms the banner path is frozen
// absolute at load time even when TURF_HOME itself is relative. The banner is
// read much later (at TUI construction), so a path left relative would resolve
// against whatever the cwd is by then — and turf chdirs for --chdir and
// --worktree. Anchoring at load time is what makes the read cwd-independent.
func TestLoadTurfConfigBrandingBannerIsAbsolute(t *testing.T) {
	base := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(base); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// A relative TURF_HOME — the shape that used to leave the banner relative.
	t.Setenv("TURF_HOME", "home")
	writeFile(t, filepath.Join(base, "home", turfConfigFileName), "branding:\n  banner: art.txt\n")

	cfg, err := loadTurfConfig(t.TempDir())
	if err != nil {
		t.Fatalf("loadTurfConfig: %v", err)
	}
	if !filepath.IsAbs(cfg.Branding.Banner) {
		t.Fatalf("banner = %q, want an absolute path", cfg.Branding.Banner)
	}
	// Build the expectation from the post-chdir cwd rather than from base: on
	// macOS t.TempDir hands back a symlinked path (/var), while Getwd — and hence
	// filepath.Abs — reports the resolved one (/private/var).
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if want := filepath.Join(cwd, "home", "art.txt"); cfg.Branding.Banner != want {
		t.Errorf("banner = %q, want %q", cfg.Branding.Banner, want)
	}
}

// TestBrandingLeavesNamesAlone pins the deliberate omission: branding has no
// name key, so turf's identity — the app name in the TUI, the agent name on the
// badge, and appName itself (the toolset name that produces the turf_ tool
// prefix the renderers, the /tools grouping, and preApprovedTurfTools all key
// on) — is unaffected by any brand.
func TestBrandingLeavesNamesAlone(t *testing.T) {
	withBranding(t, brandingConfig{Theme: "surf", WelcomeMessage: "Welcome to Taco."})

	if appName != "turf" {
		t.Fatalf("appName = %q, want turf", appName)
	}
	if turfAgentName != appName {
		t.Errorf("turfAgentName = %q, want %q (must match agent.New's name)", turfAgentName, appName)
	}
	if got := appNameWithBranch(""); got != appName {
		t.Errorf("appNameWithBranch(\"\") = %q, want %q", got, appName)
	}
	if got := appNameWithBranch("wt-1"); got != appName+" · wt-1" {
		t.Errorf("appNameWithBranch(\"wt-1\") = %q, want %q", got, appName+" · wt-1")
	}
	for _, name := range preApprovedTurfTools {
		if !strings.HasPrefix(name, appName+"_") {
			t.Errorf("pre-approved tool %q lost its %q prefix", name, appName+"_")
		}
	}
}

// TestBrandWelcomeMessage confirms branding replaces the welcome text but not
// the decision of whether to show one: a caller passing no welcome (up/destroy)
// still gets none.
func TestBrandWelcomeMessage(t *testing.T) {
	withBranding(t, brandingConfig{})
	if got := brandWelcomeMessage("built-in"); got != "built-in" {
		t.Errorf("brandWelcomeMessage = %q, want the caller's default when unbranded", got)
	}

	withBranding(t, brandingConfig{WelcomeMessage: "Welcome to Taco."})
	if got := brandWelcomeMessage("built-in"); got != "Welcome to Taco." {
		t.Errorf("brandWelcomeMessage = %q, want the branded text", got)
	}
	if got := brandWelcomeMessage(""); got != "Welcome to Taco." {
		t.Errorf("brandWelcomeMessage(\"\") = %q, want the branded text", got)
	}
}

// TestBrandInstructions confirms additional_instructions is appended to turf's
// persona (not substituted for it) and lands after it, so the base workflow
// contract is stated first.
func TestBrandInstructions(t *testing.T) {
	withBranding(t, brandingConfig{})
	if got := brandInstructions(); got != workflowInstructions {
		t.Error("unbranded instructions should be exactly workflowInstructions")
	}

	withBranding(t, brandingConfig{AdditionalInstructions: "\n  Gate on policy.\n"})
	got := brandInstructions()
	if !strings.HasPrefix(got, workflowInstructions) {
		t.Error("branded instructions should start with turf's persona intact")
	}
	if !strings.Contains(got, "Gate on policy.") {
		t.Errorf("branded instructions missing the brand text: %q", got)
	}
	if strings.Index(got, "Gate on policy.") < strings.Index(got, "Additional instructions") {
		t.Error("brand text should follow its header")
	}
}

// TestBrandBannerFallsBack confirms a missing, unreadable, or empty banner
// degrades to turf's own wordmark instead of failing the run.
func TestBrandBannerFallsBack(t *testing.T) {
	withBranding(t, brandingConfig{})
	if got := brandBanner(); !slices.Equal(got, turfBanner) {
		t.Error("unbranded banner should be turf's own")
	}

	withBranding(t, brandingConfig{Banner: filepath.Join(t.TempDir(), "missing.txt")})
	if got := brandBanner(); !slices.Equal(got, turfBanner) {
		t.Error("a missing banner file should fall back to turf's own")
	}

	empty := filepath.Join(t.TempDir(), "empty.txt")
	writeFile(t, empty, "\n\n")
	withBranding(t, brandingConfig{Banner: empty})
	if got := brandBanner(); !slices.Equal(got, turfBanner) {
		t.Error("an empty banner file should fall back to turf's own")
	}
}

// TestBrandBannerReadsArt confirms a configured banner is read and split into
// lines, with the trailing newline dropped (matching turfBanner's own shape).
func TestBrandBannerReadsArt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "art.txt")
	writeFile(t, path, "TACO\n=====\n")
	withBranding(t, brandingConfig{Banner: path})

	got := brandBanner()
	if want := []string{"TACO", "====="}; !slices.Equal(got, want) {
		t.Errorf("brandBanner() = %q, want %q", got, want)
	}
}
