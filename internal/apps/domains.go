package apps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// HostnameRe is the loose client-side gate for a customer hostname: DNS
// labels joined by dots, at least two of them. deploy-api's validateHostname
// is the authority (it also refuses wildcards, IPs and the platform's own
// domains); this only stops obvious typos before a request is issued.
var HostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// NormalizeHostname mirrors deploy-api's appdomains.NormalizeHostname:
// lower-cased, trimmed, trailing dot dropped.
func NormalizeHostname(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

// DNSInstruction is the record the customer creates at their registrar.
type DNSInstruction struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Target string `json:"target"`
	Record string `json:"record"`
}

// Domain is one custom hostname as GET/POST /deployments/{alias}/domains
// shows it. Status and SSLStatus are Cloudflare's words; Active is the only
// pair that means the hostname serves.
type Domain struct {
	ID              string         `json:"id"`
	DeploymentAlias string         `json:"deployment_alias"`
	Hostname        string         `json:"hostname"`
	Status          string         `json:"status"`
	SSLStatus       string         `json:"ssl_status"`
	Active          bool           `json:"active"`
	Errors          []string       `json:"errors,omitempty"`
	DNS             DNSInstruction `json:"dns"`
	IsApex          bool           `json:"is_apex"`
	ApexAdvice      string         `json:"apex_advice,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// DomainsListResponse is GET /deployments/{alias}/domains.
type DomainsListResponse struct {
	Domains     []Domain `json:"domains"`
	CNAMETarget string   `json:"cname_target"`
	ApexAdvice  string   `json:"apex_advice"`
}

func domainsURL(apiURL, alias string) string {
	return fmt.Sprintf("%s/api/deploy/deployments/%s/domains", strings.TrimSuffix(apiURL, "/"), alias)
}

// ListDomains returns the app's custom hostnames. The server refreshes every
// row's status from Cloudflare on the way out, so this is also how a single
// hostname is verified — see FindDomain.
func ListDomains(apiURL, apiToken, alias string) (*DomainsListResponse, []byte, error) {
	status, body, err := doChecksRequest(http.MethodGet, domainsURL(apiURL, alias), apiToken, nil)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, body, statusError(status, body)
	}
	var out DomainsListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, body, fmt.Errorf("decode response: %w (body=%s)", err, string(body))
	}
	return &out, body, nil
}

// AddDomain registers hostname on the app (POST). 201 is a new row, 200 the
// owner's own row handed back refreshed; both are a success to the caller.
func AddDomain(apiURL, apiToken, alias, hostname string) (*Domain, []byte, error) {
	status, body, err := doChecksRequest(http.MethodPost, domainsURL(apiURL, alias), apiToken,
		map[string]string{"hostname": hostname})
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return nil, body, statusError(status, body)
	}
	var out Domain
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, body, fmt.Errorf("decode response: %w (body=%s)", err, string(body))
	}
	return &out, body, nil
}

// RemoveDomain unregisters hostname from the app (DELETE, 204).
func RemoveDomain(apiURL, apiToken, alias, hostname string) error {
	status, body, err := doChecksRequest(http.MethodDelete,
		domainsURL(apiURL, alias)+"/"+url.PathEscape(hostname), apiToken, nil)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && status != http.StatusOK {
		return statusError(status, body)
	}
	return nil
}

// FindDomain returns the hostname's row out of a listing, or nil.
func (r *DomainsListResponse) FindDomain(hostname string) *Domain {
	for i := range r.Domains {
		if r.Domains[i].Hostname == hostname {
			return &r.Domains[i]
		}
	}
	return nil
}

// Verdict renders the Cloudflare status pair as one line a person can act
// on: "active", "waiting for DNS", "issuing certificate", or the failure.
// Cloudflare's hostname status is pending → active (or moved/deleted/
// blocked); the SSL status walks initializing → pending_validation →
// pending_issuance → pending_deployment → active. Anything with "pending"
// or "initializing" in it is Cloudflare waiting; the CNAME is what it waits
// for until validation has passed.
func (d *Domain) Verdict() string {
	if d.Active {
		return "active — serving with a valid certificate"
	}
	st, ssl := d.Status, d.SSLStatus
	switch {
	case st == "missing" || ssl == "missing":
		return "missing at Cloudflare — remove and add the domain again"
	case st == "blocked" || st == "moved" || st == "deleted" || strings.Contains(ssl, "error") || strings.Contains(ssl, "timed_out") || strings.Contains(ssl, "expired"):
		return fmt.Sprintf("error (%s / ssl %s)", st, ssl)
	case ssl == "pending_deployment" || ssl == "pending_issuance":
		return "issuing certificate — the CNAME resolves; Cloudflare is finishing the certificate (usually a minute or two)"
	case st == "pending" || ssl == "pending_validation" || ssl == "initializing" || ssl == "":
		return fmt.Sprintf("waiting for DNS — create the CNAME %s → %s, then run verify again", d.DNS.Name, d.DNS.Target)
	}
	return fmt.Sprintf("%s / ssl %s", st, ssl)
}
