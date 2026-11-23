package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// Licence acceptance. From release 1.5 the release archive is under the Turf
// Community Edition License, accepted by an explicit act: `turf license
// accept`, TURF_ACCEPT_EULA=yes, --accept-eula, or yes to the first-run
// question. The CLI's own source stays MPL-2.0. turf-mcp-server and the engine
// implement the same contract in their own code; this file is the CLI's copy,
// and the public statement of it is docs/license-acceptance.md. The strings
// below are fixed by that contract: change them only together with it.

const (
	termsName       = "turf-community-edition"
	termsTitle      = "Turf Community Edition License"
	termsVersion    = "1.0"
	termsMinVersion = "1.0"
	termsURL        = "https://turf.build/license/v1/"

	acceptEnv  = "TURF_ACCEPT_EULA"
	acceptFlag = "accept-eula"

	// notAcceptedExitCode is the exit status of a gated command run without
	// acceptance (EX_NOPERM).
	notAcceptedExitCode = 77

	markerFileName = "license.json"
)

// The ways acceptance is given. A marker records only command or prompt: the
// flag and the variable accept for the process they reach and leave no record.
const (
	methodFlag    = "flag"
	methodEnv     = "env"
	methodCommand = "command"
	methodPrompt  = "prompt"
)

// acceptanceNotice is printed on stderr when the terms are not accepted, and
// before the first-run question.
const acceptanceNotice = "Turf is proprietary software. To accept the Turf license terms (version 1.0,\n" +
	"https://turf.build/license/v1/), run `turf license accept` or set TURF_ACCEPT_EULA=yes.\n" +
	"Free for individuals, education and small businesses within the Community limits.\n" +
	"Government and larger organizations need a commercial license.\n"

// licenseLines is the short statement printed under the version by
// `turf --version` and `turf version`, the same two lines every Turf binary
// prints.
const licenseLines = "License: Turf Community Edition License 1.0 — proprietary; free within the Community limits\n" +
	"https://turf.build/license/v1/\n"

// acceptPrompt is the first-run question, asked on stderr.
const acceptPrompt = "Type yes to accept: "

// acceptanceExemptCommands are the commands directly under root that run
// without acceptance (with their subcommands): help, shell completion, and
// the commands that show the version, the terms and the telemetry setting.
// --version and --help never reach the check: cobra answers them first.
var acceptanceExemptCommands = map[string]bool{
	"help":             true,
	"completion":       true,
	"__complete":       true,
	"__completeNoDesc": true,
	"version":          true,
	"license":          true,
	"telemetry":        true,
}

// flagAcceptEULA is the persistent --accept-eula flag.
var flagAcceptEULA bool

// exitError ends the process with code, printing msg on stderr as it is:
// without cobra's "Error:" prefix and without usage text (main.go).
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return strings.TrimRight(e.msg, "\n") }

func markerPath() string { return filepath.Join(turfHome(), markerFileName) }

// markerRecord is one acceptance as license.json records it.
type markerRecord struct {
	Terms        string
	TermsVersion string
	TermsURL     string
	AcceptedAt   time.Time
	Method       string
	AcceptedWith string
}

// The keys of one record; on re-acceptance these move into "previous".
var markerRecordKeys = []string{"terms", "terms_version", "terms_url", "accepted_at", "method", "accepted_with"}

var termsVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// parseTermsVersion parses MAJOR.MINOR.
func parseTermsVersion(v string) (major, minor int, ok bool) {
	m := termsVersionPattern.FindStringSubmatch(v)
	if m == nil {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(m[1])
	minor, err2 := strconv.Atoi(m[2])
	return major, minor, err1 == nil && err2 == nil
}

// readMarker reads license.json: the record it holds and the whole document
// (for a writer that keeps keys it does not know). A missing file returns an
// error wrapping os.ErrNotExist; a file that is not JSON, not an object, or
// not a valid record returns an error saying why. Either way it is not
// acceptance.
func readMarker(path string) (markerRecord, map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return markerRecord{}, nil, err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return markerRecord{}, nil, fmt.Errorf("not valid JSON: %w", err)
	}
	doc, ok := v.(map[string]any)
	if !ok {
		return markerRecord{}, nil, errors.New("not a JSON object")
	}
	str := func(k string) string { s, _ := doc[k].(string); return s }
	rec := markerRecord{
		Terms:        str("terms"),
		TermsVersion: str("terms_version"),
		TermsURL:     str("terms_url"),
		Method:       str("method"),
		AcceptedWith: str("accepted_with"),
	}
	if rec.Terms != termsName {
		return rec, doc, fmt.Errorf("terms is %q, not %q", rec.Terms, termsName)
	}
	major, minor, ok := parseTermsVersion(rec.TermsVersion)
	if !ok {
		return rec, doc, fmt.Errorf("terms_version %q is not MAJOR.MINOR", rec.TermsVersion)
	}
	minMajor, minMinor, _ := parseTermsVersion(termsMinVersion)
	if major < minMajor || (major == minMajor && minor < minMinor) {
		return rec, doc, fmt.Errorf("terms_version %s is below the minimum accepted version %s", rec.TermsVersion, termsMinVersion)
	}
	at, err := time.Parse(time.RFC3339, str("accepted_at"))
	if err != nil {
		return rec, doc, fmt.Errorf("accepted_at %q is not an RFC 3339 time", str("accepted_at"))
	}
	rec.AcceptedAt = at
	if rec.Method != methodCommand && rec.Method != methodPrompt {
		return rec, doc, fmt.Errorf("method %q is not %s or %s", rec.Method, methodCommand, methodPrompt)
	}
	return rec, doc, nil
}

// writeMarker records an acceptance of the current terms in license.json,
// atomically, mode 0600. Keys it does not know are kept; an earlier record
// moves into "previous". A file that is not a JSON object is replaced.
func writeMarker(path, method string, now time.Time) (markerRecord, error) {
	doc := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			if m, ok := v.(map[string]any); ok {
				doc = m
			}
		}
	}
	if _, had := doc["accepted_at"]; had {
		prev := map[string]any{}
		for _, k := range markerRecordKeys {
			if val, ok := doc[k]; ok {
				prev[k] = val
			}
		}
		list, _ := doc["previous"].([]any)
		doc["previous"] = append(list, prev)
	}
	if _, ok := doc["previous"].([]any); !ok {
		doc["previous"] = []any{}
	}
	rec := markerRecord{
		Terms:        termsName,
		TermsVersion: termsVersion,
		TermsURL:     termsURL,
		AcceptedAt:   now.UTC().Truncate(time.Second),
		Method:       method,
		AcceptedWith: "turf " + Version,
	}
	doc["terms"] = rec.Terms
	doc["terms_version"] = rec.TermsVersion
	doc["terms_url"] = rec.TermsURL
	doc["accepted_at"] = rec.AcceptedAt.Format(time.RFC3339)
	doc["method"] = rec.Method
	doc["accepted_with"] = rec.AcceptedWith
	if err := writeJSONPrivate(path, doc); err != nil {
		return markerRecord{}, err
	}
	return rec, nil
}

// acceptance is the outcome of resolveAcceptance.
type acceptance struct {
	accepted     bool
	method       string // flag, env, command or prompt
	termsVersion string
	acceptedAt   time.Time // a marker's time; zero for the flag and the variable
	source       string    // --accept-eula, TURF_ACCEPT_EULA, or the marker's path

	envProblem    string // TURF_ACCEPT_EULA set to a value that does not accept
	markerPath    string
	marker        *markerRecord // a valid marker, whether or not it decided
	markerMissing bool
	markerProblem string // why the marker is not acceptance, when it exists
}

// envAccepts reports whether a TURF_ACCEPT_EULA value accepts: yes, in any
// case, after trimming spaces. Nothing else does: not true, 1 or y.
func envAccepts(v string) bool { return strings.EqualFold(strings.TrimSpace(v), "yes") }

// resolveAcceptance decides whether the terms are accepted, checking the
// flag, then the variable, then the marker file.
func resolveAcceptance(flag bool, envVal string, envSet bool, path string) acceptance {
	a := acceptance{markerPath: path}
	if rec, _, err := readMarker(path); err == nil {
		a.marker = &rec
	} else if errors.Is(err, os.ErrNotExist) {
		a.markerMissing = true
	} else {
		a.markerProblem = err.Error()
	}
	if envSet && !envAccepts(envVal) {
		a.envProblem = fmt.Sprintf("%s is set to %q, which does not accept the terms: only yes does.", acceptEnv, envVal)
	}
	switch {
	case flag:
		a.accepted, a.method, a.termsVersion, a.source = true, methodFlag, termsVersion, "--"+acceptFlag
	case envSet && envAccepts(envVal):
		a.accepted, a.method, a.termsVersion, a.source = true, methodEnv, termsVersion, acceptEnv
	case a.marker != nil:
		a.accepted, a.method, a.termsVersion, a.source = true, a.marker.Method, a.marker.TermsVersion, path
		a.acceptedAt = a.marker.AcceptedAt
	}
	return a
}

// problemLines say why the terms are not accepted, beyond the notice: a
// variable set to a value that does not accept, and a marker file that is
// there but not valid.
func (a acceptance) problemLines() []string {
	var out []string
	if a.envProblem != "" {
		out = append(out, a.envProblem)
	}
	if a.markerProblem != "" {
		out = append(out, fmt.Sprintf("%s is not a valid acceptance record: %s.", a.markerPath, a.markerProblem))
	}
	return out
}

func (a acceptance) noticeText() string {
	s := acceptanceNotice
	for _, l := range a.problemLines() {
		s += l + "\n"
	}
	return s
}

// Seams for the first-run question: whether stdin and stderr are terminals,
// and where the answer is read from.
var (
	acceptanceTerminals           = func() bool { return isTerminal(os.Stdin) && isTerminal(os.Stderr) }
	acceptanceStdin     io.Reader = os.Stdin
)

func isTerminal(f *os.File) bool {
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// acceptanceExempt reports whether cmd runs without acceptance: it is, or is
// under, one of acceptanceExemptCommands. The root itself (bare `turf`, which
// runs the chat) is gated.
func acceptanceExempt(cmd *cobra.Command) bool {
	c := cmd
	for c.HasParent() && c.Parent().HasParent() {
		c = c.Parent()
	}
	if !c.HasParent() {
		return false
	}
	return acceptanceExemptCommands[c.Name()]
}

// requireAcceptance is the check the root's PersistentPreRunE runs before any
// command. Accepted, it returns nil (and, for --accept-eula, sets
// TURF_ACCEPT_EULA=yes so the server this process starts inherits it). Not
// accepted, it asks on a terminal outside CI, and otherwise returns an
// exitError with the notice and code 77.
func requireAcceptance(cmd *cobra.Command) error {
	if acceptanceExempt(cmd) {
		return nil
	}
	envVal, envSet := os.LookupEnv(acceptEnv)
	path := markerPath()
	a := resolveAcceptance(flagAcceptEULA, envVal, envSet, path)
	if a.accepted {
		if a.method == methodFlag {
			return os.Setenv(acceptEnv, "yes")
		}
		return nil
	}
	if os.Getenv("CI") == "" && acceptanceTerminals() {
		return promptAcceptance(acceptanceStdin, cmd.ErrOrStderr(), path, a)
	}
	return &exitError{code: notAcceptedExitCode, msg: a.noticeText()}
}

// promptAcceptance asks the first-run question: the notice, then "Type yes to
// accept: ". yes (in any case) records the acceptance with method prompt and
// returns nil; anything else, or the end of input, returns the exit error.
func promptAcceptance(in io.Reader, out io.Writer, path string, a acceptance) error {
	_, _ = fmt.Fprint(out, a.noticeText()+"\n"+acceptPrompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		_, _ = fmt.Fprintln(out)
	}
	if !envAccepts(line) {
		return &exitError{code: notAcceptedExitCode, msg: "The license terms were not accepted.\n"}
	}
	rec, werr := writeMarker(path, methodPrompt, time.Now())
	if werr != nil {
		// The answer stands for this run; the server it starts inherits it.
		_, _ = fmt.Fprintf(out, "Accepted for this run, but could not record it in %s: %v\n", path, werr)
		return os.Setenv(acceptEnv, "yes")
	}
	_, _ = fmt.Fprintln(out, acceptedMessage(rec, path))
	return nil
}

// acceptedMessage confirms a recorded acceptance.
func acceptedMessage(rec markerRecord, path string) string {
	return fmt.Sprintf("Accepted the %s %s (%s) on %s; recorded in %s.",
		termsTitle, rec.TermsVersion, termsURL, rec.AcceptedAt.UTC().Format(time.RFC3339), path)
}

// termsVersionLine is the line `turf --version` and `turf version` print
// under the license: whether the terms are accepted, and how. It never fails.
func termsVersionLine() string {
	envVal, envSet := os.LookupEnv(acceptEnv)
	a := resolveAcceptance(flagAcceptEULA, envVal, envSet, markerPath())
	switch {
	case !a.accepted:
		return "Terms: not accepted (turf license accept, or TURF_ACCEPT_EULA=yes)\n"
	case a.method == methodFlag:
		return "Terms: accepted by --accept-eula\n"
	case a.method == methodEnv:
		return "Terms: accepted by TURF_ACCEPT_EULA\n"
	}
	return fmt.Sprintf("Terms: accepted, version %s (%s, %s)\n", a.termsVersion, a.method, a.acceptedAt.UTC().Format(time.RFC3339))
}
