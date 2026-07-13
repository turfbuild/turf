package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// turf aligns with Terraform/OpenTofu: `up` and `destroy` operate on the
// current directory (retarget with -C/--chdir), and reject a positional path
// argument the way `tofu plan ./dir` does. Guard that contract so a stray
// cobra.MaximumNArgs can't silently reintroduce the non-Terraform positional.
func TestUpDestroyRejectPositionalArg(t *testing.T) {
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
			if err := cmd.Args(cmd, []string{"./somedir"}); err == nil {
				t.Fatalf("%s should reject a positional path argument", tc.name)
			}
		})
	}
}
