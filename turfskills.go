package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/docker/docker-agent/pkg/skills"
)

// turf discovers user-authored skills from turf-owned locations only — never
// the agent-ecosystem defaults (~/.claude, ~/.codex, ~/.agents) that cagent's
// own skills.Load("local") scans. A skill is a directory holding a SKILL.md
// with YAML frontmatter declaring at least `name` and `description`; supporting
// files (e.g. references/*.md) are loaded on demand via read_skill_file.
const (
	turfSkillsSubdir = "skills"
	skillFileName    = "SKILL.md"
)

// loadTurfSkills scans turf's two skill locations, in increasing precedence
// (later wins on a name collision):
//
//  1. <TURF_HOME>/skills/<name>/SKILL.md  — per-user global skills (org best
//     practices, migration playbooks, custom procedures).
//  2. <cwd>/.turf/skills/<name>/SKILL.md  — project skills, versioned with the
//     infra config; they override a global skill of the same name.
//
// The working-dir location is exactly <cwd>/.turf/skills — there is no walk up
// the directory tree, so discovery is predictable from where turf is launched.
func loadTurfSkills(cwd string) []skills.Skill {
	byName := map[string]skills.Skill{}
	for _, root := range []string{
		filepath.Join(turfHome(), turfSkillsSubdir),
		filepath.Join(cwd, ".turf", turfSkillsSubdir),
	} {
		for _, sk := range loadSkillsFromDir(root) {
			byName[sk.Name] = sk
		}
	}

	out := make([]skills.Skill, 0, len(byName))
	for _, sk := range byName {
		out = append(out, sk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// loadSkillsFromDir loads every skill in the immediate subdirectories of dir.
// A missing dir is not an error (it just yields no skills).
func loadSkillsFromDir(dir string) []skills.Skill {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var out []skills.Skill
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if sk, ok := parseTurfSkill(filepath.Join(dir, e.Name(), skillFileName), e.Name()); ok {
			out = append(out, sk)
		}
	}
	return out
}

// parseTurfSkill reads and parses a single SKILL.md. dirName supplies the skill
// name when the frontmatter omits it. A skill without a name or description is
// skipped (returns ok=false), matching cagent's loader.
func parseTurfSkill(path, dirName string) (skills.Skill, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return skills.Skill{}, false
	}
	fm, ok := parseSkillFrontmatter(string(data))
	if !ok {
		return skills.Skill{}, false
	}

	name := fm["name"]
	if name == "" {
		name = dirName
	}
	desc := fm["description"]
	if name == "" || desc == "" {
		return skills.Skill{}, false
	}

	base := filepath.Dir(path)
	return skills.Skill{
		Name:        name,
		Description: desc,
		FilePath:    path,
		BaseDir:     base,
		Local:       true,
		// context: fork runs the skill as an isolated sub-agent (run_skill);
		// model overrides the model for that fork. Both are optional.
		Context: fm["context"],
		Model:   fm["model"],
		// Populate Files so the toolset exposes read_skill_file and lists the
		// supporting files in <available_skills>. cagent's own local loader
		// leaves this empty, which suppresses read_skill_file entirely.
		Files: listSkillFiles(base),
	}, true
}

// parseSkillFrontmatter extracts top-level scalar `key: value` pairs from a
// SKILL.md's leading ---...--- block. It mirrors cagent's tolerant parser
// (split on the first ": ", strip surrounding quotes) for the scalar keys turf
// uses. Indented/block values (metadata, list-form allowed-tools) are ignored.
func parseSkillFrontmatter(content string) (map[string]string, bool) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---") {
		return nil, false
	}
	end := strings.Index(content[3:], "\n---")
	if end == -1 {
		return nil, false
	}
	block := content[4 : end+3]

	fm := map[string]string{}
	for line := range strings.SplitSeq(block, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue // blanks and indented (block) continuation lines
		}
		key, val, found := strings.Cut(line, ": ")
		if !found {
			continue
		}
		fm[strings.TrimSpace(key)] = unquoteScalar(strings.TrimSpace(val))
	}
	return fm, true
}

// listSkillFiles returns every regular file under base as a slash-separated
// path relative to base (including SKILL.md itself), sorted for determinism.
func listSkillFiles(base string) []string {
	var files []string
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if rel, err := filepath.Rel(base, p); err == nil {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(files)
	return files
}

// unquoteScalar strips a single matching pair of surrounding quotes.
func unquoteScalar(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
