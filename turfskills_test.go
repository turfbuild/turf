package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillstool "github.com/docker/docker-agent/pkg/tools/builtin/skills"
)

// writeSkill creates <root>/skills/<name>/SKILL.md (and optional extra files
// keyed by relative path) for a global skill, or pass the project base for a
// project skill via the dir argument.
func writeSkill(t *testing.T, skillsDir, name, body string, extra map[string]string) {
	t.Helper()
	dir := filepath.Join(skillsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, content := range extra {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadTurfSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TURF_HOME", home)
	globalSkills := filepath.Join(home, "skills")

	cwd := t.TempDir()
	projectSkills := filepath.Join(cwd, ".turf", "skills")

	// Global skill: enterprise best practice, with a supporting reference file.
	writeSkill(t, globalSkills, "tagging-policy", `---
name: tagging-policy
description: Apply the org-wide resource tagging and naming policy on every create.
---

# Tagging policy

Every resource gets cost-center and owner tags. Details: read_skill_file references/tags.md.
`, map[string]string{"references/tags.md": "cost-center, owner, env"})

	// Global skill that the project will override by name.
	writeSkill(t, globalSkills, "prod-deploy", `---
name: prod-deploy
description: GLOBAL default prod deploy procedure.
---
# global
`, nil)

	// Project skill overriding the global prod-deploy, plus a fork skill.
	writeSkill(t, projectSkills, "prod-deploy", `---
name: prod-deploy
description: PROJECT prod deploy procedure (overrides global).
---
# project
`, nil)
	writeSkill(t, projectSkills, "azure-migration", `---
name: azure-migration
description: Adopt hand-built Azure resources into managed state.
context: fork
---
# migration
`, nil)

	loaded := loadTurfSkills(cwd)

	byName := map[string]int{}
	for i, sk := range loaded {
		byName[sk.Name] = i
	}

	// All three distinct skills present (prod-deploy deduped to one).
	for _, want := range []string{"tagging-policy", "prod-deploy", "azure-migration"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("skill %q not loaded; got %v", want, byName)
		}
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 skills, got %d (%v)", len(loaded), byName)
	}

	// Project skill overrides the global one of the same name.
	prod := loaded[byName["prod-deploy"]]
	if !strings.Contains(prod.Description, "PROJECT") {
		t.Fatalf("prod-deploy not overridden by project; desc = %q", prod.Description)
	}
	if !strings.HasPrefix(prod.BaseDir, cwd) {
		t.Fatalf("prod-deploy should resolve under cwd, got BaseDir %q", prod.BaseDir)
	}

	// Supporting files are captured so read_skill_file is usable.
	tagging := loaded[byName["tagging-policy"]]
	var hasRef bool
	for _, f := range tagging.Files {
		if f == "references/tags.md" {
			hasRef = true
		}
	}
	if !hasRef {
		t.Fatalf("tagging-policy missing references/tags.md in Files: %v", tagging.Files)
	}

	// context: fork carried through.
	if mig := loaded[byName["azure-migration"]]; !mig.IsFork() {
		t.Fatalf("azure-migration should be a fork skill, Context = %q", mig.Context)
	}

	// Tour: show exactly what the model receives + which tools light up.
	ts := skillstool.New(loaded, cwd)
	instr := ts.Instructions()
	for _, want := range []string{"<available_skills>", "tagging-policy", "azure-migration", "read_skill_file", "run_skill"} {
		if !strings.Contains(instr, want) {
			t.Errorf("Instructions() missing %q", want)
		}
	}
	t.Logf("read_skill toolset Instructions() the agent sees:\n%s", instr)
}

// TestLoadTurfSkills_IgnoresNonTurfLocations confirms ~/.claude-style dirs are
// NOT scanned: a skill placed in <cwd>/.claude/skills must not be discovered.
func TestLoadTurfSkills_IgnoresNonTurfLocations(t *testing.T) {
	t.Setenv("TURF_HOME", t.TempDir())
	cwd := t.TempDir()
	writeSkill(t, filepath.Join(cwd, ".claude", "skills"), "stray", `---
name: stray
description: should not be discovered by turf.
---
# stray
`, nil)

	if loaded := loadTurfSkills(cwd); len(loaded) != 0 {
		t.Fatalf("expected no skills from .claude/skills, got %d", len(loaded))
	}
}
