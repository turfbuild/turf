package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/opentofu/svchost"
	"github.com/opentofu/svchost/disco"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/term"

	"github.com/docker/docker-agent/pkg/browser"
)

// turf login emulates `tofu login`, storing an API token in the shared
// OpenTofu/Terraform credentials file so turf, tofu, and terraform all
// authenticate from one place (see credentials.go for the file format).
//
// Two mechanisms exist. The host's service discovery document may advertise a
// login.v1 block, in which case turf runs an OAuth 2.0 authorization-code flow
// with PKCE against a loopback redirect. In practice no public host does:
// HCP Terraform, Scalr, and the OpenTofu registry all publish only tfe.v2 and
// friends. So the manual path — open the host's token page, paste the token
// back — is the one that actually runs, and it is treated as a first-class
// path here rather than as a fallback.

const (
	// loginTimeout bounds how long the OAuth path waits for the user to finish
	// in the browser. Matches the wait budget cagent uses for its own flows.
	loginTimeout = 10 * time.Minute

	// discoveryTimeout matches svchost/disco's own default.
	discoveryTimeout = 11 * time.Second

	// httpTimeout bounds the token exchange and the token-validation probe.
	httpTimeout = 30 * time.Second

	// loginSeparator visually brackets the browser step, as tofu's output does.
	loginSeparator = "---------------------------------------------------------------------------------"

	// callbackPath is the loopback redirect path. Deliberately has no trailing
	// slash: some OAuth servers reject callback URLs that end in one.
	callbackPath = "/login"
)

// openBrowser is a seam for tests; production opens the system browser.
var openBrowser = browser.Open

// stdin is a seam for tests, which substitute a pipe.
var stdin *os.File = os.Stdin

// stdinLines buffers stdin for the line-oriented prompts. It has to be one
// shared reader for the whole run: a fresh bufio.Reader per prompt would read
// ahead past the line it needs and drop the remainder, so the consent answer
// would swallow the token typed after it.
var stdinLines *bufio.Reader

func stdinReader() *bufio.Reader {
	if stdinLines == nil {
		stdinLines = bufio.NewReader(stdin)
	}
	return stdinLines
}

// stdinIsInteractive reports whether there is a human at the other end of
// stdin, which decides whether login may prompt at all. It is a seam so tests
// can drive the interactive flow from a pipe.
//
// Deliberately separate from the echo-suppression check in promptSecret: that
// one asks whether the terminal *can* disable echo, which is a different
// question and must stay honest even under test.
var stdinIsInteractive = func() bool { return term.IsTerminal(int(stdin.Fd())) }

// errTokenInvalid reports that the host rejected the supplied token.
var errTokenInvalid = errors.New("the token is invalid")

// loginOptions is the injectable core of the login flow. RunE assembles one
// from flags and the environment; tests construct one directly with a Disco
// preloaded via ForceHostServices and an httptest-backed client, so no test
// ever touches the network.
type loginOptions struct {
	host       svchost.Hostname
	credsPath  string
	services   *disco.Disco
	httpClient *http.Client
	out        io.Writer
	tokenStdin bool
}

func newLoginCmd() *cobra.Command {
	var tokenStdin bool

	cmd := &cobra.Command{
		Use:   "login <hostname>",
		Short: "Obtain and save an API token for a remote host",
		Long: "Obtain and save an API token for a Terraform-compatible host, such as a private module registry or a TFE-compatible backend.\n\n" +
			"The token is written to credentials.tfrc.json in your OpenTofu/Terraform CLI configuration directory (~/.terraform.d on macOS and Linux), so tofu and terraform read the same credentials turf stores.\n\n" +
			"Note that turf itself does not yet use these credentials for its own registry and backend requests; today they serve a co-installed tofu or terraform.\n\n" +
			"A TF_TOKEN_<hostname> environment variable, and any credentials block in .terraformrc or .tofurc, take precedence over the file this command writes.",
		Args: exactlyOneHost("login", "log in to"),
		RunE: func(cmd *cobra.Command, args []string) error {
			host, err := svchost.ForComparison(args[0])
			if err != nil {
				return fmt.Errorf("the given hostname %q is not valid: %w", args[0], err)
			}
			path, err := credentialsFilePath()
			if err != nil {
				return fmt.Errorf("unable to determine credentials file path: %w", err)
			}
			return runLogin(cmd.Context(), loginOptions{
				host:       host,
				credsPath:  path,
				services:   newLoginDisco(),
				httpClient: newLoginHTTPClient(),
				out:        cmd.OutOrStdout(),
				tokenStdin: tokenStdin,
			})
		},
	}

	cmd.Flags().BoolVar(&tokenStdin, "token-stdin", false, "Read the token from stdin instead of prompting, for non-interactive use")

	return cmd
}

// exactlyOneHost requires a single hostname argument, matching tofu, which also
// declines to guess a default host. Cobra's own ExactArgs(1) message
// ("accepts 1 arg(s), received 0") is unhelpful here because the root command
// sets SilenceUsage, so no usage text follows to explain what is missing.
func exactlyOneHost(command, verb string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("the %s command expects exactly one argument: the host to %s", command, verb)
		}
		return nil
	}
}

func runLogin(ctx context.Context, o loginOptions) error {
	disp := o.host.ForDisplay()

	// --token-stdin short-circuits everything interactive. Consent is implicit:
	// the caller piped in the secret, so there is nothing to disclose that they
	// have not already decided.
	if o.tokenStdin {
		return loginFromStdin(ctx, o)
	}

	if !stdinIsInteractive() {
		return fmt.Errorf("turf login is an interactive command and cannot run with stdin redirected. To supply a token non-interactively, pipe it to `turf login --token-stdin %s`", disp)
	}

	discoCtx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	h, err := o.services.Discover(discoCtx, o.host)
	if err != nil {
		return fmt.Errorf("service discovery failed for %s: %w", disp, err)
	}

	// Prefer the OAuth flow when the host advertises one, but treat every way of
	// not advertising it — absent, a newer version we do not speak, a malformed
	// block, or one offering only the password grant turf does not implement —
	// as a fall-through to the manual path rather than a failure.
	oauthClient, err := h.ServiceOAuthClient("login.v1")
	switch {
	case err == nil && !oauthClient.SupportedGrantTypes.Has(disco.OAuthAuthzCodeGrant):
		slog.Debug("host advertises login.v1 without an authz_code grant; using the token path", "host", disp)
		oauthClient = nil
	case err != nil:
		var notProvided *disco.ErrServiceNotProvided
		if !errors.As(err, &notProvided) {
			slog.Warn("ignoring unusable login.v1 service", "host", disp, "err", err)
		}
		oauthClient = nil
	}

	var tfeService *url.URL
	if oauthClient == nil {
		tfeService, err = h.ServiceURL("tfe.v2")
		if err != nil {
			return fmt.Errorf("host %s does not support turf authorization tokens", disp)
		}
	}

	// Gather what the consent prompt needs to disclose before showing it.
	_, replacing, err := storedCredential(o.credsPath, o.host)
	if err != nil {
		return err
	}
	if envVar, ok := envTokenVarForHost(o.host); ok {
		fmt.Fprintf(o.out, "\nNote: the environment variable %s is set for\n%s and takes precedence over the credentials file. The token\nsaved by turf login will not be used until you unset it.\n", envVar, disp)
	}

	ok, err := consent(o, oauthClient != nil, replacing)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("login cancelled: the login was not confirmed")
	}

	var token string
	if oauthClient != nil {
		token, err = loginByCode(ctx, o, oauthClient)
	} else {
		token, err = loginByToken(ctx, o, tfeService)
	}
	if err != nil {
		return err
	}

	if err := storeCredential(o.credsPath, o.host, token); err != nil {
		return err
	}

	fmt.Fprintf(o.out, "\nSuccess! turf has obtained and saved an API token.\n\n"+
		"The new API token will be used for any future turf command that must make\nauthenticated requests to %s.\n", disp)
	return nil
}

// loginFromStdin stores a token piped in on stdin. The token is validated
// against the host's tfe.v2 service when it advertises one, so a typo still
// fails loudly, but a host without that service is not a reason to refuse.
func loginFromStdin(ctx context.Context, o loginOptions) error {
	disp := o.host.ForDisplay()

	raw, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("cannot read token from stdin: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return errors.New("no token was given on stdin")
	}

	discoCtx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	if svc, err := o.services.DiscoverServiceURL(discoCtx, o.host, "tfe.v2"); err == nil {
		if _, err := validateTFEToken(ctx, o.httpClient, svc, token); err != nil {
			if errors.Is(err, errTokenInvalid) {
				return fmt.Errorf("%w for %s", errTokenInvalid, disp)
			}
			slog.Warn("could not verify the token before storing it", "host", disp, "err", err)
		}
	} else {
		slog.Debug("host provides no tfe.v2 service; storing the token unverified", "host", disp, "err", err)
	}

	if err := storeCredential(o.credsPath, o.host, token); err != nil {
		return err
	}
	fmt.Fprintf(o.out, "Success! turf has saved an API token for %s.\n", disp)
	return nil
}

// consent discloses where the token will be written and requires a literal
// "yes". This is the one gate before turf writes a plaintext secret to disk, so
// it runs before any browser opens or any token is typed.
func consent(o loginOptions, oauthBased, replacing bool) (bool, error) {
	disp := o.host.ForDisplay()

	if oauthBased {
		fmt.Fprintf(o.out, "\nturf will request an API token for %s using OAuth.\n\n"+
			"This will work only if you are able to use a web browser on this computer to\n"+
			"complete a login process. If not, you must obtain an API token by another\n"+
			"means and configure it in the CLI configuration manually.\n", disp)
	} else {
		fmt.Fprintf(o.out, "\nturf will request an API token for %s using your browser.\n", disp)
	}

	fmt.Fprintf(o.out, "\nIf login is successful, turf will store the token in plain text in\n"+
		"the following file for use by subsequent commands:\n    %s\n", o.credsPath)
	if replacing {
		fmt.Fprintf(o.out, "\nThis will replace the token currently stored for %s.\n", disp)
	}

	answer, err := promptLine(o.out, "\nDo you want to proceed? Only 'yes' will be accepted to confirm: ")
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(answer), "yes"), nil
}

// loginByCode runs the OAuth 2.0 authorization-code flow with PKCE against a
// loopback redirect. Only the access token is kept: the login protocol has no
// notion of refreshing, so a refresh token or expiry in the response is dropped.
func loginByCode(ctx context.Context, o loginOptions, client *disco.OAuthClient) (string, error) {
	verifier := oauth2.GenerateVerifier()
	state, err := randomState()
	if err != nil {
		return "", err
	}

	listener, redirectURI, err := listenerForCallback(ctx, client.MinPort, client.MaxPort)
	if err != nil {
		return "", err
	}

	type outcome struct {
		code string
		err  error
	}
	done := make(chan outcome, 1)
	report := func(code string, err error) {
		select {
		case done <- outcome{code: code, err: err}:
		default: // a result is already in flight; ignore duplicate callbacks
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		// ParseForm so a server that POSTs the response works as well as one
		// that redirects with a query string.
		if err := r.ParseForm(); err != nil {
			writeCallbackPage(w, http.StatusBadRequest, "Login failed", "the authorization response could not be parsed")
			return
		}
		// tofu ignores these and simply hangs until its timeout; reporting the
		// denial turns a ten-minute wait into an immediate, explicable error.
		if errCode := r.Form.Get("error"); errCode != "" {
			detail := errCode
			if desc := r.Form.Get("error_description"); desc != "" {
				detail += ": " + desc
			}
			writeCallbackPage(w, http.StatusBadRequest, "Login failed", detail)
			report("", fmt.Errorf("the host reported an authorization error: %s", detail))
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Form.Get("state")), []byte(state)) != 1 {
			writeCallbackPage(w, http.StatusBadRequest, "Login failed", "state mismatch in the authorization response")
			report("", errors.New("state mismatch in the authorization response"))
			return
		}
		code := r.Form.Get("code")
		if code == "" {
			writeCallbackPage(w, http.StatusBadRequest, "Login failed", "no authorization code in the callback")
			report("", errors.New("no authorization code in the callback"))
			return
		}
		writeCallbackPage(w, http.StatusOK, "Logged in to turf",
			"The login server has returned an authorization code to turf. You can close this tab and return to the terminal.")
		report(code, nil)
	})

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			report("", fmt.Errorf("callback server failed: %w", err))
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Warn("failed to shut down the login callback server", "err", err)
		}
	}()

	cfg := &oauth2.Config{
		ClientID:    client.ID,
		Endpoint:    client.Endpoint(),
		RedirectURL: redirectURI,
		Scopes:      client.Scopes,
	}
	authURL := cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))

	// Print before opening, so the URL is on screen even if the browser fails.
	fmt.Fprintf(o.out, "\nturf must now open a web browser to the login page for %s.\n\n"+
		"If a browser does not open this automatically, open the following URL to proceed:\n    %s\n",
		o.host.ForDisplay(), authURL)
	if err := openBrowser(ctx, authURL); err != nil {
		slog.Warn("failed to open the browser for login", "err", err)
	}
	fmt.Fprintf(o.out, "\nturf will now wait for the host to signal that login was successful.\n")

	var code string
	select {
	case result := <-done:
		if result.err != nil {
			return "", result.err
		}
		code = result.code
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(loginTimeout):
		return "", fmt.Errorf("timed out after %s waiting for the browser login to complete", loginTimeout)
	}

	// client.Endpoint() sets AuthStyleInParams, so client_id travels in the form
	// body rather than an Authorization header — turf is a public client with no
	// secret to present.
	exchangeCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	exchangeCtx = context.WithValue(exchangeCtx, oauth2.HTTPClient, o.httpClient)
	tok, err := cfg.Exchange(exchangeCtx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", fmt.Errorf("failed to obtain an API token: %w", err)
	}
	if tok.AccessToken == "" {
		return "", errors.New("the host returned an empty access token")
	}
	return tok.AccessToken, nil
}

// loginByToken sends the user to the host's token page and reads back what they
// paste. This is the path every real-world host takes today.
func loginByToken(ctx context.Context, o loginOptions, tfeService *url.URL) (string, error) {
	disp := o.host.ForDisplay()
	pageURL := tokensPageURL(tfeService)

	fmt.Fprintf(o.out, "\n%s\n\nturf must now open a web browser to the tokens page for %s.\n\n"+
		"If a browser does not open this automatically, open the following URL to proceed:\n    %s\n",
		loginSeparator, disp, pageURL)
	if err := openBrowser(ctx, pageURL); err != nil {
		slog.Warn("failed to open the browser for the tokens page", "err", err)
	}

	fmt.Fprintf(o.out, "\n%s\n\nGenerate a token using your browser, and copy-paste it into this prompt.\n\n"+
		"turf will store the token in plain text in the following file\nfor use by subsequent commands:\n    %s\n",
		loginSeparator, o.credsPath)

	token, err := promptSecret(o.out, fmt.Sprintf("\nToken for %s: ", disp))
	if err != nil {
		return "", err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("no token was entered")
	}

	username, err := validateTFEToken(ctx, o.httpClient, tfeService, token)
	if err != nil {
		if errors.Is(err, errTokenInvalid) {
			return "", fmt.Errorf("%w for %s", errTokenInvalid, disp)
		}
		return "", fmt.Errorf("failed to retrieve user account details: %w", err)
	}
	fmt.Fprintf(o.out, "\nRetrieved token for user %s\n", username)

	return token, nil
}

// tokensPageURL derives the host's user-token page from its tfe.v2 service URL.
// Only the host is taken: the service path (/api/v2/) addresses the API, while
// the token page lives at a fixed location in the web UI.
func tokensPageURL(tfeService *url.URL) string {
	u := url.URL{
		Scheme:   "https",
		Host:     tfeService.Host,
		Path:     "/app/settings/tokens",
		RawQuery: "source=terraform-login",
	}
	return u.String()
}

// validateTFEToken confirms a token is accepted by the host and returns the
// username it belongs to. This stands in for go-tfe's Users.ReadCurrent, which
// would be a heavy dependency for a single JSON:API request.
func validateTFEToken(ctx context.Context, client *http.Client, base *url.URL, token string) (string, error) {
	endpoint := base.JoinPath("account", "details")

	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.api+json")
	req.Header.Set("Content-Type", "application/vnd.api+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", errTokenInvalid
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected response %s from %s", resp.Status, endpoint)
	}

	var payload struct {
		Data struct {
			Attributes struct {
				Username string `json:"username"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", fmt.Errorf("cannot decode the account details response: %w", err)
	}
	if payload.Data.Attributes.Username == "" {
		return "", errors.New("the account details response contained no username")
	}
	return payload.Data.Attributes.Username, nil
}

// listenerForCallback binds a loopback listener on a port the host is willing to
// redirect to, and returns the redirect URI to advertise.
//
// That URI names "localhost" while the listener binds 127.0.0.1, and its path
// carries no trailing slash. Both quirks are inherited from tofu and are
// load-bearing: hosts register the literal string, so it has to match.
//
// The port range is treated as inclusive of both ends. tofu computes its span as
// max-min, which never selects maxPort and fails outright when the host
// advertises a single port; accepting those cases is a strict superset.
func listenerForCallback(ctx context.Context, minPort, maxPort uint16) (net.Listener, string, error) {
	// disco validates this too, but a panic here would be an awful way to learn
	// that a host advertised a privileged range.
	if minPort < 1024 || maxPort < 1024 {
		return nil, "", fmt.Errorf("the host advertises a login port range %d-%d that includes privileged ports", minPort, maxPort)
	}
	if maxPort < minPort {
		return nil, "", fmt.Errorf("the host advertises an invalid login port range %d-%d", minPort, maxPort)
	}

	span := int(maxPort) - int(minPort) + 1
	tries := span + span/2
	var lc net.ListenConfig
	for range tries {
		port := int(minPort) + mrand.IntN(span)
		// IPv4 loopback specifically: a host that registered a redirect on
		// "localhost" may not resolve it to ::1.
		listener, err := lc.Listen(ctx, "tcp4", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue // most likely already in use; try another
		}
		return listener, fmt.Sprintf("http://localhost:%d%s", port, callbackPath), nil
	}
	return nil, "", fmt.Errorf("no TCP port numbers between %d and %d are available for the login callback", minPort, maxPort)
}

func randomState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("cannot generate random state: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func writeCallbackPage(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>%[1]s</title></head>
<body style="font-family: system-ui, sans-serif; text-align: center; padding-top: 4rem;">
<h1>%[1]s</h1>
<p>%[2]s</p>
</body>
</html>`, html.EscapeString(title), html.EscapeString(detail))
}

// promptSecret reads a line without echoing it when stdin is a terminal.
// The non-terminal branch is not only a fallback for odd environments: it is
// also what lets tests drive the prompt through a pipe with no seam.
func promptSecret(out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	if term.IsTerminal(int(stdin.Fd())) {
		line, err := term.ReadPassword(int(stdin.Fd()))
		fmt.Fprintln(out)
		if err != nil {
			return "", fmt.Errorf("cannot read the token: %w", err)
		}
		return string(line), nil
	}
	return readLine()
}

func promptLine(out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	return readLine()
}

// readLine reads a single line, tolerating a final line with no newline so a
// piped answer without a trailing newline still works.
func readLine() (string, error) {
	line, err := stdinReader().ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", fmt.Errorf("cannot read from stdin: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// newLoginHTTPClient builds the client used for the token exchange and the
// token-validation probe.
//
// Deliberately not cagent's SSRF-safe client: that one refuses to dial private
// addresses, which would break `turf login tfe.corp.internal`. The hostname here
// comes from the user's own command line, not from an attacker-supplied
// document, so the threat that client defends against does not apply.
func newLoginHTTPClient() *http.Client {
	return &http.Client{Timeout: httpTimeout}
}

func newLoginDisco() *disco.Disco {
	return disco.New(disco.WithHTTPClient(newLoginHTTPClient()))
}
