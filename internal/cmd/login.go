package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/auth"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/contextcfg"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
	"github.com/dibbla-agents/dibbla-cli/internal/env"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
)

var (
	loginAPIKey     string
	loginBrowser    bool
	loginAPIURL     string
	loginWriteEnv   bool
	loginNoKeychain bool
	loginContext    string
	loginNoSwitch   bool
)

// apiKeysPath is the page in the dibbla web app where users mint API tokens,
// relative to that instance's app URL. Displayed whenever we direct the user to
// --api-key. The route is "/api-keys" (defined in auth-ui/src/App.tsx) —
// earlier copies of this CLI pointed at /settings/api-tokens, which has never
// existed.
const apiKeysPath = "/api-keys"

// apiKeysURLFor returns the token page on the instance being logged in to,
// rather than always Dibbla's production portal.
//
// Telling someone logging in to api.haja.fatshark.se to mint their token at
// app.dibbla.com is not a cosmetic wrong link: it sends them to a different
// company's product to create a credential that would not work. auth.DeriveAppURL
// is the same api.->app. rewrite the browser flow already uses, so the two
// cannot disagree about where an instance's UI lives.
func apiKeysURLFor(apiBaseURL string) string {
	if apiBaseURL == "" || apiBaseURL == config.DefaultAPIURL {
		return config.DefaultAppURL + apiKeysPath
	}
	if derived, err := auth.DeriveAppURL(apiBaseURL); err == nil && derived != "" {
		return derived + apiKeysPath
	}
	// An instance whose app URL cannot be derived gets no guess: a wrong URL
	// is worse than none, and the surrounding messages still name --api-key.
	return ""
}

// mintTokenAt renders "create one at <url>" only when there is a URL worth
// naming, so a non-derivable instance does not print a dangling sentence.
func mintTokenAt(apiBaseURL string) string {
	if u := apiKeysURLFor(apiBaseURL); u != "" {
		return "create one at " + u
	}
	return "create one in that instance's web UI, under API keys"
}

var loginCmd = &cobra.Command{
	Use:   "login [api_url]",
	Short: "Log in and store API credentials securely",
	Long: `Authenticate with the Dibbla API and store your token in the OS credential store.

By default uses https://api.dibbla.com. To target a different endpoint:
  dibbla login api.dibbla.net                  # positional arg
  dibbla login --api-url https://api.dibbla.net  # --api-url flag (mutually exclusive with positional)

Three ways to provide the token:
  (interactive)        Run in a real terminal; pick "Log in with browser" or "Paste an API token".
                       Over SSH the picker auto-routes to "Paste an API token" — see below.
  --browser            Skip the interactive menu and go straight to browser-based OAuth.
                       Works in non-TTY contexts (Claude Code ! prefix, scripted shells,
                       agentic tooling) when this machine has a local browser. Refuses over
                       SSH: the OAuth callback uses a localhost server on this host, which
                       the laptop's browser cannot reach. Use --api-key instead.
  --api-key <token>    Provide a pre-generated token; works in any context.

Persistence:
  (default)            Token + URL stored in the OS keyring (macOS Keychain, Windows
                       Credential Manager, libsecret/pass on Linux).
  --write-env          Also write DIBBLA_API_TOKEN + DIBBLA_API_URL to ./.env in the
                       current directory (atomic, preserves existing keys/comments) and
                       ensure .env is listed in ./.gitignore.
  --no-keychain        Skip the OS keyring entirely — validate only. Useful on cloud
                       VMs / SSH / Docker where libsecret/gnome-keyring isn't
                       installed. Combine with --write-env to persist credentials
                       to the project's .env instead.

In CI, set DIBBLA_API_TOKEN (and optionally DIBBLA_API_URL) in the shell environment or
./.env — the CLI reads both, and login is not required.`,
	Args: cobra.MaximumNArgs(1),
	Run:  runLogin,
}

func init() {
	loginCmd.Flags().StringVar(&loginAPIKey, "api-key", "", "API token (if omitted, you will be prompted)")
	loginCmd.Flags().BoolVar(&loginBrowser, "browser", false, "Use browser-based OAuth directly; works in non-TTY contexts (Claude Code, agentic tools)")
	loginCmd.Flags().StringVar(&loginAPIURL, "api-url", "", "API endpoint URL (alternative to the positional arg; mutually exclusive with it)")
	loginCmd.Flags().BoolVar(&loginWriteEnv, "write-env", false, "After validation, write DIBBLA_API_TOKEN + DIBBLA_API_URL to ./.env and ensure .env is in ./.gitignore")
	loginCmd.Flags().BoolVar(&loginNoKeychain, "no-keychain", false, "Do not persist credentials to the OS keyring — useful on cloud VMs / SSH where keyring services are not installed")
	// NOTE: this shadows the root persistent --context flag, deliberately.
	// Theirs names the context to READ; this one names the context to WRITE.
	// So `dibbla login --context x` adds or refreshes context x — it does not
	// set a read override for this invocation.
	loginCmd.Flags().StringVar(&loginContext, "context", "", "Name the context to create or refresh (default: derived from the API URL, or the existing context with that URL)")
	loginCmd.Flags().BoolVar(&loginNoSwitch, "no-switch", false, "Store the login without making it the context in use")
}

func runLogin(cmd *cobra.Command, args []string) {
	baseURL, err := resolveLoginBaseURL(args)
	if err != nil {
		fmt.Printf("%s Error: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}
	if baseURL == "" {
		fmt.Printf("%s Error: Invalid API URL\n", platform.Icon("❌", "[X]"))
		os.Exit(1)
	}

	token := strings.TrimSpace(loginAPIKey)
	if token == "" && loginBrowser {
		// Over SSH the localhost-callback browser flow can't complete —
		// the callback URL points at this host's loopback, not the
		// user's laptop. Refuse with a useful pointer instead of
		// hanging for 5 minutes on a callback that will never arrive.
		if auth.IsSSHSession() {
			fmt.Printf("%s --browser cannot complete login over SSH.\n"+
				"  The OAuth callback uses a localhost server on this host,\n"+
				"  which your laptop's browser cannot reach. Use either:\n\n"+
				"    dibbla login --api-key <token>      (%s)\n"+
				"    DIBBLA_API_TOKEN=<token> dibbla ... (any subsequent dibbla command)\n",
				platform.Icon("❌", "[X]"), mintTokenAt(baseURL))
			os.Exit(1)
		}
		// Skip the interactive survey menu — go directly to browser OAuth.
		// Safe in non-TTY contexts because the browser flow uses a localhost
		// callback server for token delivery, not stdin.
		t, err := browserLogin(baseURL)
		if err != nil {
			fmt.Printf("%s Error: %v\n", platform.Icon("❌", "[X]"), err)
			os.Exit(1)
		}
		token = strings.TrimSpace(t)
	}
	if token == "" {
		var err error
		token, err = acquireToken(baseURL)
		if err != nil {
			fmt.Printf("%s Error: %v\n", platform.Icon("❌", "[X]"), err)
			os.Exit(1)
		}
		token = strings.TrimSpace(token)
		if token == "" {
			fmt.Printf("%s Error: API token is required\n", platform.Icon("❌", "[X]"))
			os.Exit(1)
		}
	}

	if err := apiclient.ValidateToken(baseURL, token); err != nil {
		if apiErr, ok := err.(*apiclient.APIError); ok {
			fmt.Printf("%s Error: %s\n", platform.Icon("❌", "[X]"), apiErr.Message)
			os.Exit(apiclient.ExitCodeForStatus(apiErr.StatusCode))
		}
		fmt.Printf("%s Error: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	usedFileFallback := false
	ctxName := ""
	switched := false
	if !loginNoKeychain {
		var err error
		ctxName, usedFileFallback, switched, err = storeLoginAsContext(baseURL, token)
		if err != nil {
			fmt.Printf("%s Error: %v\n", platform.Icon("❌", "[X]"), err)
			os.Exit(1)
		}
		if usedFileFallback {
			fmt.Printf("%s OS keyring unavailable on this host (no org.freedesktop.secrets).\n"+
				"  Stored credentials in %s instead.\n",
				platform.Icon("⚠", "[!]"), credential.ContextTokenFilePath(ctxName))
		}
	}

	if loginWriteEnv {
		if err := writeEnvAndGitignore(token, baseURL); err != nil {
			fmt.Printf("%s Error: %v\n", platform.Icon("❌", "[X]"), err)
			os.Exit(1)
		}
	}

	switch {
	case loginNoKeychain && loginWriteEnv:
		fmt.Printf("%s Validated %s (keychain skipped, credentials in .env)\n", platform.Icon("✅", "[OK]"), baseURL)
	case loginNoKeychain:
		fmt.Printf("%s Validated %s (keychain skipped — re-run with --write-env to persist, or re-run without --no-keychain)\n", platform.Icon("✅", "[OK]"), baseURL)
	case usedFileFallback && loginWriteEnv:
		fmt.Printf("%s Logged in to %s as context %s (credentials in %s and .env)\n",
			platform.Icon("✅", "[OK]"), baseURL, ctxName, credential.ContextTokenFilePath(ctxName))
	case usedFileFallback:
		fmt.Printf("%s Logged in to %s as context %s (credentials in %s)\n",
			platform.Icon("✅", "[OK]"), baseURL, ctxName, credential.ContextTokenFilePath(ctxName))
	case loginWriteEnv:
		fmt.Printf("%s Logged in to %s as context %s (credentials also written to .env)\n",
			platform.Icon("✅", "[OK]"), baseURL, ctxName)
	default:
		fmt.Printf("%s Logged in to %s as context %s\n", platform.Icon("✅", "[OK]"), baseURL, ctxName)
	}
	if ctxName != "" && !switched {
		fmt.Printf("%s Left in place: the context in use is unchanged. Switch with `dibbla context use %s`.\n",
			platform.Icon("ℹ", "[i]"), ctxName)
	}

	// If env vars are shadowing the just-saved credentials (the classic
	// stale-tmux-env footgun), warn now — at the moment of creation —
	// rather than letting the user discover it on the next 401. We
	// suppress when --no-keychain was used without --write-env: in that
	// case nothing was actually persisted to be shadowed and the hint's
	// "saved credentials" wording would be misleading.
	if !(loginNoKeychain && !loginWriteEnv) {
		if hint := apiclient.AuthShadowHint(); hint != "" {
			fmt.Printf("%s %s\n", platform.Icon("⚠", "[!]"), hint)
		}
	}
}

// writeEnvAndGitignore persists DIBBLA_API_TOKEN + DIBBLA_API_URL into
// ./.env and ensures ./.gitignore lists .env. A failure to patch .gitignore
// is warned about but does not fail the command — the .env is already
// safely written and the command's primary job (materializing credentials)
// has succeeded.
func writeEnvAndGitignore(token, baseURL string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	envPath := filepath.Join(wd, ".env")
	gitignorePath := filepath.Join(wd, ".gitignore")

	written, err := env.MergeEnvFile(envPath, map[string]string{
		"DIBBLA_API_TOKEN": token,
		"DIBBLA_API_URL":   baseURL,
	})
	if err != nil {
		return fmt.Errorf("write %s: %w", envPath, err)
	}
	fmt.Printf("%s Wrote %s to %s\n", platform.Icon("✅", "[OK]"), strings.Join(written, ", "), envPath)

	modified, gerr := env.EnsureGitignoreEntry(gitignorePath)
	if gerr != nil {
		fmt.Printf("%s Warning: failed to update %s: %v\n", platform.Icon("⚠️", "[!]"), gitignorePath, gerr)
		return nil
	}
	if modified {
		fmt.Printf("%s Added .env to %s\n", platform.Icon("✅", "[OK]"), gitignorePath)
	}
	return nil
}

// acquireToken presents the user with a choice of login methods and returns an API token.
func acquireToken(baseURL string) (string, error) {
	interactive := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	if !interactive {
		// Tailor the recovery options to context. Over SSH, --browser
		// is not a viable suggestion (the callback can't reach the
		// user's laptop) — leave it out so we don't lead the user
		// into a 5-minute timeout.
		if auth.IsSSHSession() {
			return "", fmt.Errorf("non-interactive SSH session detected. Use one of:\n"+
				"  --api-key TOK     pass a token (%s)\n"+
				"  env DIBBLA_API_TOKEN=...   for headless CI", mintTokenAt(baseURL))
		}
		return "", fmt.Errorf("non-interactive terminal detected. Use one of:\n"+
			"  --browser         opens your browser (works in Claude Code, agentic shells, CI with a browser)\n"+
			"  --api-key TOK     pass a token (%s)\n"+
			"  env DIBBLA_API_TOKEN=...   for headless CI", mintTokenAt(baseURL))
	}

	// Over SSH, browser login can't complete (localhost callback can't
	// reach the user's laptop). Skip the picker and route to the
	// paste-token flow with a one-line explanation so the user knows
	// why their usual choice isn't being offered.
	if auth.IsSSHSession() {
		fmt.Printf("%s SSH session detected — browser login isn't viable here.\n"+
			"  The OAuth callback uses a localhost server on this host,\n"+
			"  which your laptop's browser can't reach. Paste an API\n"+
			"  token instead (%s).\n\n",
			platform.Icon("ℹ", "[i]"), mintTokenAt(baseURL))
		return promptAPIToken(baseURL)
	}

	const (
		optBrowser  = "Log in with browser"
		optAPIToken = "Paste an API token"
	)

	var method string
	prompt := &survey.Select{
		Message: "How would you like to log in?",
		Options: []string{optBrowser, optAPIToken},
	}
	if err := survey.AskOne(prompt, &method); err != nil {
		return "", err
	}

	switch method {
	case optBrowser:
		return browserLogin(baseURL)
	default:
		return promptAPIToken(baseURL)
	}
}

// browserLogin performs the browser-based OAuth login flow.
func browserLogin(apiBaseURL string) (string, error) {
	// Derive the app URL for the auth UI.
	appURL := config.DefaultAppURL
	if apiBaseURL != config.DefaultAPIURL {
		derived, err := auth.DeriveAppURL(apiBaseURL)
		if err != nil {
			return "", fmt.Errorf("cannot determine app URL for %s: %w\nUse 'Paste an API token' instead", apiBaseURL, err)
		}
		appURL = derived
	}

	state, err := auth.GenerateState()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	port, resultCh, shutdown := auth.StartCallbackServer(ctx, state)
	defer shutdown()

	loginURL := auth.BuildLoginURL(appURL, port, state)

	if auth.HasGraphicalSession() {
		fmt.Printf("%s Opening browser for login...\n", platform.Icon("🌐", "[>]"))
		if err := auth.OpenBrowser(loginURL); err != nil {
			// Browser didn't open — try clipboard, then print URL.
			if clipErr := auth.CopyToClipboard(loginURL); clipErr == nil {
				fmt.Printf("  %s Login URL copied to clipboard!\n", platform.Icon("📋", "[*]"))
			}
			fmt.Printf("  If the browser didn't open, visit:\n  %s\n", loginURL)
		}
	} else {
		// No $DISPLAY / $WAYLAND_DISPLAY: xdg-open would silently fail
		// (cmd.Start returns nil but the child dies after fork). Print
		// the URL as the primary instruction. Note this still won't
		// work over SSH — the localhost callback at port %d is on
		// this host, not the laptop — but the SSH gate upstream
		// already refuses that case, so reaching here implies a
		// non-SSH headless context where the user knows what they're
		// doing (e.g. a desktop session that lost its display).
		fmt.Printf("%s No graphical session detected. Open this URL in a browser\n"+
			"  on a machine that can reach 127.0.0.1:%d :\n\n  %s\n",
			platform.Icon("🌐", "[>]"), port, loginURL)
	}

	fmt.Println()
	fmt.Println("Waiting for browser login... (press Ctrl+C to cancel)")

	result := <-resultCh
	if result.Err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("login timed out after 5 minutes; try again or use --api-key")
		}
		return "", result.Err
	}

	fmt.Printf("%s Browser login successful! Creating API token...\n", platform.Icon("✅", "[OK]"))

	apiToken, err := auth.ExchangeJWTForAPIToken(apiBaseURL, result.Token)
	if err != nil {
		return "", fmt.Errorf("failed to create API token: %w", err)
	}

	// Linger briefly so the browser can finish loading the success page
	// (favicon, reflexive reload) before defer shutdown() above tears
	// down the listener. The grace timer inside StartCallbackServer
	// alone is bypassed: defer shutdown() runs as soon as this function
	// returns — right after token exchange, ~1s — well before the
	// timer fires. Long term: switch to OAuth Device Authorization Flow
	// to eliminate the localhost callback entirely.
	time.Sleep(auth.CallbackGracePeriod)

	return apiToken, nil
}

// resolveLoginBaseURL picks the API URL to validate against, in order:
//  1. --api-url flag OR positional arg (explicit; mutually exclusive — error if both).
//  2. DIBBLA_API_URL env var.
//  3. DIBBLA_AUTH_SERVICE_URL env var (the name used by the dibbla-tasks
//     steprunner when injecting env into subprocesses — ensures `dibbla
//     login` invoked from inside a task file targets the same service
//     the parent CLI is logged into).
//  4. the ACTIVE CONTEXT's API URL, when there is one.
//  5. config.DefaultAPIURL.
//
// Step 4 is new with named contexts and fixes a sharp edge that predates them:
// a bare `dibbla login` while working against a customer instance used to fall
// straight through to production, silently re-targeting the user mid-session
// and — before contexts — destroying the credential they were using. Preferring
// the context they are actually on is what a re-login means.
//
// This is not circular the way reading back the token would be. --api-url and
// the positional argument still win, so setting a new target is unaffected;
// this only decides what "no target given" means, and "the one I am on" is a
// better answer than "production".
func resolveLoginBaseURL(args []string) (string, error) {
	flagURL := strings.TrimSpace(loginAPIURL)
	var posURL string
	if len(args) > 0 {
		posURL = strings.TrimSpace(args[0])
	}
	if flagURL != "" && posURL != "" {
		return "", fmt.Errorf("cannot specify both positional api_url and --api-url flag")
	}
	if flagURL != "" {
		return normalizeAPIURL(flagURL), nil
	}
	if posURL != "" {
		return normalizeAPIURL(posURL), nil
	}
	if u := strings.TrimSpace(os.Getenv("DIBBLA_API_URL")); u != "" {
		return normalizeAPIURL(u), nil
	}
	if u := strings.TrimSpace(os.Getenv("DIBBLA_AUTH_SERVICE_URL")); u != "" {
		return normalizeAPIURL(u), nil
	}
	if r := config.ResolveContext(); r.Err == nil && r.APIURL != "" {
		return normalizeAPIURL(r.APIURL), nil
	}
	return config.DefaultAPIURL, nil
}

// normalizeAPIURL returns a full https URL. Accepts "api.dibbla.net" or "https://api.dibbla.net".
func normalizeAPIURL(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	if !strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://") {
		return "https://" + input
	}
	return strings.TrimSuffix(input, "/")
}

func promptAPIToken(apiBaseURL string) (string, error) {
	var token string
	help := "Get your token in that instance's web UI, under API keys"
	if u := apiKeysURLFor(apiBaseURL); u != "" {
		help = "Get your token at " + u
	}
	prompt := &survey.Password{
		Message: "API token:",
		Help:    help,
	}
	err := survey.AskOne(prompt, &token)
	return token, err
}

// storeLoginAsContext persists a successful login as a named context instead of
// overwriting the CLI's single credential slot. This is the change P-0011
// exists to make: logging in to a second server no longer destroys the first.
//
// Naming, in order:
//   - --context <name>, when given. The name is validated, because it becomes
//     both a keyring key and a filename.
//   - the existing context with this API URL, if there is one. Keying on URL is
//     what makes a re-login a refresh rather than a second entry for the same
//     server.
//   - a name derived from the host (api.haja.fatshark.se -> "haja", the default
//     endpoint -> "prod"), disambiguated if taken. Names are renameable, so a
//     slightly ugly derived name is never a trap.
//
// Returns the context name, whether the keyring-less file fallback was used,
// and whether this login became the context in use.
func storeLoginAsContext(baseURL, token string) (name string, usedFile, switched bool, err error) {
	store, err := contextcfg.Load()
	if err != nil {
		return "", false, false, err
	}

	switch {
	case strings.TrimSpace(loginContext) != "":
		name = strings.TrimSpace(loginContext)
		if !contextcfg.ValidName(name) {
			return "", false, false, fmt.Errorf("%q is not a usable context name: use letters, digits, dot, dash or underscore (it becomes a filename and a keyring key)", name)
		}
	case store.FindByURL(baseURL) != "":
		name = store.FindByURL(baseURL)
	default:
		name = contextcfg.UniqueName(contextcfg.DeriveName(baseURL, config.DefaultAPIURL), store.Contexts)
	}

	// The token first, so config.yaml never names a context whose credential
	// has not landed.
	if serr := credential.SetContextToken(name, token); serr != nil {
		if !credential.IsKeyringUnavailable(serr) {
			return name, false, false, fmt.Errorf("token validated but could not be stored: %w", serr)
		}
		// Linux SSH / cloud VM / Docker without libsecret. Fall back to this
		// context's own credentials file, which mirrors keychain semantics
		// (machine-wide, persists across `cd`) rather than the cwd-bound
		// --write-env behaviour.
		if ferr := credential.SetContextTokenFile(name, token, baseURL); ferr != nil {
			return name, false, false, fmt.Errorf("OS keyring unavailable on this host AND the file fallback failed: %w", ferr)
		}
		usedFile = true
	}

	existing, existed := store.Get(name)
	ctx := contextcfg.Context{APIURL: baseURL}
	if existed && strings.TrimSuffix(existing.APIURL, "/") == strings.TrimSuffix(baseURL, "/") {
		// A refresh of the same server keeps that context's organization pin:
		// re-authenticating is not a request to change which org you act as.
		ctx.Org, ctx.OrgName = existing.Org, existing.OrgName
	}
	// A context being re-pointed at a DIFFERENT server drops its pin, because
	// an organization id from the old server means nothing on the new one.
	store.Set(name, ctx)

	if !loginNoSwitch {
		store.Current = name
		switched = true
	}
	if serr := store.Save(); serr != nil {
		return name, usedFile, switched, serr
	}
	if switched {
		// Repoint the legacy single-slot storage, so a dibbla binary older than
		// contexts — and every script that sources credentials.env — follows
		// this login rather than staying on the previous server.
		config.SyncLegacyMirror()
	}
	return name, usedFile, switched, nil
}
