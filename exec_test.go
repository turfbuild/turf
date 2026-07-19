package main

import "testing"

// exec accepts at most one positional message (a prompt or "-" for stdin);
// additional positionals are a mistake (usually an unquoted multi-word prompt),
// so guard the arg contract.
func TestExecArgs(t *testing.T) {
	cmd := newExecCmd()

	if err := cmd.Args(cmd, nil); err != nil {
		t.Fatalf("exec should accept zero args, got: %v", err)
	}
	if err := cmd.Args(cmd, []string{"create a bucket"}); err != nil {
		t.Fatalf("exec should accept a single message arg, got: %v", err)
	}
	if err := cmd.Args(cmd, []string{"create", "a", "bucket"}); err == nil {
		t.Fatal("exec should reject multiple positional args (unquoted prompt)")
	}
}

// The scripting-facing flags must exist with stable names, since external
// drivers (e.g. an agent exercising turf headlessly) depend on them.
func TestExecFlags(t *testing.T) {
	cmd := newExecCmd()
	for _, name := range []string{"json", "yes", "auto-approve", "hide-tool-calls"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("exec missing --%s flag", name)
		}
	}
}
