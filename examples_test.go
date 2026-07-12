package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker-agent/pkg/config"
)

// The examples live in the sibling github.com/turfbuild/turf-examples repo,
// not in this module, so these are integration checks that run only when that
// checkout is present. They guard against the example artifacts drifting out of
// sync with the turf behavior and the cagent (docker-agent) version we pin:
//   - the turf example skill stays discoverable by loadTurfSkills, and
//   - the cagent example config still parses (e.g. `skills: true` keeps working).
//
// Resolution: $TURF_EXAMPLES_DIR if set, else ../turf-examples relative to this
// module. When neither resolves, the test is skipped rather than failed.
func examplesDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("TURF_EXAMPLES_DIR"); dir != "" {
		if _, err := os.Stat(dir); err != nil {
			t.Skipf("TURF_EXAMPLES_DIR=%q not accessible: %v", dir, err)
		}
		return dir
	}
	// Tests run with the working dir set to the package dir (the module root).
	dir, err := filepath.Abs(filepath.Join("..", "turf-examples"))
	if err != nil {
		t.Skipf("cannot resolve examples dir: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("turf-examples checkout not found at %q (set TURF_EXAMPLES_DIR to override)", dir)
	}
	return dir
}

// TestCliExampleSkillDiscovered confirms the turf example working dir
// (cli/.turf/skills) yields the demonstration skill through the real loader.
func TestCliExampleSkillDiscovered(t *testing.T) {
	cwd := filepath.Join(examplesDir(t), "cli")
	if _, err := os.Stat(cwd); err != nil {
		t.Skipf("cli example not present: %v", err)
	}
	t.Setenv("TURF_HOME", t.TempDir()) // isolate from any real ~/.turf skills

	loaded := loadTurfSkills(cwd)
	if len(loaded) != 1 || loaded[0].Name != "tagging-policy" {
		t.Fatalf("expected one skill 'tagging-policy' from %s/.turf/skills, got %+v", cwd, loaded)
	}
	var hasRef bool
	for _, f := range loaded[0].Files {
		if f == "references/tags.md" {
			hasRef = true
		}
	}
	if !hasRef {
		t.Fatalf("tagging-policy missing references/tags.md in Files: %v", loaded[0].Files)
	}
}

// TestCagentExampleConfigParses confirms cagent's example turf.yaml still loads
// against the pinned docker-agent version, including `skills: true`.
func TestCagentExampleConfigParses(t *testing.T) {
	path := filepath.Join(examplesDir(t), "cagent", "turf.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("cagent example not present: %v", err)
	}

	cfg, err := config.Load(context.Background(), config.NewFileSource(path))
	if err != nil {
		t.Fatalf("cagent config.Load(%s) failed: %v", path, err)
	}

	rootIdx := -1
	for i := range cfg.Agents {
		if cfg.Agents[i].Name == "root" {
			rootIdx = i
		}
	}
	if rootIdx < 0 {
		t.Fatal("no 'root' agent in parsed config")
	}
	if got := cfg.Agents[rootIdx].Skills.Sources; len(got) == 0 {
		t.Fatalf("`skills: true` did not resolve to a local source; Sources=%v", got)
	}
}
