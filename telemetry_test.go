package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runTelemetry(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newTelemetryCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("turf telemetry %v: %v", args, err)
	}
	return out.String()
}

func TestTelemetryOffOnRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TURF_HOME", home)
	t.Setenv("TURF_TELEMETRY", "")
	t.Setenv("DO_NOT_TRACK", "")

	if out := runTelemetry(t); !strings.Contains(out, "on (the default; no setting yet)") {
		t.Errorf("status with no setting:\n%s", out)
	}
	if line := telemetryVersionLine(); line != "Telemetry: on (turf telemetry off to disable)\n" {
		t.Errorf("version line with no setting: %q", line)
	}

	runTelemetry(t, "off")
	path := filepath.Join(home, "telemetry.json")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	off, _, err := readTelemetrySetting(path)
	if err != nil || off.enabled || !uuidPattern.MatchString(off.installationID) {
		t.Fatalf("after off: %+v %v", off, err)
	}
	if line := telemetryVersionLine(); line != "Telemetry: off (turf telemetry on to enable)\n" {
		t.Errorf("version line when off: %q", line)
	}

	runTelemetry(t, "on")
	on, _, _ := readTelemetrySetting(path)
	if !on.enabled || on.installationID != off.installationID {
		t.Errorf("after on: %+v, want on with the same id %s", on, off.installationID)
	}
	if out := runTelemetry(t, "status"); !strings.Contains(out, "Installation id: "+on.installationID) {
		t.Errorf("status:\n%s", out)
	}
}

func TestTelemetryWriteKeepsOtherKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.json")
	const id = "1b4e28ba-2fa1-11d2-883f-b9a761bde3fb"
	if err := os.WriteFile(path, []byte(`{"enabled": true, "installation_id": "`+id+`", "note": "kept"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeTelemetrySetting(path, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["note"] != "kept" || doc["enabled"] != false || doc["installation_id"] != id {
		t.Errorf("rewritten as %s", raw)
	}
}

func TestTelemetryStatusNamesShellOverrides(t *testing.T) {
	t.Setenv("TURF_HOME", t.TempDir())
	t.Setenv("TURF_TELEMETRY", "off")
	t.Setenv("DO_NOT_TRACK", "")
	if out := runTelemetry(t, "status"); !strings.Contains(out, "TURF_TELEMETRY=off is set in this shell") {
		t.Errorf("status did not name the override:\n%s", out)
	}
}

// A broken setting reads as off, as the engine's supervisor runs it, and
// `turf telemetry on|off` replaces it.
func TestTelemetryBrokenSetting(t *testing.T) {
	for _, body := range []string{`{"enabled": fal`, `null`, `[]`, `{"enabled": "false"}`, `{"enabled": 0}`} {
		home := t.TempDir()
		t.Setenv("TURF_HOME", home)
		t.Setenv("TURF_TELEMETRY", "")
		t.Setenv("DO_NOT_TRACK", "")
		path := filepath.Join(home, "telemetry.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readTelemetrySetting(path); err == nil {
			t.Errorf("%s read without an error", body)
		}
		if line := telemetryVersionLine(); !strings.HasPrefix(line, "Telemetry: off until the setting is fixed") {
			t.Errorf("%s: version line %q", body, line)
		}
		if out := runTelemetry(t, "status"); !strings.Contains(out, "off until the setting is fixed") {
			t.Errorf("%s: status\n%s", body, out)
		}
		runTelemetry(t, "off")
		if s, _, err := readTelemetrySetting(path); err != nil || s.enabled || s.installationID == "" {
			t.Errorf("%s after off: %+v, %v", body, s, err)
		}
	}
}

func TestNewUUIDIsVersion4(t *testing.T) {
	a, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newUUID()
	if !uuidPattern.MatchString(a) || a == b || a[14] != '4' || !strings.ContainsRune("89ab", rune(a[19])) {
		t.Errorf("newUUID = %q, %q", a, b)
	}
}

// The agent runtime's telemetry is off for every command, whatever the flags.
func TestAgentRuntimeTelemetryAlwaysOff(t *testing.T) {
	t.Setenv("TELEMETRY_ENABLED", "true")
	t.Setenv("TURF_HOME", t.TempDir())
	cmd := newRootCmd()
	cmd.SetArgs([]string{"telemetry", "status"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("TELEMETRY_ENABLED"); got != "false" {
		t.Errorf("TELEMETRY_ENABLED = %q after a command, want false", got)
	}
}
