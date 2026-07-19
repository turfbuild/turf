package main

import "testing"

// exec accepts any number of positionals (including zero, and everything after
// a "--" terminator); the words are joined into one message, so quoting is
// optional and `exec -- <prompt>` works. Guard that the arg validator stays
// permissive.
func TestExecArgs(t *testing.T) {
	cmd := newExecCmd()

	for _, args := range [][]string{
		nil,
		{"create a bucket"},
		{"create", "a", "bucket"},    // unquoted multi-word
		{"--dry-run", "the", "plan"}, // as delivered after `exec -- ...`
		{"-"},
	} {
		if err := cmd.Args(cmd, args); err != nil {
			t.Errorf("exec should accept args %q, got: %v", args, err)
		}
	}
}

func TestExecMessageJoining(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"none", nil, nil},
		{"single quoted", []string{"create a bucket"}, []string{"create a bucket"}},
		{"unquoted words", []string{"create", "a", "bucket"}, []string{"create a bucket"}},
		{"dash-prefixed after --", []string{"--dry-run", "plan"}, []string{"--dry-run plan"}},
		{"stdin sentinel", []string{"-"}, []string{"-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := execMessages(tc.args)
			if len(got) != len(tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %q, want %q", got, tc.want)
				}
			}
		})
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
