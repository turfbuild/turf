package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/goccy/go-yaml"
)

// The examples live in the sibling github.com/turfbuild/turf-examples repo,
// not in this module, so these are integration checks that run only when that
// checkout is present. They guard against the example artifacts drifting out of
// sync with the turf behavior and the cagent (docker-agent) version we pin:
//   - the turf example skill stays discoverable by loadTurfSkills, and
//   - the example turf.yaml model configurations stay valid.
//
// Resolution: $TURF_EXAMPLES_DIR if set, else ../turf-examples relative to this
// module. When neither resolves, the test is skipped rather than failed; once
// the checkout is there, an example missing from where a test looks fails the
// test, since a skip would hide the drift these tests exist to catch.
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

// TestCliExampleSkillDiscovered confirms the turf CLI example's working dir
// (integrations/turf-cli/.turf/skills) yields the demonstration skill through
// the real loader.
func TestCliExampleSkillDiscovered(t *testing.T) {
	cwd := filepath.Join(examplesDir(t), "integrations", "turf-cli")
	if _, err := os.Stat(filepath.Join(cwd, ".turf", "skills")); err != nil {
		t.Fatalf("turf CLI example skills not present: %v", err)
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

// TestCliConfigExamplesValid confirms the example turf.yaml model-configuration
// files (integrations/turf-cli) parse into turfConfig and pass cagent's schema
// validation (e.g. first_available's mutual-exclusion rules), so the gallery
// cannot drift into an invalid state against the pinned docker-agent.
func TestCliConfigExamplesValid(t *testing.T) {
	root := filepath.Join(examplesDir(t), "integrations", "turf-cli")
	if _, err := os.Stat(filepath.Join(root, "gallery")); err != nil {
		t.Fatalf("turf-cli model-config gallery not present: %v", err)
	}

	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		// Both the active .turf/turf.yaml and the gallery/*.turf.yaml configs.
		if filepath.Base(p) == turfConfigFileName || filepath.Ext(p) == ".yaml" {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(files) == 0 {
		t.Fatalf("no example configs found under %s", root)
	}

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		var c turfConfig
		if err := yaml.Unmarshal(data, &c); err != nil {
			t.Errorf("%s: does not parse into turfConfig: %v", f, err)
			continue
		}
		// Validate the reused cagent model/provider schema (first_available rules,
		// auth, compaction thresholds) exactly as a real config load would.
		syn := latest.Config{Models: c.Models, Providers: c.Providers}
		if err := syn.Validate(); err != nil {
			t.Errorf("%s: fails schema validation: %v", f, err)
		}
	}
}
