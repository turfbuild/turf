package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// turf aligns with Terraform/OpenTofu on the *path*: `up` and `destroy` operate
// on the current directory (retarget with -C/--chdir), never a positional path.
// But positional args ARE accepted now — they are freeform *instructions* that
// steer the plan (joined into the server prompt's instructions argument), not a
// path. Guard both halves: zero args is fine, and arbitrary positional args are
// accepted rather than rejected.
func TestUpDestroyAcceptInstructionArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  func() *cobra.Command
	}{
		{"up", newUpCmd},
		{"destroy", newDestroyCmd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.cmd()
			if err := cmd.Args(cmd, nil); err != nil {
				t.Fatalf("%s should accept zero args, got: %v", tc.name, err)
			}
			if err := cmd.Args(cmd, []string{"replace", "the", "vpc"}); err != nil {
				t.Fatalf("%s should accept positional instruction args, got: %v", tc.name, err)
			}
		})
	}
}

// TestUpDestroyInstructionsReachArgs verifies how the CLI surfaces freeform
// instructions to the RunE, including the `turf up -- <text>` form: cobra strips
// the -- and forwards the remainder as positional args, which RunE joins into
// the instructions argument. This is what lets instructions that start with a
// dash (e.g. `-target=...`) pass through without being parsed as flags.
func TestUpDestroyInstructionsReachArgs(t *testing.T) {
	cases := []struct {
		name     string
		argv     []string
		wantJoin string
	}{
		{"none", nil, ""},
		{"plain", []string{"replace", "the", "vpc"}, "replace the vpc"},
		{"single quoted", []string{"replace aws_s3_bucket.assets"}, "replace aws_s3_bucket.assets"},
		{"dash dash guards dash-leading text", []string{"--", "-target=module.network", "now"}, "-target=module.network now"},
	}
	for _, ctor := range []struct {
		name string
		cmd  func() *cobra.Command
	}{
		{"up", newUpCmd},
		{"destroy", newDestroyCmd},
	} {
		for _, tc := range cases {
			t.Run(ctor.name+"/"+tc.name, func(t *testing.T) {
				cmd := ctor.cmd()
				var got string
				captured := false
				// Swap in a capturing RunE so we exercise cobra's real arg/`--`
				// parsing without building a runtime.
				cmd.RunE = func(_ *cobra.Command, args []string) error {
					got = strings.Join(args, " ")
					captured = true
					return nil
				}
				cmd.SetArgs(tc.argv)
				if err := cmd.Execute(); err != nil {
					t.Fatalf("execute %v: %v", tc.argv, err)
				}
				if !captured {
					t.Fatalf("RunE did not run for %v", tc.argv)
				}
				if got != tc.wantJoin {
					t.Errorf("joined instructions = %q, want %q", got, tc.wantJoin)
				}
			})
		}
	}
}
