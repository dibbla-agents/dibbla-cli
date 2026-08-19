package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// SubdomainURL rewrites the leading "api." label of a Dibbla API URL to a
// sibling label, e.g. ("https://api.dibbla.com", "ai") → "https://ai.dibbla.com".
// Scheme and port are kept; path, query and fragment are dropped.
//
// The platform convention is that companion services live next to the API on
// the same parent domain, so this substitution is the whole rule. Anything not
// matching it is rejected rather than guessed at, so callers can say "set the
// URL explicitly" instead of silently pointing somewhere wrong.
func SubdomainURL(apiURL, label string) (string, error) {
	apiURL = strings.TrimSpace(apiURL)
	if apiURL == "" {
		return "", fmt.Errorf("empty")
	}
	if !strings.HasPrefix(apiURL, "http://") && !strings.HasPrefix(apiURL, "https://") {
		apiURL = "https://" + apiURL
	}
	u, err := url.Parse(apiURL)
	if err != nil {
		return "", fmt.Errorf("not a URL: %w", err)
	}
	host := u.Hostname()
	if !strings.HasPrefix(host, "api.") {
		return "", fmt.Errorf("host %q does not start with \"api.\"", host)
	}
	newHost := label + "." + strings.TrimPrefix(host, "api.")
	if port := u.Port(); port != "" {
		newHost = newHost + ":" + port
	}
	return u.Scheme + "://" + newHost, nil
}

// GatewayURL resolves the AI gateway base URL for a given API URL:
// DIBBLA_AI_GATEWAY_URL when set (matching what deployed pods see), otherwise
// the api. → ai. sibling. Returns "" when neither applies.
func GatewayURL(apiURL string) string {
	if v := strings.TrimSpace(os.Getenv("DIBBLA_AI_GATEWAY_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	derived, err := SubdomainURL(apiURL, "ai")
	if err != nil {
		return ""
	}
	return derived
}
