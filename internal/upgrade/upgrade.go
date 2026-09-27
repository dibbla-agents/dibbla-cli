// Package upgrade is `dibbla upgrade` (DIB-1047): a Stripe Checkout link for
// the org's Business plan, printed in the terminal — the same one-click path
// an agent gets from platform_whoami upgrade=true (DIB-1046).
//
// It uses the console's own backend: auth-service's
// POST /api/v1/portal/billing/checkout, reached through the gateway at
// /api/auth/v1/... with the CLI's token. The gateway injects the caller's role
// in the org named by X-Org-ID and the route is owner/admin only. auth-service
// refuses Enterprise, externally billed and already-Business organizations
// with the same codes the console and the agent path carry, so all three say
// the same thing.
//
// Nothing changes in the platform until the payment goes through: the only
// effect of a call is a Checkout session in Stripe, which expires unused.
package upgrade

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const checkoutPath = "/api/auth/v1/portal/billing/checkout"

// Reasons a link is not handed out. The first four are auth-service's codes
// (handlers/billing.go) and the agent path's; ROLE_REQUIRED is what a member
// or viewer is told, BILLING_DISABLED an install without card payments.
const (
	RoleRequired      = "ROLE_REQUIRED"
	AlreadySubscribed = "ALREADY_SUBSCRIBED"
	EnterprisePlan    = "ENTERPRISE_PLAN"
	ExternallyBilled  = "EXTERNALLY_BILLED"
	BillingDisabled   = "BILLING_DISABLED"
)

// offers is the plain words per reason, the agent path's (mcp-server
// platform_whoami_upgrade.go) with the CLI named where it names the agent.
var offers = map[string]string{
	RoleRequired:      "Only an owner or admin of %s can upgrade its plan. Ask one of them to upgrade — they can run dibbla upgrade, ask their own agent for the payment link, or use Org settings → Plan in the Dibbla console.",
	AlreadySubscribed: "%s is already on Business, so there is nothing to upgrade. Cards, invoices and cancellation are handled under Org settings → Plan → Manage billing in the Dibbla console.",
	EnterprisePlan:    "%s is on an Enterprise agreement, which is not changed through a payment link. Contact Dibbla to change the plan.",
	ExternallyBilled:  "%s is billed by Dibbla directly rather than by card, so a payment link would charge it twice. Contact Dibbla to change the plan.",
	BillingDisabled:   "This Dibbla installation does not take card payments, so there is no payment link. Contact whoever runs it about the plan.",
}

// Status values, as platform_whoami upgrade=true names them.
const (
	StatusReady        = "checkout_ready"
	StatusNotAvailable = "not_available"
)

// Result is the answer `dibbla upgrade` prints, and its --json shape.
type Result struct {
	Status      string `json:"status"`
	CheckoutURL string `json:"checkout_url,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Message     string `json:"message"`
	OrgID       string `json:"org_id"`
	OrgName     string `json:"org_name,omitempty"`
}

// Ready is the answer with a link.
func Ready(orgID, orgName, checkoutURL string) Result {
	return Result{
		Status:      StatusReady,
		CheckoutURL: checkoutURL,
		OrgID:       orgID,
		OrgName:     orgName,
		Message: fmt.Sprintf("Here is your upgrade link — Business for %s, paid by card in Stripe's secure checkout, VAT included; "+
			"Stripe shows the exact amount in your currency before you pay.\n\n"+
			"Nothing changes until the payment goes through. Right after it does, deploys work again and the trial countdown is gone; "+
			"your apps and data are exactly as they are now. The link is personal to this organization and expires in 24 hours.",
			displayName(orgName)),
	}
}

// NotAvailable is the answer without a link, for one of the reasons above.
func NotAvailable(orgID, orgName, reason string) Result {
	msg := offers[reason]
	if strings.Contains(msg, "%s") {
		msg = fmt.Sprintf(msg, displayName(orgName))
	}
	return Result{Status: StatusNotAvailable, Reason: reason, Message: msg, OrgID: orgID, OrgName: orgName}
}

func displayName(orgName string) string {
	if orgName == "" {
		return "this organization"
	}
	return orgName
}

// IsAdmin reports whether an org role may upgrade: owner or admin. The server
// decides again; this only spares a member the round trip.
func IsAdmin(role string) bool {
	return role == "owner" || role == "admin"
}

// Error is a failure that is not an answer: the call did not get as far as a
// yes or a reason. Nothing changed either way.
type Error struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *Error) Error() string { return e.Message }

// requestTimeout covers Stripe on the far side of auth-service.
const requestTimeout = 30 * time.Second

// Checkout asks for a Checkout session for orgID. It returns the https URL,
// or a reason (one of the constants above) when the server answered that
// there is no link for this org, or an *Error.
func Checkout(apiURL, token, orgID string) (checkoutURL, reason string, err error) {
	body, _ := json.Marshal(map[string]string{"reviewed_organization_id": orgID})
	req, err := http.NewRequest(http.MethodPost, strings.TrimSuffix(apiURL, "/")+checkoutPath, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// The review binds to this org: auth-service refuses with ORG_CHANGED
	// when the org the gateway resolved is another one.
	req.Header.Set("X-Org-ID", orgID)

	res, err := (&http.Client{Timeout: requestTimeout}).Do(req)
	if err != nil {
		return "", "", &Error{Code: "UNREACHABLE", Message: fmt.Sprintf("Could not reach Dibbla to create the payment link: %v. Nothing has changed.", err)}
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	code, msg := readError(raw)

	switch {
	case res.StatusCode == http.StatusOK:
		var out struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return "", "", &Error{StatusCode: res.StatusCode, Code: "BAD_RESPONSE", Message: "Could not read the payment link from Dibbla. Nothing has changed."}
		}
		// Only ever an absolute https link is printed or opened.
		if u, err := url.Parse(out.URL); err != nil || u.Scheme != "https" || u.Host == "" {
			return "", "", &Error{StatusCode: res.StatusCode, Code: "BAD_RESPONSE", Message: "Dibbla answered without a usable payment link. Nothing has changed."}
		}
		return out.URL, "", nil
	case res.StatusCode == http.StatusConflict && (code == AlreadySubscribed || code == EnterprisePlan || code == ExternallyBilled):
		return "", code, nil
	case res.StatusCode == http.StatusForbidden && (code == "FORBIDDEN" || code == RoleRequired):
		// RequireRole's "Insufficient role": the gateway resolved a role
		// below admin in this org.
		return "", RoleRequired, nil
	case res.StatusCode == http.StatusNotFound && msg != "Organization not found":
		// With billing off the route is not mounted at all, and the
		// org-admin catch-all answers a bare 404.
		return "", BillingDisabled, nil
	case res.StatusCode == http.StatusUnauthorized:
		return "", "", &Error{StatusCode: res.StatusCode, Code: "UNAUTHORIZED", Message: "Your login is not valid any more. Run dibbla login, then dibbla upgrade again."}
	case res.StatusCode == http.StatusConflict && code == "ORG_CHANGED":
		return "", "", &Error{StatusCode: res.StatusCode, Code: code, Message: "The organization changed while the link was created. Run dibbla upgrade again."}
	default:
		detail := msg
		if detail == "" {
			detail = fmt.Sprintf("HTTP %d", res.StatusCode)
		}
		return "", "", &Error{StatusCode: res.StatusCode, Code: code, Message: fmt.Sprintf("Could not create the payment link (%s). Nothing has changed; try again in a minute.", detail)}
	}
}

// readError reads auth-service's error envelope, {"error":{"code","message"}}
// or the flat {"code","message"}.
func readError(raw []byte) (code, message string) {
	var nested struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &nested) == nil && nested.Error.Code != "" {
		return nested.Error.Code, nested.Error.Message
	}
	var flat struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &flat) == nil {
		return flat.Code, flat.Message
	}
	return "", ""
}

// CommandHint is the line a plan refusal ends with in the terminal when the
// way forward is an upgrade from a trial (DIB-1047): the same link is one
// command away. "" for any other error, and for a paid plan that is full —
// there is nothing to upgrade there.
func CommandHint(code, upgradeURL string) string {
	switch {
	case code == "TRIAL_EXPIRED":
	case code == "PLAN_LIMIT_EXCEEDED" && strings.Contains(upgradeURL, "upgrade=review"):
	default:
		return ""
	}
	return "Or get the payment link right here: dibbla upgrade"
}
