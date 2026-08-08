package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/opentofu/svchost"

	"github.com/docker/docker-agent/pkg/atomicfile"
)

// This file is turf's interop layer with the OpenTofu/Terraform credentials
// file. It is a deliberate port of OpenTofu's
// internal/command/cliconfig/credentials.go rather than an independent design:
// `turf login` is only useful if a co-installed `tofu`/`terraform` reads back
// exactly what turf wrote, so path resolution, the surgical read-modify-write,
// and the on-disk byte layout all have to match upstream. Divergences are
// called out individually below.

// credentialsFileName is the fixed basename of the credentials file. Only the
// directory varies by platform; the name never does.
const credentialsFileName = "credentials.tfrc.json"

// credentialsMu serializes read-modify-write of the credentials file within one
// process. Racing turf processes are last-writer-wins, exactly as with tofu:
// upstream takes no lock either, and adding one would only protect against
// turf-vs-turf, not turf-vs-tofu.
var credentialsMu sync.Mutex

// tofuHomeDir mirrors cliconfig.ConfigLoader.homeDir: $HOME first, then
// user.Current().HomeDir.
//
// Deliberately not os.UserHomeDir, which consults USERPROFILE on Windows —
// upstream's config dir there hangs off %APPDATA% instead (see credentialsDir).
func tofuHomeDir() (string, error) {
	if home := os.Getenv("HOME"); home != "" {
		return home, nil
	}
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	if u.HomeDir == "" {
		return "", errors.New("cannot determine home directory: blank output from user lookup")
	}
	return u.HomeDir, nil
}

// credentialsDir resolves the CLI config directory that holds the credentials
// file, mirroring cliconfig.ConfigLoader.configDir.
//
// On Unix that is <home>/.terraform.d, except that a *fresh* install — one where
// ~/.terraform.d does not exist — with XDG_CONFIG_HOME set uses
// $XDG_CONFIG_HOME/opentofu instead. Note this is a stat-time decision, so the
// answer can change once the legacy directory appears.
//
// On Windows it is %APPDATA%\terraform.d. Upstream calls SHGetFolderPathW for
// CSIDL_APPDATA; turf reads the environment variable (falling back to
// os.UserConfigDir, which resolves the same folder) to keep syscall/unsafe out
// of the CLI. These agree in every ordinary configuration.
func credentialsDir() (string, error) {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "terraform.d"), nil
		}
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "terraform.d"), nil
	}

	home, err := tofuHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".terraform.d")
	if xdgDir := os.Getenv("XDG_CONFIG_HOME"); xdgDir != "" && !pathExists(dir) {
		dir = filepath.Join(xdgDir, "opentofu")
	}
	return dir, nil
}

// credentialsFilePath returns the full path of the credentials file.
func credentialsFilePath() (string, error) {
	dir, err := credentialsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, credentialsFileName), nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// storeCredential records token as the credential for host.
func storeCredential(path string, host svchost.Hostname, token string) error {
	return updateCredentialsFile(path, host, &token)
}

// forgetCredential removes any credential stored for host. Removing a host that
// is not present is not an error.
func forgetCredential(path string, host svchost.Hostname) error {
	return updateCredentialsFile(path, host, nil)
}

// updateCredentialsFile surgically rewrites the credentials file so that host
// maps to token, or — when token is nil — so that host is absent. It is a port
// of cliconfig (*CredentialsSource).updateLocalHostCredentials.
//
// "Surgically" is the operative word: the file is a general CLI config file that
// may carry unrelated top-level settings (plugin_cache_dir, provider_installation,
// …), so the whole document is decoded, one key is adjusted, and everything else
// is re-emitted untouched. Numbers are decoded as json.Number so a large integer
// elsewhere in the file survives the round-trip instead of degrading to a float.
func updateCredentialsFile(path string, host svchost.Hostname, token *string) error {
	credentialsMu.Lock()
	defer credentialsMu.Unlock()

	oldSrc, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}

	// Removing a credential from a file that does not exist is a no-op: there is
	// nothing to remove, and creating an empty file would be surprising.
	if len(oldSrc) == 0 && token == nil {
		return nil
	}

	var raw map[string]any
	if len(oldSrc) > 0 {
		dec := json.NewDecoder(bytes.NewReader(oldSrc))
		dec.UseNumber()
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("cannot read %s: %w", path, err)
		}
	}
	if raw == nil {
		raw = make(map[string]any)
	}

	rawCredsI, ok := raw["credentials"]
	if !ok {
		rawCredsI = make(map[string]any)
		raw["credentials"] = rawCredsI
	}
	rawCredsMap, ok := rawCredsI.(map[string]any)
	if !ok {
		return fmt.Errorf("credentials file %s has invalid value for \"credentials\" property: must be a JSON object", path)
	}

	// Hosts are keyed in display form, the way a human would write them, so an
	// existing entry may be spelled differently (different case, or unicode vs
	// punycode) while denoting the same host. Sweep out every key that normalizes
	// to our target before inserting, so we never leave a duplicate behind.
	for givenHost := range rawCredsMap {
		if canonHost, err := svchost.ForComparison(givenHost); err == nil && canonHost == host {
			delete(rawCredsMap, givenHost)
		}
	}

	if token != nil {
		// Upstream stores a ctyjson.SimpleJSONValue here; a plain map marshals
		// byte-identically under MarshalIndent (which re-indents any custom
		// marshaler's compacted output), and keeps go-cty out of turf's imports.
		rawCredsMap[host.ForDisplay()] = map[string]any{"token": *token}
	}

	// Two-space indent and no trailing newline, matching what tofu writes.
	newSrc, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot serialize updated credentials file: %w", err)
	}

	// Upstream lets os.CreateTemp fail when the directory is absent; turf creates
	// it, so `turf login` works on a machine that has never run tofu. 0700 keeps
	// the atomic-rename window (during which the file briefly carries umask
	// permissions) closed to other users.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("cannot create directory for credentials file %s: %w", path, err)
	}

	// atomicfile.Write is write-to-temp-in-the-same-dir + rename + chmod, which is
	// upstream's algorithm plus an fsync. The chmod is load-bearing on rewrite:
	// the underlying atomic writer copies the destination's existing mode onto the
	// temp file, so a file that was somehow 0644 would stay 0644 without it.
	if err := atomicfile.Write(path, bytes.NewReader(newSrc), 0o600); err != nil {
		return fmt.Errorf("cannot write credentials file %s: %w", path, err)
	}
	return nil
}

// storedCredential returns the token currently recorded for host, matching any
// key that normalizes to host rather than only its display spelling.
func storedCredential(path string, host svchost.Hostname) (string, bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if len(src) == 0 {
		return "", false, nil
	}

	var raw struct {
		Credentials map[string]struct {
			Token string `json:"token"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal(src, &raw); err != nil {
		return "", false, fmt.Errorf("cannot read %s: %w", path, err)
	}

	for givenHost, creds := range raw.Credentials {
		if canonHost, err := svchost.ForComparison(givenHost); err == nil && canonHost == host {
			return creds.Token, true, nil
		}
	}
	return "", false, nil
}

// envTokenVarForHost reports the name of a set TF_TOKEN_* variable that resolves
// to host, if any. A token supplied that way takes precedence over the
// credentials file, so login and logout warn that their write is shadowed.
//
// The name encoding mirrors cliconfig.collectCredentialsFromEnv: "__" becomes
// "-" *before* "_" becomes ".", which is unambiguous because a hyphen may not
// start or end a label, so an odd run of underscores cannot occur in a valid
// name. Hostnames are then normalized through the permissive top-level
// ForDisplay first, which — unlike everywhere else in turf — tolerates a
// pre-punycoded name, since environment variables often cannot carry unicode.
func envTokenVarForHost(host svchost.Hostname) (string, bool) {
	const prefix = "TF_TOKEN_"

	for _, ev := range os.Environ() {
		name, _, found := strings.Cut(ev, "=")
		if !found || !strings.HasPrefix(name, prefix) {
			continue
		}
		rawHost := name[len(prefix):]
		rawHost = strings.ReplaceAll(rawHost, "__", "-")
		rawHost = strings.ReplaceAll(rawHost, "_", ".")

		candidate, err := svchost.ForComparison(svchost.ForDisplay(rawHost))
		if err != nil {
			continue // not a valid hostname; ignore, as upstream does
		}
		if candidate == host {
			return name, true
		}
	}
	return "", false
}
