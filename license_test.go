package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/turfbuild/turf/release"
)

// TestLicenseCommandPrintsThreeDocuments checks that `turf license` prints
// LICENSE, STANDARD-TERMS.md and NOTICE, each in full, in that order, with a
// rule line between them.
func TestLicenseCommandPrintsThreeDocuments(t *testing.T) {
	cmd := newLicenseCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("turf license: %v", err)
	}
	want := release.License + "\n" + licenseRule + "\n\n" + release.StandardTerms + "\n" + licenseRule + "\n\n" + release.Notice
	if out.String() != want {
		t.Errorf("turf license output is not LICENSE, rule, STANDARD-TERMS.md, rule, NOTICE")
	}
	if !strings.HasPrefix(release.License, "# Turf Community Edition License\n") {
		t.Error("LICENSE does not start with its title")
	}
	if !strings.HasPrefix(release.StandardTerms, "# Common Paper Software License Agreement: Standard Terms, Version 1.1") {
		t.Error("STANDARD-TERMS.md does not start with its title")
	}
	if !strings.Contains(release.License, acceptanceNotice) {
		t.Error("LICENSE does not carry the acceptance notice word for word")
	}
	for _, name := range []string{"turf-mcp-server", "turf-engine", "turf-builtin-provider", "turf-ui", "STANDARD-TERMS.md", "Turf Community Edition License 1.0"} {
		if !strings.Contains(release.Notice, name) {
			t.Errorf("NOTICE does not name %s", name)
		}
	}
}

// TestNoOldLicenseNames: the Noncommercial license is gone from the shipped
// license, the version lines, the notice and every command's help.
func TestNoOldLicenseNames(t *testing.T) {
	texts := map[string]string{
		"release/LICENSE": release.License,
		"release/NOTICE":  release.Notice,
		"licenseLines":    licenseLines,
		"notice":          acceptanceNotice,
	}
	walk(fullTree(), func(c *cobra.Command) {
		texts[c.CommandPath()+" help"] = c.Short + "\n" + c.Long + "\n" + c.LocalFlags().FlagUsages()
	})
	for name, s := range texts {
		for _, bad := range []string{"PolyForm", "Noncommercial", "Free Use Grant"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s mentions %q", name, bad)
			}
		}
	}
}
