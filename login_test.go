package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/opentofu/svchost"
	"github.com/opentofu/svchost/disco"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

// setStdin points the prompt seam at a temp file holding the given input. A
// regular file is not a terminal, so promptSecret takes its line-read branch and
// the secret prompt needs no seam of its own. The shared buffered reader is
// reset too, since it would otherwise hold a handle on the previous stdin.
func setStdin(t *testing.T, input string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("create temp stdin: %v", err)
	}
	if _, err := f.WriteString(input); err != nil {
		t.Fatalf("write temp stdin: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek temp stdin: %v", err)
	}

	prevFile, prevReader := stdin, stdinLines
	stdin, stdinLines = f, nil
	t.Cleanup(func() {
		stdin, stdinLines = prevFile, prevReader
		_ = f.Close()
	})
}

// setInteractiveStdin additionally claims the substituted stdin is a terminal,
// so the interactive flow proceeds instead of refusing. promptSecret still sees
// a plain file and takes its line-read branch, which is what lets the token be
// supplied without a pty.
func setInteractiveStdin(t *testing.T, input string) {
	t.Helper()
	setStdin(t, input)
	prev := stdinIsInteractive
	stdinIsInteractive = func() bool { return true }
	t.Cleanup(func() { stdinIsInteractive = prev })
}

// setBrowser swaps the browser-opening seam for the duration of a test.
func setBrowser(t *testing.T, open func(ctx context.Context, u string) error) {
	t.Helper()
	prev := openBrowser
	openBrowser = open
	t.Cleanup(func() { openBrowser = prev })
}

// discoFor builds a Disco whose answer for host is fixed, so no discovery
// request is ever made. Service URLs are absolute, so a plain (non-TLS)
// httptest server is enough.
func discoFor(host svchost.Hostname, services map[string]any) *disco.Disco {
	d := disco.New()
	d.ForceHostServices(host, services)
	return d
}

func testLoginOptions(t *testing.T, host svchost.Hostname, services map[string]any, out *bytes.Buffer) loginOptions {
	t.Helper()
	return loginOptions{
		host:       host,
		credsPath:  filepath.Join(t.TempDir(), "credentials.tfrc.json"),
		services:   discoFor(host, services),
		httpClient: http.DefaultClient,
		out:        out,
	}
}

// TestLoginArgs guards that both commands require exactly one hostname, matching
// tofu, which likewise declines to guess a default host. The message matters:
// the root command sets SilenceUsage, so cobra's own terse wording would appear
// with no usage text to explain it.
func TestLoginArgs(t *testing.T) {
	cases := []struct {
		name    string
		ctor    func() *cobra.Command
		argv    []string
		wantErr string
	}{
		{"login no args", newLoginCmd, nil, "the login command expects exactly one argument: the host to log in to"},
		{"login two args", newLoginCmd, []string{"a.example.com", "b.example.com"}, "the login command expects exactly one argument"},
		{"login one arg", newLoginCmd, []string{"a.example.com"}, ""},
		{"logout no args", newLogoutCmd, nil, "the logout command expects exactly one argument: the host to log out of"},
		{"logout two args", newLogoutCmd, []string{"a.example.com", "b.example.com"}, "the logout command expects exactly one argument"},
		{"logout one arg", newLogoutCmd, []string{"a.example.com"}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.ctor()
			err := cmd.Args(cmd, tc.argv)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Args(%v) = %v, want nil", tc.argv, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Args(%v) = nil, want an error", tc.argv)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Args(%v) = %q, want it to contain %q", tc.argv, err, tc.wantErr)
			}
		})
	}
}

// TestLoginHostnameNormalization guards the hostname handling that decides the
// credentials file key. A divergence here means tofu cannot find what turf wrote.
func TestLoginHostnameNormalization(t *testing.T) {
	cases := []struct {
		name        string
		given       string
		wantCompare string
		wantDisplay string
		wantErr     bool
	}{
		{name: "plain", given: "app.terraform.io", wantCompare: "app.terraform.io", wantDisplay: "app.terraform.io"},
		{name: "uppercase folded", given: "APP.TERRAFORM.IO", wantCompare: "app.terraform.io", wantDisplay: "app.terraform.io"},
		{name: "default port stripped", given: "app.terraform.io:443", wantCompare: "app.terraform.io", wantDisplay: "app.terraform.io"},
		{name: "other port retained", given: "tfe.example.com:8443", wantCompare: "tfe.example.com:8443", wantDisplay: "tfe.example.com:8443"},
		{name: "idn punycoded for comparison", given: "café.fr", wantCompare: "xn--caf-dma.fr", wantDisplay: "café.fr"},
		{name: "full url rejected", given: "https://app.terraform.io", wantErr: true},
		{name: "punycode input rejected", given: "xn--caf-dma.fr", wantErr: true},
		{name: "empty rejected", given: "", wantErr: true},
		{name: "empty label rejected", given: "a..b", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, err := svchost.ForComparison(tc.given)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ForComparison(%q) = %q, want an error", tc.given, host)
				}
				return
			}
			if err != nil {
				t.Fatalf("ForComparison(%q): %v", tc.given, err)
			}
			if string(host) != tc.wantCompare {
				t.Errorf("comparison form = %q, want %q", host, tc.wantCompare)
			}
			if got := host.ForDisplay(); got != tc.wantDisplay {
				t.Errorf("display form = %q, want %q", got, tc.wantDisplay)
			}
		})
	}
}

// TestListenerForCallback guards the loopback redirect URI, which is
// protocol-load-bearing: hosts register the literal string, so it must name
// "localhost" (while the listener binds 127.0.0.1) and carry no trailing slash.
func TestListenerForCallback(t *testing.T) {
	t.Run("shape and bounds", func(t *testing.T) {
		listener, redirectURI, err := listenerForCallback(t.Context(), 10000, 10010)
		if err != nil {
			t.Fatalf("listenerForCallback: %v", err)
		}
		t.Cleanup(func() { closeListener(t, listener) })

		u, err := url.Parse(redirectURI)
		if err != nil {
			t.Fatalf("parse %q: %v", redirectURI, err)
		}
		if u.Scheme != "http" {
			t.Errorf("scheme = %q, want http", u.Scheme)
		}
		if u.Hostname() != "localhost" {
			t.Errorf("redirect host = %q, want localhost", u.Hostname())
		}
		if u.Path != "/login" {
			t.Errorf("redirect path = %q, want /login (no trailing slash)", u.Path)
		}

		listenPort := listenerPort(t, listener.Addr().String())
		if got := listener.Addr().String(); !strings.HasPrefix(got, "127.0.0.1:") {
			t.Errorf("listener bound to %q, want 127.0.0.1", got)
		}
		if u.Port() != strconv.Itoa(int(listenPort)) {
			t.Errorf("redirect port %q does not match listener port %d", u.Port(), listenPort)
		}
		if listenPort < 10000 || listenPort > 10010 {
			t.Errorf("port %d outside the advertised range 10000-10010", listenPort)
		}
	})

	// A host must not be able to talk turf into binding a privileged port.
	t.Run("privileged range rejected", func(t *testing.T) {
		listener, _, err := listenerForCallback(t.Context(), 0, 0)
		if err == nil {
			closeListener(t, listener)
			t.Fatal("privileged range accepted, want an error")
		}
	})

	// tofu computes its span as max-min, so a single-port range yields zero
	// attempts and a spurious failure. Accepting it is a strict superset.
	t.Run("single port range", func(t *testing.T) {
		// Bind and release a port so we know one specific port is free.
		probe, _, err := listenerForCallback(t.Context(), 10000, 10010)
		if err != nil {
			t.Fatalf("probe listener: %v", err)
		}
		port := listenerPort(t, probe.Addr().String())
		closeListener(t, probe)

		listener, redirectURI, err := listenerForCallback(t.Context(), port, port)
		if err != nil {
			t.Fatalf("single-port range rejected: %v", err)
		}
		t.Cleanup(func() { closeListener(t, listener) })

		if want := fmt.Sprintf("http://localhost:%d/login", port); redirectURI != want {
			t.Errorf("redirect URI = %q, want %q", redirectURI, want)
		}
	})

	t.Run("exhausted range errors", func(t *testing.T) {
		held, _, err := listenerForCallback(t.Context(), 10000, 10010)
		if err != nil {
			t.Fatalf("probe listener: %v", err)
		}
		t.Cleanup(func() { closeListener(t, held) })
		port := listenerPort(t, held.Addr().String())

		if listener, _, err := listenerForCallback(t.Context(), port, port); err == nil {
			closeListener(t, listener)
			t.Fatal("bound an already-held port, want an error")
		}
	})
}

// TestLoginByCodeEndToEnd drives the whole OAuth path against a fake host: the
// browser seam completes the redirect, and the token endpoint asserts the PKCE
// and public-client properties the protocol depends on.
func TestLoginByCodeEndToEnd(t *testing.T) {
	const wantClientID = "turf-cli"

	var (
		gotChallenge string
		tokenForm    url.Values
		gotAuthzHdr  string
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse token form: %v", err)
		}
		tokenForm = r.PostForm
		gotAuthzHdr = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		// A refresh token and expiry are returned deliberately: the login
		// protocol has no notion of refreshing, so they must be dropped.
		_, _ = fmt.Fprint(w, `{"access_token":"the-token","token_type":"bearer","refresh_token":"nope","expires_in":3600}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := mustHost(t, "tfe.example.com")
	var out bytes.Buffer
	o := testLoginOptions(t, host, map[string]any{
		"login.v1": map[string]any{
			"client": wantClientID,
			"authz":  srv.URL + "/authz",
			"token":  srv.URL + "/token",
			"ports":  []any{float64(10000), float64(10010)},
		},
	}, &out)
	o.httpClient = srv.Client()

	setBrowser(t, func(_ context.Context, rawURL string) error {
		u, err := url.Parse(rawURL)
		if err != nil {
			return err
		}
		q := u.Query()
		gotChallenge = q.Get("code_challenge")
		gotState := q.Get("state")

		if got := q.Get("response_type"); got != "code" {
			t.Errorf("response_type = %q, want code", got)
		}
		if got := q.Get("code_challenge_method"); got != "S256" {
			t.Errorf("code_challenge_method = %q, want S256", got)
		}
		if got := q.Get("client_id"); got != wantClientID {
			t.Errorf("client_id = %q, want %q", got, wantClientID)
		}
		if _, ok := q["scope"]; ok {
			t.Errorf("scope present (%q) though the host advertised none", q.Get("scope"))
		}

		resp, err := http.Get(q.Get("redirect_uri") + "?code=the-code&state=" + url.QueryEscape(gotState))
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("callback status = %d, want 200", resp.StatusCode)
		}
		return nil
	})

	oauthClient := &disco.OAuthClient{
		ID:                  wantClientID,
		SupportedGrantTypes: disco.NewOAuthGrantTypeSet("authz_code"),
		MinPort:             10000,
		MaxPort:             10010,
		AuthorizationURL:    mustParseURL(t, srv.URL+"/authz"),
		TokenURL:            mustParseURL(t, srv.URL+"/token"),
	}

	token, err := loginByCode(t.Context(), o, oauthClient)
	if err != nil {
		t.Fatalf("loginByCode: %v", err)
	}
	if token != "the-token" {
		t.Errorf("token = %q, want %q", token, "the-token")
	}

	// The client is public: client_id travels in the form body, never as HTTP
	// Basic credentials, because there is no client secret to present.
	if gotAuthzHdr != "" {
		t.Errorf("token request carried an Authorization header %q, want none", gotAuthzHdr)
	}
	if got := tokenForm.Get("client_id"); got != wantClientID {
		t.Errorf("token request client_id = %q, want %q", got, wantClientID)
	}
	if got := tokenForm.Get("grant_type"); got != "authorization_code" {
		t.Errorf("grant_type = %q, want authorization_code", got)
	}
	if got := tokenForm.Get("code"); got != "the-code" {
		t.Errorf("code = %q, want the-code", got)
	}
	if got := tokenForm.Get("redirect_uri"); !strings.HasPrefix(got, "http://localhost:") || !strings.HasSuffix(got, "/login") {
		t.Errorf("redirect_uri = %q, want http://localhost:<port>/login", got)
	}

	verifier := tokenForm.Get("code_verifier")
	if verifier == "" {
		t.Fatal("token request carried no code_verifier")
	}
	sum := sha256.Sum256([]byte(verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); want != gotChallenge {
		t.Errorf("code_challenge = %q, want S256(code_verifier) = %q", gotChallenge, want)
	}

	// Persisting the token is runLogin's job, but confirm the discarded fields
	// never reach the caller.
	if strings.Contains(token, "nope") {
		t.Errorf("refresh token leaked into the stored token %q", token)
	}
}

// TestLoginByCodeReportsAuthorizationError guards a deliberate divergence from
// tofu, which ignores the error parameters and hangs until its timeout.
func TestLoginByCodeReportsAuthorizationError(t *testing.T) {
	host := mustHost(t, "tfe.example.com")
	var out bytes.Buffer
	o := testLoginOptions(t, host, nil, &out)

	setBrowser(t, func(ctx context.Context, rawURL string) error {
		u, err := url.Parse(rawURL)
		if err != nil {
			return err
		}
		resp, err := http.Get(u.Query().Get("redirect_uri") + "?error=access_denied&error_description=user+said+no")
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("callback status = %d, want 400", resp.StatusCode)
		}
		return nil
	})

	oauthClient := &disco.OAuthClient{
		ID:                  "turf-cli",
		SupportedGrantTypes: disco.NewOAuthGrantTypeSet("authz_code"),
		MinPort:             10000,
		MaxPort:             10010,
		AuthorizationURL:    mustParseURL(t, "https://tfe.example.com/authz"),
		TokenURL:            mustParseURL(t, "https://tfe.example.com/token"),
	}

	_, err := loginByCode(t.Context(), o, oauthClient)
	if err == nil {
		t.Fatal("loginByCode succeeded, want an authorization error")
	}
	if !strings.Contains(err.Error(), "access_denied") {
		t.Errorf("error = %q, want it to name access_denied", err)
	}
}

// TestLoginByCodeRejectsBadCallbacks guards the callback validation: a wrong
// state or a missing code must not yield a token.
func TestLoginByCodeRejectsBadCallbacks(t *testing.T) {
	cases := []struct {
		name  string
		query func(state string) string
	}{
		{"state mismatch", func(string) string { return "?code=c&state=wrong" }},
		{"missing code", func(state string) string { return "?state=" + url.QueryEscape(state) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := mustHost(t, "tfe.example.com")
			var out bytes.Buffer
			o := testLoginOptions(t, host, nil, &out)

			setBrowser(t, func(ctx context.Context, rawURL string) error {
				u, err := url.Parse(rawURL)
				if err != nil {
					return err
				}
				resp, err := http.Get(u.Query().Get("redirect_uri") + tc.query(u.Query().Get("state")))
				if err != nil {
					return err
				}
				defer func() { _ = resp.Body.Close() }()
				if resp.StatusCode != http.StatusBadRequest {
					t.Errorf("callback status = %d, want 400", resp.StatusCode)
				}
				return nil
			})

			oauthClient := &disco.OAuthClient{
				ID:                  "turf-cli",
				SupportedGrantTypes: disco.NewOAuthGrantTypeSet("authz_code"),
				MinPort:             10000,
				MaxPort:             10010,
				AuthorizationURL:    mustParseURL(t, "https://tfe.example.com/authz"),
				TokenURL:            mustParseURL(t, "https://tfe.example.com/token"),
			}

			if _, err := loginByCode(t.Context(), o, oauthClient); err == nil {
				t.Fatal("loginByCode succeeded, want an error")
			}
		})
	}
}

// TestLoginByTokenEndToEnd drives the manual-token path — the one every
// real-world host actually takes, since none advertises login.v1.
func TestLoginByTokenEndToEnd(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		status     int
		wantErr    string
		wantStored string
	}{
		{name: "accepted", input: "yes\n  tok-123  \n", status: http.StatusOK, wantStored: "tok-123"},
		{name: "declined", input: "no\n", status: http.StatusOK, wantErr: "login cancelled"},
		{name: "token rejected", input: "yes\nbad-token\n", status: http.StatusUnauthorized, wantErr: "the token is invalid"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotAuth string
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v2/account/details", func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				if tc.status != http.StatusOK {
					w.WriteHeader(tc.status)
					return
				}
				w.Header().Set("Content-Type", "application/vnd.api+json")
				_, _ = fmt.Fprint(w, `{"data":{"attributes":{"username":"alice"}}}`)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			host := mustHost(t, "app.terraform.io")
			var out bytes.Buffer
			o := testLoginOptions(t, host, map[string]any{"tfe.v2": srv.URL + "/api/v2/"}, &out)
			o.httpClient = srv.Client()

			setInteractiveStdin(t, tc.input)
			var browserURL string
			setBrowser(t, func(_ context.Context, u string) error {
				browserURL = u
				return nil
			})

			err := runLogin(t.Context(), o)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("runLogin succeeded, want %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				if _, err := os.Stat(o.credsPath); !os.IsNotExist(err) {
					t.Errorf("credentials file was written on a failed login; stat err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("runLogin: %v", err)
			}

			// The host here is the fake tfe.v2 service; TestTokensPageURL pins
			// the host-only derivation against realistic service URLs.
			if want := tokensPageURL(mustParseURL(t, srv.URL+"/api/v2/")); browserURL != want {
				t.Errorf("browser opened %q, want %q", browserURL, want)
			}
			if want := "Bearer " + tc.wantStored; gotAuth != want {
				t.Errorf("probe Authorization = %q, want %q", gotAuth, want)
			}
			if !strings.Contains(out.String(), "Retrieved token for user alice") {
				t.Errorf("output did not report the username; got:\n%s", out.String())
			}

			token, ok, err := storedCredential(o.credsPath, host)
			if err != nil {
				t.Fatalf("storedCredential: %v", err)
			}
			if !ok || token != tc.wantStored {
				t.Errorf("stored token = (%q, %v), want (%q, true)", token, ok, tc.wantStored)
			}
		})
	}
}

// TestTokensPageURL guards that only the host is taken from the tfe.v2 service
// URL: that URL addresses the API, while the token page lives in the web UI.
func TestTokensPageURL(t *testing.T) {
	cases := []struct {
		name    string
		service string
		want    string
	}{
		{"api path discarded", "https://app.terraform.io/api/v2/", "https://app.terraform.io/app/settings/tokens?source=terraform-login"},
		{"port retained", "https://tfe.example.com:8443/api/v2/", "https://tfe.example.com:8443/app/settings/tokens?source=terraform-login"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokensPageURL(mustParseURL(t, tc.service)); got != tc.want {
				t.Errorf("tokensPageURL(%q) = %q, want %q", tc.service, got, tc.want)
			}
		})
	}
}

// TestLoginWithoutTokensAPI guards that a host offering neither login.v1 nor
// tfe.v2 — the OpenTofu registry, for instance — fails cleanly and writes nothing.
func TestLoginWithoutTokensAPI(t *testing.T) {
	cases := []struct {
		name     string
		services map[string]any
	}{
		{"registry only", map[string]any{"modules.v1": "https://registry.opentofu.org/v1/modules/"}},
		{"no services at all", map[string]any{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := mustHost(t, "registry.opentofu.org")
			var out bytes.Buffer
			o := testLoginOptions(t, host, tc.services, &out)
			setInteractiveStdin(t, "yes\n")

			err := runLogin(t.Context(), o)
			if err == nil {
				t.Fatal("runLogin succeeded, want an error")
			}
			if !strings.Contains(err.Error(), "does not support turf authorization tokens") {
				t.Errorf("error = %q, want it to report no token support", err)
			}
			if _, err := os.Stat(o.credsPath); !os.IsNotExist(err) {
				t.Errorf("credentials file was written; stat err = %v", err)
			}
		})
	}
}

// TestConsentRequiresLiteralYes guards the one gate before turf writes a
// plaintext secret to disk.
func TestConsentRequiresLiteralYes(t *testing.T) {
	cases := []struct {
		answer string
		want   bool
	}{
		{"yes\n", true},
		{"YES\n", true},
		{"Yes\n", true},
		{"  yes  \n", true},
		{"y\n", false},
		{"no\n", false},
		{"\n", false},
		{"yesplease\n", false},
	}

	for _, tc := range cases {
		t.Run(strings.TrimSpace(tc.answer), func(t *testing.T) {
			setStdin(t, tc.answer)
			var out bytes.Buffer
			o := testLoginOptions(t, mustHost(t, "app.terraform.io"), nil, &out)

			got, err := consent(o, false, false)
			if err != nil {
				t.Fatalf("consent: %v", err)
			}
			if got != tc.want {
				t.Errorf("consent(%q) = %v, want %v", tc.answer, got, tc.want)
			}
		})
	}
}

// TestConsentDisclosesPathAndReplacement guards that the prompt states where the
// token lands and warns when it will overwrite an existing one — the whole
// reason the gate exists.
func TestConsentDisclosesPathAndReplacement(t *testing.T) {
	setStdin(t, "yes\n")
	var out bytes.Buffer
	o := testLoginOptions(t, mustHost(t, "app.terraform.io"), nil, &out)

	if _, err := consent(o, false, true); err != nil {
		t.Fatalf("consent: %v", err)
	}

	for _, want := range []string{o.credsPath, "plain text", "This will replace the token currently stored for app.terraform.io"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("consent output missing %q; got:\n%s", want, out.String())
		}
	}
}

// TestLoginRequiresInteractiveStdin guards the non-interactive contract: turf
// refuses rather than hanging, and names the flag that does work. With that flag
// the token is stored and no consent prompt appears, since piping the secret is
// itself the consent.
func TestLoginRequiresInteractiveStdin(t *testing.T) {
	host := mustHost(t, "app.terraform.io")

	t.Run("refuses without the flag", func(t *testing.T) {
		var out bytes.Buffer
		o := testLoginOptions(t, host, nil, &out)
		setStdin(t, "")

		err := runLogin(t.Context(), o)
		if err == nil {
			t.Fatal("runLogin succeeded, want an error")
		}
		if !strings.Contains(err.Error(), "--token-stdin") {
			t.Errorf("error = %q, want it to name --token-stdin", err)
		}
	})

	t.Run("token-stdin stores without prompting", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v2/account/details", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, `{"data":{"attributes":{"username":"alice"}}}`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		var out bytes.Buffer
		o := testLoginOptions(t, host, map[string]any{"tfe.v2": srv.URL + "/api/v2/"}, &out)
		o.httpClient = srv.Client()
		o.tokenStdin = true
		setStdin(t, "  piped-token\n")

		if err := runLogin(t.Context(), o); err != nil {
			t.Fatalf("runLogin: %v", err)
		}
		if strings.Contains(out.String(), "Do you want to proceed") {
			t.Errorf("--token-stdin emitted a consent prompt; got:\n%s", out.String())
		}

		token, ok, err := storedCredential(o.credsPath, host)
		if err != nil {
			t.Fatalf("storedCredential: %v", err)
		}
		if !ok || token != "piped-token" {
			t.Errorf("stored token = (%q, %v), want (%q, true)", token, ok, "piped-token")
		}
	})

	t.Run("token-stdin rejects an invalid token", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v2/account/details", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		var out bytes.Buffer
		o := testLoginOptions(t, host, map[string]any{"tfe.v2": srv.URL + "/api/v2/"}, &out)
		o.httpClient = srv.Client()
		o.tokenStdin = true
		setStdin(t, "bad\n")

		if err := runLogin(t.Context(), o); !errors.Is(err, errTokenInvalid) {
			t.Fatalf("runLogin err = %v, want errTokenInvalid", err)
		}
		if _, err := os.Stat(o.credsPath); !os.IsNotExist(err) {
			t.Errorf("credentials file was written for an invalid token; stat err = %v", err)
		}
	})
}

// TestLoginWarnsAboutEnvShadowing guards that a user is told when the file turf
// is about to write will be ignored in favor of an environment variable.
func TestLoginWarnsAboutEnvShadowing(t *testing.T) {
	t.Setenv("TF_TOKEN_app_terraform_io", "from-env")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/account/details", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":{"attributes":{"username":"alice"}}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := mustHost(t, "app.terraform.io")
	var out bytes.Buffer
	o := testLoginOptions(t, host, map[string]any{"tfe.v2": srv.URL + "/api/v2/"}, &out)
	o.httpClient = srv.Client()
	setInteractiveStdin(t, "no\n")
	setBrowser(t, func(context.Context, string) error { return nil })

	// The answer is "no", so this returns the cancellation error; the warning
	// must already have been printed by then, before the user had to decide.
	_ = runLogin(t.Context(), o)

	if !strings.Contains(out.String(), "TF_TOKEN_app_terraform_io") {
		t.Errorf("output did not warn about the env var; got:\n%s", out.String())
	}
}

// TestValidateTFEToken guards the probe that stands in for go-tfe's
// Users.ReadCurrent, including that an auth failure is distinguishable from a
// transport or protocol failure.
func TestValidateTFEToken(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantUser string
		wantErr  error
	}{
		{name: "ok", status: http.StatusOK, body: `{"data":{"attributes":{"username":"alice"}}}`, wantUser: "alice"},
		{name: "unauthorized", status: http.StatusUnauthorized, wantErr: errTokenInvalid},
		{name: "forbidden", status: http.StatusForbidden, wantErr: errTokenInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v2/account/details", func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Accept"); got != "application/vnd.api+json" {
					t.Errorf("Accept = %q, want application/vnd.api+json", got)
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			user, err := validateTFEToken(t.Context(), srv.Client(), mustParseURL(t, srv.URL+"/api/v2/"), "tok")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateTFEToken: %v", err)
			}
			if user != tc.wantUser {
				t.Errorf("username = %q, want %q", user, tc.wantUser)
			}
		})
	}
}

// TestS256ChallengeMatchesSpec pins the PKCE derivation to RFC 7636: base64url
// of the SHA-256 digest, without padding.
func TestS256ChallengeMatchesSpec(t *testing.T) {
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if got := oauth2.S256ChallengeFromVerifier(verifier); got != want {
		t.Errorf("S256ChallengeFromVerifier = %q, want %q", got, want)
	}
	if strings.Contains(want, "=") {
		t.Errorf("challenge %q is padded, want unpadded base64url", want)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

// listenerPort reports the port a listener actually bound. It returns uint16 —
// the type listenerForCallback takes — so callers never have to narrow an int
// and reason about whether the value fits.
func listenerPort(t *testing.T, addr string) uint16 {
	t.Helper()
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	return uint16(port)
}

// closeListener closes a listener, failing the test if it cannot.
func closeListener(t *testing.T, l net.Listener) {
	t.Helper()
	if err := l.Close(); err != nil {
		t.Errorf("close listener: %v", err)
	}
}
