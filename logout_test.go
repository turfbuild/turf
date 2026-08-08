package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLogoutMissingHostSucceeds guards that logging out of a host turf never
// logged in to is a success, not an error: the desired end state already holds.
func TestLogoutMissingHostSucceeds(t *testing.T) {
	cases := []struct {
		name string
		seed string
	}{
		{"no file at all", ""},
		{"file exists, host absent", `{"credentials":{"other.example.com":{"token":"x"}}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "credentials.tfrc.json")
			if tc.seed != "" {
				if err := os.WriteFile(path, []byte(tc.seed), 0o600); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}

			var out bytes.Buffer
			if err := runLogout(mustHost(t, "app.terraform.io"), path, &out); err != nil {
				t.Fatalf("runLogout: %v", err)
			}
			if want := "No credentials for app.terraform.io are stored."; !strings.Contains(out.String(), want) {
				t.Errorf("output = %q, want it to contain %q", out.String(), want)
			}
		})
	}
}

// TestLogoutRemovesOnlyTargetHost guards the surgical removal: other hosts and
// unrelated CLI settings in the same file must survive untouched.
func TestLogoutRemovesOnlyTargetHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.tfrc.json")
	seed := `{
  "credentials": {
    "app.terraform.io": {
      "token": "remove-me"
    },
    "other.example.com": {
      "token": "keep-me"
    }
  },
  "plugin_cache_dir": "/tmp/pc"
}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var out bytes.Buffer
	if err := runLogout(mustHost(t, "app.terraform.io"), path, &out); err != nil {
		t.Fatalf("runLogout: %v", err)
	}

	var got struct {
		Credentials map[string]struct {
			Token string `json:"token"`
		} `json:"credentials"`
		PluginCacheDir string `json:"plugin_cache_dir"`
	}
	if err := json.Unmarshal([]byte(readCredsFile(t, path)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := got.Credentials["app.terraform.io"]; ok {
		t.Error("target host is still present")
	}
	if entry, ok := got.Credentials["other.example.com"]; !ok || entry.Token != "keep-me" {
		t.Errorf("other host = %v, want token keep-me", got.Credentials)
	}
	if got.PluginCacheDir != "/tmp/pc" {
		t.Errorf("plugin_cache_dir = %q, want /tmp/pc", got.PluginCacheDir)
	}
	if !strings.Contains(out.String(), "Success!") {
		t.Errorf("output did not report success; got:\n%s", out.String())
	}
}

// TestLogoutRemovesEquivalentHostKey guards that a host stored under a different
// but equivalent spelling is still found and removed.
func TestLogoutRemovesEquivalentHostKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.tfrc.json")
	if err := os.WriteFile(path, []byte(`{"credentials":{"APP.TERRAFORM.IO":{"token":"x"}}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var out bytes.Buffer
	if err := runLogout(mustHost(t, "app.terraform.io"), path, &out); err != nil {
		t.Fatalf("runLogout: %v", err)
	}

	if _, ok, err := storedCredential(path, mustHost(t, "app.terraform.io")); err != nil {
		t.Fatalf("storedCredential: %v", err)
	} else if ok {
		t.Error("credential survived logout")
	}
}

// TestLogoutIsOffline guards the defining property of logout: it never contacts
// the host. A hostname that cannot resolve must still complete promptly, which
// it would not if service discovery ever crept into this path.
func TestLogoutIsOffline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.tfrc.json")

	done := make(chan error, 1)
	go func() {
		var out bytes.Buffer
		done <- runLogout(mustHost(t, "does-not-exist.invalid"), path, &out)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runLogout: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runLogout did not return promptly; it may be making a network request")
	}
}

// TestLogoutWarnsAboutEnvShadowing guards that removing the stored token does not
// leave the user believing they are logged out when an environment variable
// still authenticates them.
func TestLogoutWarnsAboutEnvShadowing(t *testing.T) {
	t.Setenv("TF_TOKEN_app_terraform_io", "from-env")

	path := filepath.Join(t.TempDir(), "credentials.tfrc.json")
	if err := storeCredential(path, mustHost(t, "app.terraform.io"), "stored"); err != nil {
		t.Fatalf("storeCredential: %v", err)
	}

	var out bytes.Buffer
	if err := runLogout(mustHost(t, "app.terraform.io"), path, &out); err != nil {
		t.Fatalf("runLogout: %v", err)
	}
	if !strings.Contains(out.String(), "TF_TOKEN_app_terraform_io") {
		t.Errorf("output did not warn about the env var; got:\n%s", out.String())
	}
}
