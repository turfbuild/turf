package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// TestMain accepts the terms for the whole package and points TURF_HOME at a
// temporary directory, so no test reads or writes a developer's real ~/.turf
// and every turf-mcp-server a test starts inherits the acceptance. Tests of
// the not-accepted path clear the variable themselves (clearAcceptance).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "turf-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv("TURF_HOME", home)
	_ = os.Setenv(acceptEnv, "yes")
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

// clearAcceptance gives the test a fresh TURF_HOME with no marker, no
// TURF_ACCEPT_EULA and no --accept-eula, and returns the marker path.
func clearAcceptance(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("TURF_HOME", home)
	t.Setenv(acceptEnv, "")
	if err := os.Unsetenv(acceptEnv); err != nil {
		t.Fatal(err)
	}
	old := flagAcceptEULA
	flagAcceptEULA = false
	t.Cleanup(func() { flagAcceptEULA = old })
	return filepath.Join(home, markerFileName)
}

// withTerminal replaces the terminal test and the answer's reader.
func withTerminal(t *testing.T, terminal bool, answer string) {
	t.Helper()
	oldT, oldIn := acceptanceTerminals, acceptanceStdin
	acceptanceTerminals = func() bool { return terminal }
	acceptanceStdin = strings.NewReader(answer)
	t.Cleanup(func() { acceptanceTerminals, acceptanceStdin = oldT, oldIn })
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func validMarkerDoc() map[string]any {
	return map[string]any{
		"terms":         "turf-community-edition",
		"terms_version": "1.0",
		"terms_url":     "https://turf.build/license/v1/",
		"accepted_at":   "2026-11-23T10:00:00Z",
		"method":        "command",
		"accepted_with": "turf 1.5.0",
		"previous":      []any{},
	}
}

func markerWith(changes map[string]any) map[string]any {
	d := validMarkerDoc()
	for k, v := range changes {
		if v == nil {
			delete(d, k)
		} else {
			d[k] = v
		}
	}
	return d
}

// TestResolveAcceptance is the contract's acceptance rule as a table:
// precedence flag > variable > marker, the variable's one accepting value,
// and every way a marker fails to be acceptance.
func TestResolveAcceptance(t *testing.T) {
	type env struct {
		val string
		set bool
	}
	cases := []struct {
		name       string
		flag       bool
		env        env
		marker     any // nil: no file; string: raw bytes; map: JSON
		wantOK     bool
		wantMethod string
		wantEnvBad bool
		wantMarker string // substring of markerProblem
	}{
		{name: "nothing", wantOK: false},
		{name: "flag", flag: true, wantOK: true, wantMethod: "flag"},
		{name: "flag beats env", flag: true, env: env{"yes", true}, wantOK: true, wantMethod: "flag"},
		{name: "flag beats marker", flag: true, marker: validMarkerDoc(), wantOK: true, wantMethod: "flag"},
		{name: "env yes", env: env{"yes", true}, wantOK: true, wantMethod: "env"},
		{name: "env YES padded", env: env{" YES ", true}, wantOK: true, wantMethod: "env"},
		{name: "env Yes", env: env{"Yes", true}, wantOK: true, wantMethod: "env"},
		{name: "env beats marker", env: env{"yes", true}, marker: validMarkerDoc(), wantOK: true, wantMethod: "env"},
		{name: "env true", env: env{"true", true}, wantOK: false, wantEnvBad: true},
		{name: "env 1", env: env{"1", true}, wantOK: false, wantEnvBad: true},
		{name: "env y", env: env{"y", true}, wantOK: false, wantEnvBad: true},
		{name: "env empty", env: env{"", true}, wantOK: false, wantEnvBad: true},
		{name: "env wrong but marker valid", env: env{"true", true}, marker: validMarkerDoc(), wantOK: true, wantMethod: "command", wantEnvBad: true},
		{name: "marker command", marker: validMarkerDoc(), wantOK: true, wantMethod: "command"},
		{name: "marker prompt", marker: markerWith(map[string]any{"method": "prompt"}), wantOK: true, wantMethod: "prompt"},
		{name: "marker newer version", marker: markerWith(map[string]any{"terms_version": "2.3"}), wantOK: true, wantMethod: "command"},
		{name: "marker unknown keys", marker: markerWith(map[string]any{"x": 1}), wantOK: true, wantMethod: "command"},
		{name: "marker corrupt", marker: "{not json", wantMarker: "not valid JSON"},
		{name: "marker not object", marker: "[1]", wantMarker: "not a JSON object"},
		{name: "marker wrong name", marker: markerWith(map[string]any{"terms": "polyform"}), wantMarker: "terms is"},
		{name: "marker no name", marker: markerWith(map[string]any{"terms": nil}), wantMarker: "terms is"},
		{name: "marker below minimum", marker: markerWith(map[string]any{"terms_version": "0.9"}), wantMarker: "below the minimum"},
		{name: "marker version not MAJOR.MINOR", marker: markerWith(map[string]any{"terms_version": "1"}), wantMarker: "not MAJOR.MINOR"},
		{name: "marker version three parts", marker: markerWith(map[string]any{"terms_version": "1.0.0"}), wantMarker: "not MAJOR.MINOR"},
		{name: "marker bad time", marker: markerWith(map[string]any{"accepted_at": "yesterday"}), wantMarker: "RFC 3339"},
		{name: "marker bad method env", marker: markerWith(map[string]any{"method": "env"}), wantMarker: "method"},
		{name: "marker bad method flag", marker: markerWith(map[string]any{"method": "flag"}), wantMarker: "method"},
		{name: "marker no method", marker: markerWith(map[string]any{"method": nil}), wantMarker: "method"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), markerFileName)
			switch m := tc.marker.(type) {
			case string:
				if err := os.WriteFile(path, []byte(m), 0o600); err != nil {
					t.Fatal(err)
				}
			case map[string]any:
				writeJSONFile(t, path, m)
			}
			a := resolveAcceptance(tc.flag, tc.env.val, tc.env.set, path)
			if a.accepted != tc.wantOK {
				t.Fatalf("accepted = %v, want %v (%+v)", a.accepted, tc.wantOK, a)
			}
			if tc.wantOK && a.method != tc.wantMethod {
				t.Errorf("method = %q, want %q", a.method, tc.wantMethod)
			}
			if (a.envProblem != "") != tc.wantEnvBad {
				t.Errorf("envProblem = %q, want set=%v", a.envProblem, tc.wantEnvBad)
			}
			if tc.wantEnvBad && !strings.Contains(a.envProblem, fmt.Sprintf("%q", tc.env.val)) {
				t.Errorf("envProblem %q does not name the value %q", a.envProblem, tc.env.val)
			}
			if tc.wantMarker != "" && !strings.Contains(a.markerProblem, tc.wantMarker) {
				t.Errorf("markerProblem = %q, want it to contain %q", a.markerProblem, tc.wantMarker)
			}
			if tc.marker == nil && !a.markerMissing {
				t.Errorf("markerMissing = false with no file")
			}
		})
	}
}

// TestContractStrings is the golden test of the strings the contract fixes.
func TestContractStrings(t *testing.T) {
	const notice = "Turf is proprietary software. To accept the Turf license terms (version 1.0,\n" +
		"https://turf.build/license/v1/), run `turf license accept` or set TURF_ACCEPT_EULA=yes.\n" +
		"Free for individuals, education and small businesses within the Community limits.\n" +
		"Government and larger organizations need a commercial license.\n"
	if acceptanceNotice != notice {
		t.Errorf("acceptanceNotice =\n%s\nwant\n%s", acceptanceNotice, notice)
	}
	const lines = "License: Turf Community Edition License 1.0 — proprietary; free within the Community limits\n" +
		"https://turf.build/license/v1/\n"
	if licenseLines != lines {
		t.Errorf("licenseLines =\n%s\nwant\n%s", licenseLines, lines)
	}
	for _, c := range [][2]string{
		{termsName, "turf-community-edition"},
		{termsVersion, "1.0"},
		{termsMinVersion, "1.0"},
		{termsURL, "https://turf.build/license/v1/"},
		{acceptEnv, "TURF_ACCEPT_EULA"},
		{acceptFlag, "accept-eula"},
		{markerFileName, "license.json"},
		{acceptPrompt, "Type yes to accept: "},
	} {
		if c[0] != c[1] {
			t.Errorf("constant %q, want %q", c[0], c[1])
		}
	}
	if notAcceptedExitCode != 77 {
		t.Errorf("notAcceptedExitCode = %d, want 77", notAcceptedExitCode)
	}
}

// fullTree is the root with cobra's own help and completion commands added,
// as Execute adds them.
func fullTree() *cobra.Command {
	root := newRootCmd()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	return root
}

func walk(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, sub := range c.Commands() {
		walk(sub, fn)
	}
}

func cmdPath(c *cobra.Command) []string {
	var p []string
	for ; c.HasParent(); c = c.Parent() {
		p = append([]string{c.Name()}, p...)
	}
	return p
}

// TestEveryCommandExemptOrGated walks the whole command tree: each command is
// either exempt by the rule (it sits under an exempt command directly below
// root) or gated, and a gated one really stops at the check, with code 77,
// when run unaccepted and off a terminal. A command with its own
// PersistentPreRun would shadow the root's check, so none may have one.
func TestEveryCommandExemptOrGated(t *testing.T) {
	root := fullTree()
	var gated, exempt []string
	walk(root, func(c *cobra.Command) {
		if c != root && (c.PersistentPreRun != nil || c.PersistentPreRunE != nil) {
			t.Errorf("%s has its own PersistentPreRun, which would skip the acceptance check", c.CommandPath())
		}
		top := c
		for top.HasParent() && top.Parent().HasParent() {
			top = top.Parent()
		}
		wantExempt := top != root && acceptanceExemptCommands[top.Name()]
		if acceptanceExempt(c) != wantExempt {
			t.Errorf("%s: exempt = %v, want %v", c.CommandPath(), acceptanceExempt(c), wantExempt)
		}
		if wantExempt {
			exempt = append(exempt, c.CommandPath())
		} else {
			gated = append(gated, c.CommandPath())
		}
	})
	for _, name := range []string{"turf", "turf up", "turf destroy", "turf chat", "turf exec", "turf login", "turf logout", "turf console"} {
		if !contains(gated, name) {
			t.Errorf("%s is not gated (gated: %v)", name, gated)
		}
	}
	for _, name := range []string{"turf version", "turf license", "turf license accept", "turf license status", "turf telemetry", "turf telemetry off", "turf help", "turf completion", "turf completion zsh"} {
		if !contains(exempt, name) {
			t.Errorf("%s is not exempt (exempt: %v)", name, exempt)
		}
	}

	// Run every gated, runnable command unaccepted, off a terminal.
	walk(fullTree(), func(c *cobra.Command) {
		if acceptanceExempt(c) || !c.Runnable() || c.Hidden {
			return
		}
		t.Run(strings.Join(append([]string{"turf"}, cmdPath(c)...), " "), func(t *testing.T) {
			clearAcceptance(t)
			withTerminal(t, false, "")
			args := cmdPath(c)
			if c.ValidateArgs(nil) != nil {
				args = append(args, "example.com")
			}
			root := newRootCmd()
			var stderr bytes.Buffer
			root.SetErr(&stderr)
			root.SetOut(&stderr)
			root.SetArgs(args)
			err := root.Execute()
			var ee *exitError
			if !errors.As(err, &ee) || ee.code != 77 {
				t.Fatalf("turf %v: err = %v, want exit 77", args, err)
			}
			if !strings.HasPrefix(ee.msg, acceptanceNotice) {
				t.Errorf("message does not start with the notice:\n%s", ee.msg)
			}
		})
	})
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestExemptCommandsRunUnaccepted: version, license and its subcommands, and
// telemetry run with nothing accepted, and --version on root does too.
func TestExemptCommandsRunUnaccepted(t *testing.T) {
	for _, args := range [][]string{
		{"--version"}, {"version"}, {"license"}, {"license", "status"}, {"telemetry", "status"}, {"help"}, {"up", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			clearAcceptance(t)
			withTerminal(t, false, "")
			root := newRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("turf %v: %v", args, err)
			}
		})
	}
}

// TestNotTerminalExits77: off a terminal the check never asks; it returns the
// notice and code 77, and names a TURF_ACCEPT_EULA value that does not
// accept on a line of its own.
func TestNotTerminalExits77(t *testing.T) {
	clearAcceptance(t)
	withTerminal(t, false, "yes\n") // an answer waiting on stdin is never read
	t.Setenv(acceptEnv, "true")
	err := requireAcceptance(upCmdOf(t))
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != 77 {
		t.Fatalf("err = %v, want exit 77", err)
	}
	want := acceptanceNotice + `TURF_ACCEPT_EULA is set to "true", which does not accept the terms: only yes does.` + "\n"
	if ee.msg != want {
		t.Errorf("message =\n%s\nwant\n%s", ee.msg, want)
	}
	if _, err := os.Stat(markerPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a marker was written: %v", err)
	}
}

// TestCIDoesNotPrompt: on a terminal with CI set, no question either.
func TestCIDoesNotPrompt(t *testing.T) {
	clearAcceptance(t)
	withTerminal(t, true, "yes\n")
	t.Setenv("CI", "true")
	var ee *exitError
	if err := requireAcceptance(upCmdOf(t)); !errors.As(err, &ee) || ee.code != 77 {
		t.Fatalf("err = %v, want exit 77", err)
	}
}

func upCmdOf(t *testing.T) *cobra.Command {
	t.Helper()
	root := newRootCmd()
	c, _, err := root.Find([]string{"up"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestPromptAnswers drives the first-run question with an injected terminal
// and reader.
func TestPromptAnswers(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		accept bool
	}{
		{"yes", "yes\n", true},
		{"YES padded", "  YES \n", true},
		{"yes at EOF without newline", "yes", true},
		{"no", "no\n", false},
		{"y", "y\n", false},
		{"empty line", "\n", false},
		{"EOF", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := clearAcceptance(t)
			t.Setenv("CI", "")
			withTerminal(t, true, tc.answer)
			c := upCmdOf(t)
			var stderr bytes.Buffer
			c.SetErr(&stderr)
			err := requireAcceptance(c)
			if !strings.HasPrefix(stderr.String(), acceptanceNotice+"\n"+acceptPrompt) {
				t.Errorf("stderr does not start with the notice and the question:\n%s", stderr.String())
			}
			if tc.accept {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				rec, _, rerr := readMarker(path)
				if rerr != nil {
					t.Fatalf("marker: %v", rerr)
				}
				if rec.Method != "prompt" {
					t.Errorf("method = %q, want prompt", rec.Method)
				}
				if !strings.Contains(stderr.String(), "Accepted the Turf Community Edition License 1.0 (https://turf.build/license/v1/) on ") {
					t.Errorf("no confirmation:\n%s", stderr.String())
				}
				return
			}
			var ee *exitError
			if !errors.As(err, &ee) || ee.code != 77 {
				t.Fatalf("err = %v, want exit 77", err)
			}
			if _, serr := os.Stat(path); !errors.Is(serr, os.ErrNotExist) {
				t.Errorf("a marker was written on %q", tc.answer)
			}
		})
	}
}

// TestFlagSetsEnvForServer: --accept-eula passes the acceptance on to the
// server through the process environment.
func TestFlagSetsEnvForServer(t *testing.T) {
	clearAcceptance(t)
	withTerminal(t, false, "")
	c := upCmdOf(t) // newRootCmd resets the flag to its default
	flagAcceptEULA = true
	if err := requireAcceptance(c); err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := os.Getenv(acceptEnv); got != "yes" {
		t.Errorf("%s = %q after --accept-eula, want yes", acceptEnv, got)
	}
}

// TestMarkerRoundTrip: the written marker is valid, mode 0600, keeps unknown
// keys, and a second acceptance moves the first into previous.
func TestMarkerRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(dir, markerFileName)
	t1 := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	if _, err := writeMarker(path, methodPrompt, t1); err != nil {
		t.Fatal(err)
	}
	rec, doc, err := readMarker(path)
	if err != nil {
		t.Fatalf("readMarker: %v", err)
	}
	if rec.Terms != termsName || rec.TermsVersion != "1.0" || rec.TermsURL != termsURL || rec.Method != "prompt" ||
		!rec.AcceptedAt.Equal(t1) || rec.AcceptedWith != "turf "+Version {
		t.Errorf("record = %+v", rec)
	}
	if prev, ok := doc["previous"].([]any); !ok || len(prev) != 0 {
		t.Errorf("previous = %#v, want []", doc["previous"])
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
		}
		di, _ := os.Stat(dir)
		if di.Mode().Perm() != 0o700 {
			t.Errorf("dir mode = %v, want 0700", di.Mode().Perm())
		}
	}

	doc["custom"] = "kept"
	writeJSONFile(t, path, doc)
	t2 := t1.Add(time.Hour)
	if _, err := writeMarker(path, methodCommand, t2); err != nil {
		t.Fatal(err)
	}
	rec, doc, err = readMarker(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Method != "command" || !rec.AcceptedAt.Equal(t2) {
		t.Errorf("record after re-accept = %+v", rec)
	}
	if doc["custom"] != "kept" {
		t.Errorf("unknown key lost: %#v", doc)
	}
	prev, _ := doc["previous"].([]any)
	if len(prev) != 1 {
		t.Fatalf("previous = %#v, want one record", doc["previous"])
	}
	p0, _ := prev[0].(map[string]any)
	if p0["method"] != "prompt" || p0["accepted_at"] != "2026-11-23T10:00:00Z" || p0["terms"] != termsName {
		t.Errorf("previous[0] = %#v", p0)
	}
	if _, nested := p0["previous"]; nested {
		t.Errorf("previous[0] carries its own previous: %#v", p0)
	}

	// A corrupt file is replaced, not an error.
	if err := os.WriteFile(path, []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeMarker(path, methodCommand, t2); err != nil {
		t.Fatalf("over a corrupt file: %v", err)
	}
	if _, _, err := readMarker(path); err != nil {
		t.Errorf("after replacing a corrupt file: %v", err)
	}
}

// TestLicenseAcceptCommand: the command writes the marker with method
// command, says so in the contract's words, and the check then passes.
func TestLicenseAcceptCommand(t *testing.T) {
	path := clearAcceptance(t)
	withTerminal(t, false, "")
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"license", "accept"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "Accepted the Turf Community Edition License 1.0 (https://turf.build/license/v1/) on ") ||
		!strings.Contains(out.String(), "; recorded in "+path+". A waiting engine service starts within a few seconds.\n") {
		t.Errorf("output:\n%s", out.String())
	}
	rec, _, err := readMarker(path)
	if err != nil || rec.Method != "command" {
		t.Fatalf("marker: %+v, %v", rec, err)
	}
	if err := requireAcceptance(upCmdOf(t)); err != nil {
		t.Errorf("after turf license accept: %v", err)
	}
	if !strings.HasPrefix(termsVersionLine(), "Terms: accepted, version 1.0 (command, ") {
		t.Errorf("termsVersionLine = %q", termsVersionLine())
	}
}

// TestLicenseStatus: accepted or not, status reports and exits 0.
func TestLicenseStatus(t *testing.T) {
	path := clearAcceptance(t)
	run := func() string {
		root := newRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetArgs([]string{"license", "status"})
		if err := root.Execute(); err != nil {
			t.Fatalf("status: %v", err)
		}
		return out.String()
	}
	if s := run(); !strings.Contains(s, "Terms:    not accepted") || !strings.Contains(s, "no acceptance recorded in "+path) {
		t.Errorf("unaccepted status:\n%s", s)
	}
	t.Setenv(acceptEnv, "1")
	if s := run(); !strings.Contains(s, `TURF_ACCEPT_EULA is set to "1"`) {
		t.Errorf("status does not name the wrong value:\n%s", s)
	}
	t.Setenv(acceptEnv, "yes")
	if s := run(); !strings.Contains(s, "Method:   env") || !strings.Contains(s, "Homebrew engine service reads") {
		t.Errorf("env status:\n%s", s)
	}
	_ = os.Unsetenv(acceptEnv)
	if _, err := writeMarker(path, methodCommand, time.Now()); err != nil {
		t.Fatal(err)
	}
	if s := run(); !strings.Contains(s, "Terms:    accepted") || !strings.Contains(s, "Method:   command") || !strings.Contains(s, "From:     "+path) {
		t.Errorf("accepted status:\n%s", s)
	}
}

// TestVersionShowsTerms: --version prints the licence lines and the
// acceptance state, and --accept-eula reaches that line.
func TestVersionShowsTerms(t *testing.T) {
	clearAcceptance(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--version"}, "Terms: not accepted (turf license accept, or TURF_ACCEPT_EULA=yes)\n"},
		{[]string{"--accept-eula", "--version"}, "Terms: accepted by --accept-eula\n"},
	} {
		flagAcceptEULA = false
		root := newRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetArgs(tc.args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "\n"+licenseLines+tc.want) {
			t.Errorf("turf %v:\n%s\nwant the licence lines then %q", tc.args, out.String(), tc.want)
		}
	}
}

// TestServerLicenseLineMatches is the cross-repository contract check: a
// turf-mcp-server on PATH that already prints the new licence line in
// --version must print the CLI's two lines. An older server is skipped.
func TestServerLicenseLineMatches(t *testing.T) {
	path, err := exec.LookPath("turf-mcp-server")
	if err != nil {
		t.Skip("turf-mcp-server not on PATH")
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		t.Skipf("%s --version: %v", path, err)
	}
	const prefix = "License: Turf Community Edition License"
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			got := l + "\n"
			if i+1 < len(lines) {
				got += lines[i+1] + "\n"
			}
			if got != licenseLines {
				t.Errorf("%s --version licence lines:\n%s\nthe CLI's:\n%s", path, got, licenseLines)
			}
			return
		}
	}
	t.Skipf("%s predates the Turf Community Edition License (no %q line)", path, prefix)
}
