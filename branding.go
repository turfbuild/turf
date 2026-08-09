package main

import (
	"log/slog"
	"os"
	"strings"
)

// branding holds the co-branding overlay for the run, published once by
// createAgentRuntime from the merged turf.yaml (see setBranding). It is a
// package-level value rather than plumbing because the brand surfaces are
// scattered across layers turf does not otherwise thread config through — the
// process-global theme engine (theme.go) and the TUI constructors (tui.go) —
// and because the value is read-only after startup.
//
// The zero value is the unbranded turf: every accessor below falls back to
// turf's own built-in identity, so a user with no branding: section pays nothing
// and sees exactly what they saw before.
var branding brandingConfig

// setBranding publishes the merged turf.yaml branding for the rest of the run.
// It must be called before applyTurfTheme and before either TUI is constructed;
// createAgentRuntime does so right after loading the config, which the run
// commands call before runTUI (see chat.go / up.go / destroy.go).
func setBranding(b brandingConfig) {
	branding = b
}

// Note there is no brandName: turf's name is not brandable. The TUI status bar,
// the lean status footer, the headless printer, and the agent badge all keep
// saying "turf" (appName) under any brand — so a co-branded turf still announces
// itself as turf. That also keeps appName singular: it is the name the MCP
// toolset is registered under, hence the source of the "turf_" tool prefix the
// renderers, the /tools grouping, and preApprovedTurfTools all key on.

// brandThemeRef is the co-brand's default theme ref, or "" when unbranded. It is
// only a default — applyTurfTheme still lets a saved /theme choice and --theme
// win over it.
func brandThemeRef() string {
	return branding.Theme
}

// brandInstructions returns the agent's system instructions: turf's persona,
// plus the co-brand's additional_instructions appended under a header. cagent
// contributes a single Instruction() system message (see session.GetMessages),
// so concatenation is the mechanism. The brand text goes last so turf's base
// workflow contract — including the confirmation discipline — is stated first.
func brandInstructions() string {
	if branding.AdditionalInstructions == "" {
		return workflowInstructions
	}
	return workflowInstructions +
		"\n\n## Additional instructions\n\n" +
		strings.TrimSpace(branding.AdditionalInstructions)
}

// brandWelcomeMessage returns the welcome text to show in place of turf's
// built-in one. It takes the caller's default when unbranded, so the choice of
// *whether* a command shows a welcome at all stays with the caller (chat does,
// up/destroy do not).
func brandWelcomeMessage(def string) string {
	if branding.WelcomeMessage != "" {
		return branding.WelcomeMessage
	}
	return def
}

// brandBanner is the lean TUI's welcome banner: the co-brand's art file when one
// is configured and readable, else turf's own embedded wordmark.
//
// Never hard-fails — a broken banner path degrades to the turf banner with a
// warning, the same posture as applyTurfTheme's theme fallback. The file is
// split into lines exactly like turfBanner, and the same rendering constraints
// apply: leantui wraps each line in a lipgloss Foreground style, so truecolor
// art must be switch-only (no mid-line SGR reset) to survive the wrapper, and
// lines should stay within ~56 columns.
func brandBanner() []string {
	if branding.Banner == "" {
		return turfBanner
	}
	data, err := os.ReadFile(branding.Banner)
	if err != nil {
		slog.Warn("turf: could not read branding banner, using turf's own", "path", branding.Banner, "error", err)
		return turfBanner
	}
	art := strings.TrimRight(string(data), "\n")
	if art == "" {
		slog.Warn("turf: branding banner is empty, using turf's own", "path", branding.Banner)
		return turfBanner
	}
	return strings.Split(art, "\n")
}
