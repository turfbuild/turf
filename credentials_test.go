package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/opentofu/svchost"
)

func mustHost(t *testing.T, given string) svchost.Hostname {
	t.Helper()
	h, err := svchost.ForComparison(given)
	if err != nil {
		t.Fatalf("ForComparison(%q): %v", given, err)
	}
	return h
}

func readCredsFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestStoreCredentialByteFormat is the interop guard: a co-installed tofu or
// terraform has to read back exactly what turf writes, so the on-disk layout is
// pinned byte-for-byte — two-space indent and, notably, no trailing newline.
func TestStoreCredentialByteFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.tfrc.json")

	if err := storeCredential(path, mustHost(t, "app.terraform.io"), "abc"); err != nil {
		t.Fatalf("storeCredential: %v", err)
	}

	want := "{\n  \"credentials\": {\n    \"app.terraform.io\": {\n      \"token\": \"abc\"\n    }\n  }\n}"
	if got := readCredsFile(t, path); got != want {
		t.Errorf("file content =\n%q\nwant\n%q", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

// TestStoreCredentialPreservesUnrelatedContent guards the "surgical" part of the
// read-modify-write: the credentials file is a general CLI config file, so every
// other top-level setting must survive untouched. The large integer pins the
// json.Number decoding — without it, it would degrade to 1.2345678901234567e+19.
func TestStoreCredentialPreservesUnrelatedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.tfrc.json")
	seed := `{
  "big": 12345678901234567890,
  "credentials": {
    "other.example.com": {
      "token": "keep-me"
    }
  },
  "plugin_cache_dir": "/tmp/pc",
  "provider_installation": [
    {
      "filesystem_mirror": {}
    }
  ]
}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := storeCredential(path, mustHost(t, "app.terraform.io"), "new-token"); err != nil {
		t.Fatalf("storeCredential: %v", err)
	}

	got := readCredsFile(t, path)
	for _, want := range []string{
		`"big": 12345678901234567890`,
		`"plugin_cache_dir": "/tmp/pc"`,
		`"filesystem_mirror"`,
		`"token": "keep-me"`,
		`"token": "new-token"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("file content missing %s; got:\n%s", want, got)
		}
	}
}

// TestStoreCredentialReplacesEquivalentHostKeys guards the normalize-and-sweep
// step. Hosts are keyed in display form, so an existing entry may be spelled
// differently while denoting the same host; leaving it behind would produce a
// file with two entries for one host.
//
// Note a punycode key is deliberately NOT swept: tofu rejects punycode in a
// credentials block and ignores such an entry on read, so it denotes no host at
// all and there is nothing to collide with.
func TestStoreCredentialReplacesEquivalentHostKeys(t *testing.T) {
	cases := []struct {
		name    string
		seedKey string
		host    string
		wantKey string
	}{
		{"uppercase folded", "APP.TERRAFORM.IO", "app.terraform.io", "app.terraform.io"},
		{"idn stored in display form", "café.fr", "café.fr", "café.fr"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "credentials.tfrc.json")
			seed := map[string]any{"credentials": map[string]any{tc.seedKey: map[string]any{"token": "old"}}}
			b, err := json.Marshal(seed)
			if err != nil {
				t.Fatalf("marshal seed: %v", err)
			}
			if err := os.WriteFile(path, b, 0o600); err != nil {
				t.Fatalf("seed: %v", err)
			}

			if err := storeCredential(path, mustHost(t, tc.host), "new"); err != nil {
				t.Fatalf("storeCredential: %v", err)
			}

			var got struct {
				Credentials map[string]struct {
					Token string `json:"token"`
				} `json:"credentials"`
			}
			if err := json.Unmarshal([]byte(readCredsFile(t, path)), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(got.Credentials) != 1 {
				t.Fatalf("got %d credential entries, want 1: %v", len(got.Credentials), got.Credentials)
			}
			if entry, ok := got.Credentials[tc.wantKey]; !ok {
				t.Errorf("key = %v, want %q", got.Credentials, tc.wantKey)
			} else if entry.Token != "new" {
				t.Errorf("token = %q, want %q", entry.Token, "new")
			}
		})
	}
}

// TestStoreCredentialRetightensMode guards a subtlety of the atomic writer: it
// copies the destination file's existing mode onto its temp file, so rewriting a
// file that was somehow world-readable would preserve that unless the mode is
// reapplied after the rename.
func TestStoreCredentialRetightensMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "credentials.tfrc.json")
	if err := os.WriteFile(path, []byte(`{"credentials":{}}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := storeCredential(path, mustHost(t, "app.terraform.io"), "abc"); err != nil {
		t.Fatalf("storeCredential: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

// TestUpdateCredentialsFileRejectsNonObjectCredentials guards that a malformed
// file is reported rather than silently overwritten, since it may hold settings
// the user cares about.
func TestUpdateCredentialsFileRejectsNonObjectCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.tfrc.json")
	seed := `{"credentials": "nope"}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := storeCredential(path, mustHost(t, "app.terraform.io"), "abc"); err == nil {
		t.Fatal("storeCredential succeeded, want an error")
	}
	if got := readCredsFile(t, path); got != seed {
		t.Errorf("file was modified: got %q, want %q", got, seed)
	}
}

// TestForgetCredentialRetainsEmptyCredentialsObject matches tofu: removing the
// last host leaves the "credentials" property in place rather than deleting it.
func TestForgetCredentialRetainsEmptyCredentialsObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.tfrc.json")
	host := mustHost(t, "app.terraform.io")
	if err := storeCredential(path, host, "abc"); err != nil {
		t.Fatalf("storeCredential: %v", err)
	}

	if err := forgetCredential(path, host); err != nil {
		t.Fatalf("forgetCredential: %v", err)
	}

	want := "{\n  \"credentials\": {}\n}"
	if got := readCredsFile(t, path); got != want {
		t.Errorf("file content = %q, want %q", got, want)
	}
}

// TestForgetCredentialNoFile guards that logging out of a host on a machine that
// has never logged in is a no-op, and specifically does not create an empty file.
func TestForgetCredentialNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.tfrc.json")

	if err := forgetCredential(path, mustHost(t, "app.terraform.io")); err != nil {
		t.Fatalf("forgetCredential: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("credentials file was created; stat err = %v, want IsNotExist", err)
	}
}

// TestStoredCredential guards the lookup used to decide whether login is
// replacing a token and whether logout has anything to do — including that it
// matches on the normalized host, not the literal key spelling.
func TestStoredCredential(t *testing.T) {
	cases := []struct {
		name      string
		seed      string
		host      string
		wantToken string
		wantOK    bool
		wantErr   bool
	}{
		{name: "missing file", seed: "", host: "app.terraform.io"},
		{name: "absent host", seed: `{"credentials":{"other.example.com":{"token":"x"}}}`, host: "app.terraform.io"},
		{name: "present", seed: `{"credentials":{"app.terraform.io":{"token":"abc"}}}`, host: "app.terraform.io", wantToken: "abc", wantOK: true},
		{name: "uppercase key matches", seed: `{"credentials":{"APP.TERRAFORM.IO":{"token":"abc"}}}`, host: "app.terraform.io", wantToken: "abc", wantOK: true},
		{name: "idn display key matches", seed: `{"credentials":{"café.fr":{"token":"abc"}}}`, host: "café.fr", wantToken: "abc", wantOK: true},
		{name: "malformed", seed: `{`, host: "app.terraform.io", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "credentials.tfrc.json")
			if tc.seed != "" {
				if err := os.WriteFile(path, []byte(tc.seed), 0o600); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}

			token, ok, err := storedCredential(path, mustHost(t, tc.host))
			if tc.wantErr {
				if err == nil {
					t.Fatal("storedCredential succeeded, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("storedCredential: %v", err)
			}
			if ok != tc.wantOK || token != tc.wantToken {
				t.Errorf("= (%q, %v), want (%q, %v)", token, ok, tc.wantToken, tc.wantOK)
			}
		})
	}
}

// TestEnvTokenVarForHost guards the TF_TOKEN_ name decoding, whose ordering is
// load-bearing: "__" becomes "-" before "_" becomes ".". It also pins the
// deliberate tolerance of pre-punycoded names, which turf rejects everywhere else.
func TestEnvTokenVarForHost(t *testing.T) {
	cases := []struct {
		name    string
		envVar  string
		host    string
		wantVar string
	}{
		{"plain", "TF_TOKEN_app_terraform_io", "app.terraform.io", "TF_TOKEN_app_terraform_io"},
		{"hyphen via double underscore", "TF_TOKEN_my__host_example_com", "my-host.example.com", "TF_TOKEN_my__host_example_com"},
		{"punycode tolerated", "TF_TOKEN_xn____caf__dma_fr", "café.fr", "TF_TOKEN_xn____caf__dma_fr"},
		{"unrelated host", "TF_TOKEN_other_example_com", "app.terraform.io", ""},
		{"invalid hostname ignored", "TF_TOKEN_", "app.terraform.io", ""},
		{"not a token var", "TF_LOG", "app.terraform.io", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envVar, "some-token")

			got, ok := envTokenVarForHost(mustHost(t, tc.host))
			if tc.wantVar == "" {
				if ok {
					t.Errorf("matched %q, want no match", got)
				}
				return
			}
			if !ok || got != tc.wantVar {
				t.Errorf("= (%q, %v), want (%q, true)", got, ok, tc.wantVar)
			}
		})
	}
}

// TestCredentialsDirResolution guards the XDG rule, which is a stat-time
// decision: the legacy ~/.terraform.d wins whenever it exists, and
// XDG_CONFIG_HOME only applies to an otherwise-fresh machine.
func TestCredentialsDirResolution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the XDG rule is Unix-only")
	}

	cases := []struct {
		name       string
		legacyDir  bool
		xdg        bool
		wantSuffix string
	}{
		{"legacy exists, xdg set", true, true, ".terraform.d"},
		{"legacy absent, xdg set", false, true, filepath.Join("xdg", "opentofu")},
		{"legacy absent, no xdg", false, false, ".terraform.d"},
		{"legacy exists, no xdg", true, false, ".terraform.d"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if tc.legacyDir {
				if err := os.MkdirAll(filepath.Join(home, ".terraform.d"), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
			}
			if tc.xdg {
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
			} else {
				t.Setenv("XDG_CONFIG_HOME", "")
			}

			dir, err := credentialsDir()
			if err != nil {
				t.Fatalf("credentialsDir: %v", err)
			}
			want := filepath.Join(home, tc.wantSuffix)
			if dir != want {
				t.Errorf("credentialsDir() = %q, want %q", dir, want)
			}

			path, err := credentialsFilePath()
			if err != nil {
				t.Fatalf("credentialsFilePath: %v", err)
			}
			if wantPath := filepath.Join(want, "credentials.tfrc.json"); path != wantPath {
				t.Errorf("credentialsFilePath() = %q, want %q", path, wantPath)
			}
		})
	}
}
