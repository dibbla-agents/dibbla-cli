package apiclient

import (
	"os"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/credential"
)

// AuthShadowHint returns a non-empty hint string when the CLI's
// resolved auth came (or will come) from environment variables that
// differ from what `dibbla login` has saved on this host. The classic
// trigger: a stale DIBBLA_API_TOKEN inherited from a tmux server's
// environment shadows freshly-saved credentials, and the user can't
// figure out why their fresh login isn't taking effect.
//
// Returns "" when no shadowing is detected. Designed to be called
// freely from error paths and login success paths — it never errors
// and short-circuits cheaply when no env vars are set.
//
// The hint names exactly the env vars that diverge from the saved
// values, plus an `unset ...` command the user can copy/paste.
func AuthShadowHint() string {
	envToken := strings.TrimSpace(os.Getenv("DIBBLA_API_TOKEN"))
	envURL := strings.TrimSpace(os.Getenv("DIBBLA_API_URL"))
	envAuthURL := strings.TrimSpace(os.Getenv("DIBBLA_AUTH_SERVICE_URL"))
	if envToken == "" && envURL == "" && envAuthURL == "" {
		return ""
	}

	// Mirror config.Load's resolution: keyring first, then user-level
	// credentials file. We need to know what `dibbla login` saved so we
	// can check whether env shadows it. If neither store has anything,
	// there's nothing to be shadowed and the hint is irrelevant.
	savedToken, savedURL := readSavedCreds()
	if savedToken == "" {
		return ""
	}

	var diffs []string
	if envToken != "" && envToken != savedToken {
		diffs = append(diffs, "DIBBLA_API_TOKEN")
	}
	if envURL != "" && savedURL != "" && trimURL(envURL) != trimURL(savedURL) {
		diffs = append(diffs, "DIBBLA_API_URL")
	}
	// DIBBLA_AUTH_SERVICE_URL is only consulted when DIBBLA_API_URL is
	// unset — flag it only in that case to avoid noise when the user
	// has both set with the API URL winning.
	if envURL == "" && envAuthURL != "" && savedURL != "" && trimURL(envAuthURL) != trimURL(savedURL) {
		diffs = append(diffs, "DIBBLA_AUTH_SERVICE_URL")
	}
	if len(diffs) == 0 {
		return ""
	}
	joined := strings.Join(diffs, " ")
	pretty := strings.Join(diffs, " and ")
	return "Hint: your shell has " + pretty + " set, which overrides the credentials saved by `dibbla login`. " +
		"Run `unset " + joined + "` (or open a fresh shell) to use the saved credentials."
}

func readSavedCreds() (token, url string) {
	if t, err := credential.GetToken(); err == nil && t != "" {
		token = t
		if u, err := credential.GetAPIURL(); err == nil {
			url = u
		}
	} else if t, u, err := credential.GetTokenFile(); err == nil && t != "" {
		token = t
		url = u
	}
	// `dibbla login` stores an empty URL when the user logs into the
	// default API endpoint (see login.go's `fileURL := ""` branch), so
	// "empty" really means "default". Normalize so the env-vs-saved
	// comparison fires correctly when env DIBBLA_API_URL points at a
	// non-default host.
	if token != "" && url == "" {
		url = config.DefaultAPIURL
	}
	return token, url
}

func trimURL(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimSuffix(u, "/")
	u = strings.TrimRight(u, "\x00")
	return u
}
