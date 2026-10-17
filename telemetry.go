package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// Usage telemetry is the Turf engine's: anonymous counts it sends to Turf LLC
// (docs/telemetry.md). The CLI sends none of its own, and the agent runtime's
// telemetry is always off (root.go). What the CLI owns is the user's setting,
// $TURF_HOME/telemetry.json, which `turf-mcp-server engine run` reads each time
// it starts the engine:
//
//	{"enabled": true, "installation_id": "<uuid>"}
//
// The contract with turf-mcp-server, which reads and writes the same file: those
// two keys, an atomic write, and any other key kept as found.

const telemetryURL = "https://turf.build/telemetry"

// telemetryRestartHint is what a change to the setting needs before the
// running engine follows it.
const telemetryRestartHint = "Takes effect when the engine restarts: brew services restart turf " +
	"(or restart turf-mcp-server engine run)."

func telemetryPath() string { return filepath.Join(turfHome(), "telemetry.json") }

// telemetrySetting is what the file says. exists is false when there is no
// file yet: telemetry is then on, the default, and the engine's supervisor
// writes the file with an id at its first start.
type telemetrySetting struct {
	enabled        bool
	installationID string
	exists         bool
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// readTelemetrySetting reads the setting and the file's whole document, for a
// writer that must keep what it does not know. A missing enabled is on; a file
// that is not a JSON object, or whose enabled is not true or false, is an
// error, and the engine's supervisor starts the engine with telemetry off
// until it is fixed.
func readTelemetrySetting(path string) (telemetrySetting, map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return telemetrySetting{enabled: true}, map[string]any{}, nil
	}
	if err != nil {
		return telemetrySetting{}, nil, err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return telemetrySetting{}, nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	doc, ok := v.(map[string]any)
	if !ok {
		return telemetrySetting{}, nil, fmt.Errorf("%s is not a JSON object", path)
	}
	s := telemetrySetting{enabled: true, exists: true}
	if e, present := doc["enabled"]; present {
		b, ok := e.(bool)
		if !ok {
			return telemetrySetting{}, nil, fmt.Errorf("%s has an enabled that is not true or false", path)
		}
		s.enabled = b
	}
	if id, ok := doc["installation_id"].(string); ok && uuidPattern.MatchString(id) {
		s.installationID = strings.ToLower(id)
	}
	return s, doc, nil
}

// writeTelemetrySetting sets enabled, keeping the file's other keys and giving
// it an installation id if it has none. A broken file is replaced: the user
// asked for a setting, and nothing in it could be trusted.
func writeTelemetrySetting(path string, enabled bool) (telemetrySetting, error) {
	s, doc, err := readTelemetrySetting(path)
	if err != nil {
		if _, statErr := os.Stat(path); statErr != nil {
			return telemetrySetting{}, err
		}
		s, doc = telemetrySetting{}, map[string]any{}
	}
	if s.installationID == "" {
		if s.installationID, err = newUUID(); err != nil {
			return telemetrySetting{}, err
		}
	}
	s.enabled, s.exists = enabled, true
	doc["enabled"], doc["installation_id"] = enabled, s.installationID
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return telemetrySetting{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return telemetrySetting{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".telemetry.json.*")
	if err != nil {
		return telemetrySetting{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return telemetrySetting{}, err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return telemetrySetting{}, err
	}
	if err := tmp.Close(); err != nil {
		return telemetrySetting{}, err
	}
	return s, os.Rename(tmp.Name(), path)
}

// newUUID is a random (version 4) UUID.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// telemetryVersionLine is the line `turf --version` prints under the license:
// the user's setting, and the command that changes it.
func telemetryVersionLine() string {
	s, _, err := readTelemetrySetting(telemetryPath())
	switch {
	case err != nil:
		return "Telemetry: off until the setting is fixed (" + err.Error() + "; turf telemetry on|off rewrites it)\n"
	case s.enabled:
		return "Telemetry: on (turf telemetry off to disable)\n"
	}
	return "Telemetry: off (turf telemetry on to enable)\n"
}

func newTelemetryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry [status|on|off]",
		Short: "Show or change whether the Turf engine sends anonymous usage telemetry",
		Long: "The Turf engine sends anonymous usage telemetry to Turf LLC by default: the version, the platform, " +
			"counts of resources, changes and runs, and an anonymous installation id — never resource contents, " +
			"credentials, state, variable values or names (" + telemetryURL + "). `turf telemetry off` turns it off " +
			"in the setting the engine starts with (" + "$TURF_HOME/telemetry.json" + "); restart the engine afterwards. " +
			"TURF_TELEMETRY=off or DO_NOT_TRACK=1 in the engine's own environment also turn it off; for the Homebrew " +
			"service that environment is turf.env, not your shell.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return telemetryStatus(cmd.OutOrStdout())
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show the telemetry setting and the installation id",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return telemetryStatus(cmd.OutOrStdout())
		},
	})
	for _, on := range []bool{true, false} {
		name := map[bool]string{true: "on", false: "off"}[on]
		cmd.AddCommand(&cobra.Command{
			Use:   name,
			Short: map[bool]string{true: "Turn usage telemetry on", false: "Turn usage telemetry off"}[on],
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				s, err := writeTelemetrySetting(telemetryPath(), on)
				if err != nil {
					return fmt.Errorf("writing %s: %w", telemetryPath(), err)
				}
				out := cmd.OutOrStdout()
				_, _ = fmt.Fprintf(out, "Telemetry is %s (installation %s), in %s.\n%s\n", name, s.installationID, telemetryPath(), telemetryRestartHint)
				if os.Getenv("TURF_HOME") != "" {
					_, _ = fmt.Fprintf(out, "TURF_HOME is set in this shell; the Homebrew service reads ~/.turf unless TURF_HOME is in its turf.env too.\n")
				}
				return nil
			},
		})
	}
	return cmd
}

func telemetryStatus(out io.Writer) error {
	path := telemetryPath()
	s, _, err := readTelemetrySetting(path)
	state := map[bool]string{true: "on", false: "off"}[s.enabled]
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(out, "Telemetry:       off until the setting is fixed: %v\n", err)
		_, _ = fmt.Fprintf(out, "                 turf telemetry on|off rewrites it\n")
	case !s.exists:
		_, _ = fmt.Fprintf(out, "Telemetry:       %s (the default; no setting yet)\n", state)
		_, _ = fmt.Fprintf(out, "Installation id: none yet; the engine's first start assigns one\n")
	default:
		_, _ = fmt.Fprintf(out, "Telemetry:       %s\n", state)
		_, _ = fmt.Fprintf(out, "Installation id: %s\n", s.installationID)
	}
	_, _ = fmt.Fprintf(out, "Setting:         %s\n", path)
	for _, v := range []string{"TURF_TELEMETRY", "DO_NOT_TRACK"} {
		if val := os.Getenv(v); val != "" {
			_, _ = fmt.Fprintf(out, "Note:            %s=%s is set in this shell. It applies to an engine started from this "+
				"shell (turf-mcp-server engine run), not to the Homebrew service, whose environment is turf.env.\n", v, val)
		}
	}
	_, _ = fmt.Fprintf(out, "What is sent:    %s\n", telemetryURL)
	_, _ = fmt.Fprintf(out, "The running engine's own state: turf-mcp-server engine status\n")
	return nil
}
