package deploy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/rolerefusal"
	"github.com/dibbla-agents/dibbla-cli/internal/secrets"
	"github.com/spf13/cobra"
)

// DIB-1343: a secret's value goes from the person's browser into Dibbla, not
// through this terminal. `dibbla secrets request` asks for one secret; the
// person opens the link — the console, signed in — and types the value there.
// The CLI learns only that it happened. It is what an AI agent runs instead of
// asking anyone to type a value where the agent can see it.
//
// DIB-1340: `dibbla env promote` is the same request for a plain env var that
// should have been a secret. Its value has been readable, so the person enters
// a NEW one; the server then removes the env var and restarts the app.

const (
	// entryTitleMax and entryWhyMax are deploy-api's bounds on the two texts
	// shown to the person. Checked here so a long --why fails before the call.
	entryTitleMax = 80
	entryWhyMax   = 600
	// entryWaitGrace is how long --wait keeps reading past the request's own
	// expiry, for a save that started just before it.
	entryWaitGrace = time.Minute
	// entryPollFailures is how many reads in a row may fail on the way (a
	// network blip, a 5xx) before --wait gives up on a long wait.
	entryPollFailures = 5
)

// entryPollInterval is how often --wait reads the request's state.
var entryPollInterval = 3 * time.Second

var secretsRequestCmd = &cobra.Command{
	Use:   "request [name]",
	Short: "Ask for a secret's value to be entered in the browser, not in this terminal",
	Long: `Create a secret entry request: a link to a page in the Dibbla console where the
value is typed, signed in, and goes straight into the secrets store. It never
passes through this terminal, its history or an AI assistant's transcript. The
command prints the link, the request id and when it expires; it never sees the
value.

This is how an AI assistant gets a secret set: it runs this command, hands you
the link and waits. Only you can open it — the page answers to the person who
made the request, in this organization, with a role that may write secrets.

Scope as for 'secrets set': omit --deployment for an org-global secret, set it
for an app (an app that has not been deployed yet is fine), add --service for
one service. --title and --why are shown on the page: what the secret is, why
the app needs it, where to find it.

--wait reads the request every few seconds until the value is entered, the
request is cancelled on the page, or it expires; it exits 0 only when the value
was entered. --status <id> reads a request made earlier (with --wait, waits for
it). With --json the server's document is printed: with --wait, the request as
created and then as it ended, one per line.

Examples:
  dibbla secrets request STRIPE_API_KEY -d shop --title "Stripe secret key" \
    --why "Shop charges cards with it. Stripe dashboard → Developers → API keys."
  dibbla secrets request STRIPE_API_KEY -d shop --wait
  dibbla secrets request --status sreq_… --wait`,
	Args: cobra.MaximumNArgs(1),
	Run:  runSecretsRequest,
}

var envPromoteCmd = &cobra.Command{
	Use:   "promote <name>",
	Short: "Turn a plain env var into a secret, with a new value entered in the browser",
	Long: `Promote a plain environment variable to a secret. A plain env var is readable by
anyone who can read the app's configuration, so its current value counts as
exposed: rotate it where it was issued (a new API key at the provider), then
open the link this command prints and enter the NEW value. The page refuses
the unchanged value. Once it is entered, Dibbla stores it as a secret, removes
the env var and restarts the app.

The value never passes through this terminal. 'dibbla env pull' names the
plain variables that look like secrets: those are the candidates.

A variable that comes from dibbla.yaml (environment:) cannot be promoted —
the next deploy would put it back. Remove the line, request the secret with
'dibbla secrets request <name> -d <alias>', then deploy.

The app is the one this folder is linked to (dibbla clone / dibbla link), or
--deployment <alias>. --wait, --json and --status work as on 'secrets request',
which also reads a promotion's state: dibbla secrets request --status <id>.

Examples:
  dibbla env promote STRIPE_SECRET_KEY --title "Stripe secret key" \
    --why "Roll the key in the Stripe dashboard first; the old one has been visible."
  dibbla env promote STRIPE_SECRET_KEY -d shop --wait`,
	Args: cobra.ExactArgs(1),
	Run:  runEnvPromote,
}

var (
	secretsRequestDeployment string
	secretsRequestService    string
	secretsRequestTitle      string
	secretsRequestWhy        string
	secretsRequestWait       bool
	secretsRequestJSON       bool
	secretsRequestStatus     string

	envPromoteDeployment string
	envPromoteService    string
	envPromoteTitle      string
	envPromoteWhy        string
	envPromoteWait       bool
	envPromoteJSON       bool
)

func init() {
	secretsCmd.AddCommand(secretsRequestCmd)
	f := secretsRequestCmd.Flags()
	f.StringVarP(&secretsRequestDeployment, "deployment", "d", "", "Request the secret for this app (omit for global)")
	f.StringVarP(&secretsRequestService, "service", "s", "", "Scope the secret to a single service (requires -d)")
	f.StringVar(&secretsRequestTitle, "title", "", "Heading shown on the page: what the secret is (plain text, at most 80 bytes)")
	f.StringVar(&secretsRequestWhy, "why", "", "Paragraph shown on the page: why the app needs it, where to find it (plain text, at most 600 bytes)")
	f.BoolVar(&secretsRequestWait, "wait", false, "Wait until the value is entered, the request is cancelled or it expires")
	f.BoolVar(&secretsRequestJSON, "json", false, "Print the server's document instead of text")
	f.StringVar(&secretsRequestStatus, "status", "", "Read the state of an earlier request by its id instead of creating one")

	envCmd.AddCommand(envPromoteCmd)
	g := envPromoteCmd.Flags()
	g.StringVarP(&envPromoteDeployment, "deployment", "d", "", "App alias (default: the app this folder is linked to)")
	g.StringVarP(&envPromoteService, "service", "s", "", "The service whose env var it is, in a multi-service app")
	g.StringVar(&envPromoteTitle, "title", "", "Heading shown on the page (plain text, at most 80 bytes)")
	g.StringVar(&envPromoteWhy, "why", "", "Paragraph shown on the page (plain text, at most 600 bytes; a default tells the person to rotate)")
	g.BoolVar(&envPromoteWait, "wait", false, "Wait until the new value is entered, the request is cancelled or it expires")
	g.BoolVar(&envPromoteJSON, "json", false, "Print the server's document instead of text")
}

// entryRequestInput is what both commands hand their core.
type entryRequestInput struct {
	Name, Deployment, Service string
	Title, Why                string
	Status                    string // --status: read this request instead of creating one
	Promote                   bool
	Wait, JSON                bool
	APIURL, APIToken          string
	// Dir is the working directory, for env promote's linked-folder default.
	Dir string
	// Now and Sleep are the clock --wait runs on; tests replace both.
	Now   func() time.Time
	Sleep func(time.Duration)
}

func runSecretsRequest(cmd *cobra.Command, args []string) {
	in := entryRequestInput{
		Deployment: secretsRequestDeployment, Service: secretsRequestService,
		Title: secretsRequestTitle, Why: secretsRequestWhy, Status: secretsRequestStatus,
		Wait: secretsRequestWait, JSON: secretsRequestJSON,
	}
	if len(args) == 1 {
		in.Name = args[0]
	}
	os.Exit(runEntryCommand(in))
}

func runEnvPromote(cmd *cobra.Command, args []string) {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}
	os.Exit(runEntryCommand(entryRequestInput{
		Name: args[0], Deployment: envPromoteDeployment, Service: envPromoteService,
		Title: envPromoteTitle, Why: envPromoteWhy, Promote: true,
		Wait: envPromoteWait, JSON: envPromoteJSON, Dir: dir,
	}))
}

func runEntryCommand(in entryRequestInput) int {
	cfg := config.Load()
	requireToken(cfg)
	in.APIURL, in.APIToken = cfg.APIURL, cfg.APIToken
	in.Now, in.Sleep = time.Now, time.Sleep
	if in.Promote {
		return runEnvPromoteCore(os.Stdout, os.Stderr, in)
	}
	return runSecretsRequestCore(os.Stdout, os.Stderr, in)
}

// runSecretsRequestCore is `dibbla secrets request`. Returns the exit code.
func runSecretsRequestCore(stdout, stderr io.Writer, in entryRequestInput) int {
	bad := platform.Icon("❌", "[X]")
	if in.Status != "" {
		if in.Name != "" || in.Deployment != "" || in.Service != "" || in.Title != "" || in.Why != "" {
			fmt.Fprintf(stderr, "%s --status reads a request made earlier; it takes no name, --deployment, --service, --title or --why.\n", bad)
			return 5
		}
		return readEntryStatus(stdout, stderr, in)
	}
	if in.Name == "" {
		fmt.Fprintf(stderr, "%s name the secret: dibbla secrets request <name> [-d <alias>]  (or --status <id> for an earlier request)\n", bad)
		return 5
	}
	if code := validateEntryInput(stderr, &in); code != 0 {
		return code
	}
	return createEntryRequest(stdout, stderr, in, "secrets request")
}

// runEnvPromoteCore is `dibbla env promote`. Returns the exit code.
func runEnvPromoteCore(stdout, stderr io.Writer, in entryRequestInput) int {
	bad := platform.Icon("❌", "[X]")
	if in.Deployment == "" {
		_, _, target, ok := linkedFolder(in.Dir)
		if !ok {
			fmt.Fprintf(stderr, "%s this folder is not linked to an app.\n", bad)
			fmt.Fprintln(stderr, "  hint: run it in a folder from 'dibbla clone <app>', or name the app with --deployment <alias>.")
			return 5
		}
		in.Deployment = target.App
	}
	if code := validateEntryInput(stderr, &in); code != 0 {
		return code
	}
	if strings.TrimSpace(in.Why) == "" {
		in.Why = promoteExplanation(in.Name, in.Deployment)
	}
	return createEntryRequest(stdout, stderr, in, "env promote")
}

// promoteExplanation is the page's paragraph when --why is not given: the one
// thing the person must not miss is that the old value is spent.
func promoteExplanation(name, alias string) string {
	return fmt.Sprintf("%s has been a plain environment variable of %s, readable by anyone who can read the app's configuration, so treat its value as exposed. "+
		"Create a new value where it was issued, then enter the new value here. Dibbla stores it as a secret, removes the environment variable and restarts the app.", name, alias)
}

// validateEntryInput checks what the server would refuse anyway, before any
// call: the name, the scope and the two texts for the page.
func validateEntryInput(stderr io.Writer, in *entryRequestInput) int {
	bad := platform.Icon("❌", "[X]")
	if !secretNameRe.MatchString(in.Name) {
		fmt.Fprintf(stderr, "%s %q is not a secret name: 1-128 letters, digits and underscores, starting with a letter.\n", bad, in.Name)
		return 5
	}
	if !requireServiceWithDeployment(stderr, in.Deployment, in.Service) {
		return 5
	}
	if in.Deployment != "" && !apps.AliasRe.MatchString(in.Deployment) {
		return invalidAlias(stderr, in.Deployment)
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Why = strings.TrimSpace(strings.ReplaceAll(in.Why, "\r\n", "\n"))
	if len(in.Title) > entryTitleMax || !plainText(in.Title, false) {
		fmt.Fprintf(stderr, "%s --title must be one line of plain text, at most %d bytes (it is %d).\n", bad, entryTitleMax, len(in.Title))
		return 5
	}
	if len(in.Why) > entryWhyMax || !plainText(in.Why, true) {
		fmt.Fprintf(stderr, "%s --why must be plain text, at most %d bytes (it is %d).\n", bad, entryWhyMax, len(in.Why))
		return 5
	}
	return 0
}

// plainText mirrors deploy-api's test for the texts shown on the page: no
// control characters, line breaks only where a paragraph is expected.
func plainText(s string, newlines bool) bool {
	for _, r := range s {
		if r == '\n' && newlines {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func createEntryRequest(stdout, stderr io.Writer, in entryRequestInput, verb string) int {
	req, raw, err := secrets.CreateEntryRequest(in.APIURL, in.APIToken, secrets.EntryRequestInput{
		Name: in.Name, DeploymentAlias: in.Deployment, ServiceName: in.Service,
		Title: in.Title, Explanation: in.Why, PromoteEnv: in.Promote,
	})
	if err != nil {
		return reportEntryError(stderr, verb, in, err)
	}
	if in.Promote && !req.PromotesEnv {
		// A server from before DIB-1340 ignores promote_env and makes a plain
		// request. Entering a value there would leave the env var in place
		// beside a secret of the same name, so the link is not handed out.
		fmt.Fprintf(stderr, "%s env promote %s: this Dibbla server does not support promotion yet, so nothing will change.\n", platform.Icon("❌", "[X]"), in.Name)
		fmt.Fprintf(stderr, "  It made request %s as a plain secret request instead; it expires unused. Nothing was saved and the env var stays.\n", req.RequestID)
		return 1
	}
	if in.JSON {
		fmt.Fprintln(stdout, strings.TrimSpace(string(raw)))
	} else {
		printEntryCreated(stdout, req, in.Now())
	}
	if !in.Wait {
		return 0
	}
	return waitForEntry(stdout, stderr, in, req, nil)
}

func readEntryStatus(stdout, stderr io.Writer, in entryRequestInput) int {
	id := strings.TrimSpace(in.Status)
	req, raw, err := secrets.GetEntryRequestStatus(in.APIURL, in.APIToken, id)
	if err != nil {
		return reportEntryError(stderr, "secrets request --status", in, err)
	}
	if !in.Wait {
		if in.JSON {
			fmt.Fprintln(stdout, strings.TrimSpace(string(raw)))
		} else {
			printEntryStatus(stdout, req, in.Now())
		}
		return 0
	}
	if !in.JSON && !req.Terminal() {
		printEntryStatus(stdout, req, in.Now())
	}
	return waitForEntry(stdout, stderr, in, req, raw)
}

// waitForEntry reads the request until it can no longer change, and reports
// how it ended. Exit 0 only when the value was entered. raw is the document
// req was read from, printed under --json if no later read replaces it; nil
// when it has been printed already.
func waitForEntry(stdout, stderr io.Writer, in entryRequestInput, req *secrets.EntryRequest, raw []byte) int {
	deadline := req.ExpiresAt.Add(entryWaitGrace)
	if req.ExpiresAt.IsZero() {
		deadline = in.Now().Add(30 * time.Minute)
	}
	if !in.JSON && !req.Terminal() {
		fmt.Fprintf(stdout, "%s Waiting for the value to be entered on the page (Ctrl-C stops waiting; the request stays open until it expires)...\n", platform.Icon("⏳", "[..]"))
	}
	failures := 0
	for !req.Terminal() {
		if in.Now().After(deadline) {
			fmt.Fprintf(stderr, "%s stopped waiting for request %s: it is still %s past its expiry.\n", platform.Icon("⏱", "[TIMEOUT]"), req.RequestID, req.State)
			fmt.Fprintf(stderr, "  Read it again with: dibbla secrets request --status %s\n", req.RequestID)
			return 7
		}
		in.Sleep(entryPollInterval)
		next, body, err := secrets.GetEntryRequestStatus(in.APIURL, in.APIToken, req.RequestID)
		if err != nil {
			var se *secrets.StatusError
			if errors.As(err, &se) && se.Status < 500 {
				return reportEntryError(stderr, "secrets request --wait", in, err)
			}
			if failures++; failures >= entryPollFailures {
				return reportEntryError(stderr, "secrets request --wait", in, err)
			}
			continue
		}
		failures = 0
		req, raw = next, body
	}
	if in.JSON {
		if raw != nil {
			fmt.Fprintln(stdout, strings.TrimSpace(string(raw)))
		}
	} else {
		printEntryFinal(stdout, req)
	}
	return entryExitCode(req)
}

// entryExitCode: 0 entered, 1 cancelled, 7 expired (the CLI's timeout code).
func entryExitCode(req *secrets.EntryRequest) int {
	switch req.State {
	case secrets.EntryStateCompleted:
		return 0
	case secrets.EntryStateExpired:
		return 7
	default:
		return 1
	}
}

// entryTarget renders "API_KEY (deployment shop, service web)".
func entryTarget(req *secrets.EntryRequest) string {
	return fmt.Sprintf("%s (%s)", req.Name, scopeLabel(req.DeploymentAlias, req.ServiceName))
}

func printEntryCreated(w io.Writer, req *secrets.EntryRequest, now time.Time) {
	ok := platform.Icon("✅", "[OK]")
	if req.PromotesEnv {
		fmt.Fprintf(w, "%s Promotion requested: %s becomes a secret.\n", ok, entryTarget(req))
		fmt.Fprintln(w, "   Its value has been a plain env var, readable by anyone who can read the app's configuration: treat it as exposed.")
		fmt.Fprintln(w, "   Rotate it where it was issued, then open this link and enter the NEW value; it never passes through this terminal:")
	} else {
		fmt.Fprintf(w, "%s Secret entry request for %s.\n", ok, entryTarget(req))
		fmt.Fprintln(w, "   Open this link and enter the value; it never passes through this terminal:")
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "   %s\n", req.EntryURL)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "   Request:  %s\n", req.RequestID)
	fmt.Fprintf(w, "   Expires:  %s\n", whenExpires(req.ExpiresAt, now))
	switch {
	case req.PromotesEnv:
		fmt.Fprintf(w, "   Then:     the env var %s is removed and the app restarts with the secret. The page refuses the unchanged value.\n", req.Name)
	case req.ReplacesExisting:
		fmt.Fprintf(w, "   Replaces: the current value of %s.\n", req.Name)
	}
	fmt.Fprintf(w, "   Check:    dibbla secrets request --status %s   (add --wait to wait for it)\n", req.RequestID)
}

func printEntryStatus(w io.Writer, req *secrets.EntryRequest, now time.Time) {
	fmt.Fprintf(w, "Request %s — %s: %s\n", req.RequestID, entryTarget(req), req.State)
	if req.PromotesEnv {
		fmt.Fprintf(w, "   A promotion: once a new value is entered, the env var %s is removed and the app restarts.\n", req.Name)
	}
	switch req.State {
	case secrets.EntryStatePending:
		if req.EntryURL != "" {
			fmt.Fprintf(w, "   Link:     %s\n", req.EntryURL)
		}
		fmt.Fprintf(w, "   Expires:  %s\n", whenExpires(req.ExpiresAt, now))
	case secrets.EntryStateCompleted:
		if req.CompletedAt != nil {
			fmt.Fprintf(w, "   Entered:  %s\n", req.CompletedAt.UTC().Format("2006-01-02 15:04 UTC"))
		}
	case secrets.EntryStateCancelled, secrets.EntryStateExpired:
		fmt.Fprintln(w, "   Nothing was saved.")
	}
}

func printEntryFinal(w io.Writer, req *secrets.EntryRequest) {
	switch req.State {
	case secrets.EntryStateCompleted:
		if req.PromotesEnv {
			fmt.Fprintf(w, "%s %s is a secret now: the env var is removed and the app restarts with the new value.\n", platform.Icon("✅", "[OK]"), entryTarget(req))
			return
		}
		fmt.Fprintf(w, "%s The value of %s was entered and saved.\n", platform.Icon("✅", "[OK]"), entryTarget(req))
	case secrets.EntryStateCancelled:
		fmt.Fprintf(w, "%s The request for %s was cancelled on the page. Nothing was saved.\n", platform.Icon("❌", "[X]"), entryTarget(req))
	case secrets.EntryStateExpired:
		fmt.Fprintf(w, "%s The request for %s expired before a value was entered. Nothing was saved; run the command again for a new link.\n", platform.Icon("⏱", "[TIMEOUT]"), entryTarget(req))
	default:
		fmt.Fprintf(w, "Request %s for %s: %s\n", req.RequestID, entryTarget(req), req.State)
	}
}

// whenExpires renders "2026-10-07 12:15 UTC (in 15 min)".
func whenExpires(at, now time.Time) string {
	if at.IsZero() {
		return "-"
	}
	stamp := at.UTC().Format("2006-01-02 15:04 UTC")
	left := at.Sub(now).Round(time.Minute)
	if left <= 0 {
		return stamp + " (passed)"
	}
	return fmt.Sprintf("%s (in %d min)", stamp, int(left.Minutes()))
}

// reportEntryError prints one refusal in words the person can act on and
// returns the ladder exit code.
func reportEntryError(stderr io.Writer, verb string, in entryRequestInput, err error) int {
	bad := platform.Icon("❌", "[X]")
	var se *secrets.StatusError
	if !errors.As(err, &se) {
		fmt.Fprintf(stderr, "%s %s failed: %v\n", bad, verb, err)
		return 1
	}
	switch {
	case se.Status == 403 && se.Code == rolerefusal.Code:
		msg := se.Message
		if explained, ok := rolerefusal.Explain(se.Body); ok {
			msg = explained
		}
		fmt.Fprintf(stderr, "%s %s refused: %s\n", bad, verb, msg)
	case se.Code == "ENV_VAR_NOT_FOUND":
		fmt.Fprintf(stderr, "%s %s %s: %s is not a plain env var of %s, so there is nothing to promote.\n", bad, verb, in.Name, in.Name, scopeLabel(in.Deployment, in.Service))
		fmt.Fprintln(stderr, "  hint: names are case-sensitive; in a multi-service app, name the service with --service. To set a new secret, use 'dibbla secrets request'.")
	case se.Code == "ENV_FROM_MANIFEST":
		fmt.Fprintf(stderr, "%s %s %s: the env var comes from dibbla.yaml (environment:), so the next deploy would put it back.\n", bad, verb, in.Name)
		if se.Message != "" {
			fmt.Fprintf(stderr, "  %s\n", se.Message)
		}
		fmt.Fprintf(stderr, "  Instead: remove %s from dibbla.yaml, run 'dibbla secrets request %s -d %s' for a new value, then deploy.\n", in.Name, in.Name, in.Deployment)
	case se.Status == 404 && in.Status != "":
		fmt.Fprintf(stderr, "%s %s: no request %s for you in this organization.\n", bad, verb, strings.TrimSpace(in.Status))
		fmt.Fprintln(stderr, "  A request is answered only to the person who made it, in the organization it was made in.")
	default:
		fmt.Fprintf(stderr, "%s %s failed: %v\n", bad, verb, se)
	}
	return se.ExitCode()
}
