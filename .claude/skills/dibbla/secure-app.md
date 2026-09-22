# Make your app more secure on Dibbla

This is the plain-language companion to [guardrails.md](guardrails.md). Guardrails
is the checklist your agent runs before every deploy; this file is the same ground
written for the **person** who owns the app — ten risks in one sentence each, what
Dibbla already handles, one sentence to paste to your agent, and how you check
afterwards that it was actually done.

You do not need to understand the code. You need to know what to ask for, and what
the answer looks like when it is real.

## Who does what

Security on a platform is shared. The split is not a matter of opinion — it follows
from where the code runs.

| Dibbla does | You (and your agent) do |
|---|---|
| Runs each organization's apps isolated from every other organization's | Decide which of *your own* users may see which rows |
| Terminates TLS, so traffic to your app is encrypted | Keep secrets out of the source code |
| Stores secrets outside your code and injects them at runtime | Validate what people type into your app |
| Can put a login in front of the whole app, and tells the app who the caller is | Rate-limit your own login, signup and reset endpoints |
| Refuses to deploy without a written security review (`REVIEW.md`) | Run that review — your agent writes it |
| Keeps your data in the EU (Hetzner, Germany and Finland) and never trains AI on it | Know which personal data you hold and be able to delete it |
| Scans every build — a bill of materials per image, its known vulnerabilities, and secrets in the source | Decide what to do about a finding, and fix it |
| Runs scheduled checks and an overnight maintenance agent that proposes changes you approve | Approve or reject those proposals |

**Two things Dibbla deliberately does not do**, because they belong to the app and
guessing on your behalf would break working apps: it does not filter your app's
outbound network traffic, and it applies no request-size or rate limits in front
of your app. Points 6 and 8 below are yours because of this.

## How to use this file

Paste the **Ask your agent** sentence for one point into whatever agent you build
with (Claude Code, Cursor, Codex — anything that can read your project). Do them one
at a time; each answer is short and you will understand it. Or paste them all and
ask for one report.

Every point ends with what `REVIEW.md` should show. `REVIEW.md` is the security
review your agent writes at the root of your project before deploying — `dibbla
deploy` refuses to upload without it, and it is committed with your code, so you can
read it, diff it, and show it to a customer.

**One file, added to — never replaced.** If your project already has a `REVIEW.md`,
everything here goes *into* it. Its `---` header block at the top (`Review-status:`
and `One-Sentence-Summary:`) is what the deployments dashboard reads to show whether
your app was reviewed; an agent that rewrites the file from scratch and drops that
header leaves the deploy working and the status blank. If you are ever unsure, tell
your agent: *"add this to REVIEW.md, keep the header and the sections that are
already there."*

**One honest distinction, which matters if anyone asks you about it.** `REVIEW.md`
is an *agent's judgement of your source code*, recorded in writing and versioned
with the code. It is not a scan, not a penetration test and not a certification.
It is much better than nothing and much weaker than a deterministic tool. Say it
that way and you will never have to walk anything back.

---

## 1. Someone sees another customer's data by changing a number in the address

**The risk.** A customer opens `/invoices/41`, changes it to `/invoices/42`, and
sees someone else's invoice — because the app looked up invoice 42 without checking
who was asking.

**Dibbla does.** Puts a login in front of your whole app when you ask it to
(`require_login`), and passes the signed-in person's identity to your app in
`X-User-*` headers the platform's proxy sets — those headers are trustworthy,
anything the browser sends is not. Dibbla cannot know which invoice belongs to
which customer; only your app knows that.

**Ask your agent.**

> "Go through every route that reads or writes customer data and confirm two things
> for each: it requires a signed-in user, and the database query filters on the
> caller's own user or organization id taken from the session — never from the URL
> or from something the browser could have set. On Dibbla the `X-User-*` headers are
> set by the platform in front of the app and are the trusted identity; treat them as
> the session. Add the routes you checked to REVIEW.md and fix any that fail."

**How you know it's done.** `REVIEW.md` has no `BLOCKER: broken access control` or
`IDOR` line under the Security check, and the check's entry names the routes that
handle customer data and the field each one filters on (`user_id` or `org_id`). If
your agent found a problem, the report says which route it was and what changed.

---

## 2. An admin page is sitting on the open internet

**The risk.** A database viewer, a queue dashboard or an internal tools page ships
alongside the real app and is reachable by anyone who guesses its address.

**Dibbla does.** Only publishes the services you mark `public: true` in
`dibbla.yaml`; everything else is reachable only from inside your own deployment.
The pre-deploy checklist stops on a public service whose name looks administrative
(`adminer`, `pgadmin`, `grafana`, anything containing `admin`, `internal`, `debug`)
unless it requires a login or is limited to a development profile.

**Ask your agent.**

> "List every URL my deployment exposes to the internet, including each public
> service in dibbla.yaml and every route that does not require a login. For each
> one, tell me in one sentence who is supposed to be able to open it, and flag any
> admin, debug or internal surface that a stranger could reach."

**How you know it's done.** You get a short list of addresses you recognise, and
`REVIEW.md` records that each public service either requires a login or is
development-only. Nothing on the list surprises you.

---

## 3. A form field lets someone read the database or run a script in another user's browser

**The risk.** Text a visitor types into a search box, a name field or a URL is
handed straight to the database or printed back into a page, so typing the right
thing makes the app do something it was never meant to do.

**Dibbla does.** Nothing at this layer, on purpose — there is no filter between the
internet and your app that inspects what visitors type. The control is the review:
`dibbla deploy` will not upload without one.

**Ask your agent.**

> "Check every database query and every place user input ends up in HTML or in a
> shell command. Use parameterised queries everywhere, escape anything rendered into
> a page, and never build a shell command from user input. Fix what you find and
> record it in REVIEW.md."

**How you know it's done.** `REVIEW.md`'s Security check shows no `SQL injection`,
`Command injection` or `XSS` blockers. These are blockers, not warnings — a deploy
your agent did honestly will never leave one open.

---

## 4. A password or an API key is written into the code

**The risk.** A key to your payment provider, your mail sender or your database sits
in a source file, so it is in your git history, in everyone's laptop copy, and in
the container image.

**Dibbla does.** Keeps secrets outside your code entirely: `dibbla secrets set`
stores them and the platform injects them as environment variables when the app
runs. Uploads are stripped of `.env` files — only `.env.example` (names, no values)
travels with your code. `dibbla env pull` gives you the real values locally without
ever committing them.

**Ask your agent.**

> "Search the whole project, including the git history, for hardcoded API keys,
> passwords, tokens and database connection strings. Move every one to a Dibbla
> secret, read them from environment variables, and tell me which keys I need to
> rotate because they were committed."

**How you know it's done.** `REVIEW.md` shows no `Hardcoded secrets` blocker, and
`dibbla secrets list` shows the names your code reads. If anything was found, your
agent tells you plainly which keys to rotate — moving a leaked key is not the same
as replacing it.

---

## 5. Your own login is weaker than it looks

**The risk.** You built sign-in yourself, and passwords are stored in a way that a
stolen database gives away, or sessions never expire, or a reset link keeps working
forever.

**Dibbla does.** Offers to be your login: with `require_login` your app sits behind
Google or Microsoft sign-in and receives the verified user in `X-User-*` headers, so
there is no password for you to store at all. This point only applies if you chose
to build your own.

**Ask your agent.**

> "Does this app manage its own passwords or sessions? If it does, check that
> passwords are hashed with bcrypt, scrypt or argon2id, that sessions and tokens
> expire, that the session id is replaced at login, that password-reset tokens are
> random and single-use, and that cookies are secure and httpOnly. If it does not,
> say so in REVIEW.md in one line."

**How you know it's done.** `REVIEW.md` either states that the app has no login of
its own and relies on Dibbla, or lists each of those properties as checked. One
line is enough when the answer is "we don't store passwords".

---

## 6. Someone tries a code ten thousand times until it works

**The risk.** A six-digit verification code, a PIN or a password can be guessed by a
script in minutes when nothing stops it from trying again, and an open contact form
can be used to send mail in your name until your sender is blocked.

**Dibbla does.** Not this. The platform applies no rate limits in front of your app;
a request that reaches your domain reaches your code.

**Ask your agent.**

> "Add rate limiting and lockout to every endpoint someone could hammer: login,
> signup, password reset, verification codes, contact forms, search and export. Use
> the standard middleware for this stack, make the limits per IP and per account,
> and note in REVIEW.md which endpoints are now limited."

**How you know it's done.** `REVIEW.md` names the protected endpoints under the
Security check. Anything that checks a short secret — a code or a PIN — should be
listed; if one is missing, ask why.

---

## 7. A package your app uses has a known hole

**The risk.** Your app is built on dozens of libraries you never wrote. One of them
had a security flaw published last month, and the fixed version is one line away —
but nobody looked.

**Dibbla does.** Scans every build, after the rollout is live, and stores the
result on that revision: a bill of materials for each image, that inventory
matched against the known-vulnerability database with a severity, a fix version
and an advisory link per finding, and a scan of the built source for leaked
secrets. Services that run a pulled image — the `postgres:16` beside your app —
are scanned too, so the list covers the whole revision. The scan does not block
the deploy, and the overnight maintenance agent reads it when it decides what to
propose. Nothing of yours leaves the platform to make it happen.

What the scan cannot do is decide. It tells you a library has a published flaw
and which version fixes it; whether that flaw is reachable from your code, and
whether the upgrade is safe, is the part your agent does — which is what the
sentence below is for. Run it anyway: the audit for your language sees things a
scan of the finished image does not, such as a development dependency or a
version pin you have not built yet.

**Ask your agent.**

> "Run the dependency audit for this project's language — npm audit, govulncheck,
> pip-audit, cargo audit, whichever applies — and show me every high and critical
> finding with the version that fixes it. Update what can be updated safely, and
> list anything still open in REVIEW.md with why."

**How you know it's done.** `REVIEW.md` says which audit tool was run and what it
reported. "None found" is a fine answer; "no audit was run" is the one you should
push back on. The platform's own findings for the running revision are on the
application — `dibbla apps get <alias>` shows them — and the two are worth reading
together: the scan is the list, your agent's answer is what you did about it.
Re-run this before each release; it goes stale faster than anything else in this
file.

---

## 8. Your app fetches an address someone else chose

**The risk.** Your app loads a URL a user typed — a webhook target, an avatar
address, an "import from this link" field — and that URL points somewhere inside the
network instead of out on the internet.

**Dibbla does.** Not filter outbound traffic from your app. What your code decides
to fetch, it fetches.

**Ask your agent.**

> "Find every place the server fetches a URL that came from a user or from editable
> settings — webhooks, imports, link previews, avatars. Allow https only, check the
> destination against an allowlist, reject private and link-local addresses, do not
> follow redirects blindly, and never return the fetched body to the caller
> untouched."

**How you know it's done.** `REVIEW.md` has no `SSRF` blocker, and either names the
places the app fetches user-supplied URLs and how each is restricted, or says the
app fetches none.

---

## 9. You hold personal data you cannot delete — or you print it into the logs

**The risk.** A customer asks you to erase them and you find their name still in
three tables, an old export and yesterday's log file; or someone who can read your
logs can read your customers' email addresses.

**Dibbla does.** Keeps your data in the EU — Hetzner, in Germany and Finland —
never uses it to train AI models, and involves an AI model provider only if you
switch AI features on yourself, and only the one you chose. Dibbla cannot know
which of your fields are personal data, which is why the pre-deploy checklist makes
your agent write the inventory: that part is yours, and it is the part a customer
will ask you about.

**Ask your agent.**

> "Add a personal-data inventory to REVIEW.md, keeping whatever header and sections
> the file already has, under four headings. **Stored:** every table, file and bucket
> that holds data about a person, and which field it is. **Deletion:** how one person
> is deleted, and which stores that delete does not reach. **Logs:** whether any
> personal data is written to the logs. **Third parties:** who receives personal data
> and which fields they get."

**How you know it's done.** `REVIEW.md` contains the Personal data (GDPR) section
with four things filled in: **Stored**, **Deletion**, **Logs**, **Third parties**.
That section is the piece you can hand to a customer who asks how you handle their
data — it is the only part of the report written to be read outside your team. If
the app holds no personal data, one line saying so is the correct answer.

**If a customer is actually asking** — a questionnaire, a data processing agreement,
"where is our data stored", "delete our user" — read [gdpr.md](gdpr.md). It is this
point written out in full: what Dibbla handles and with what figures, and the seven
things that stay yours, including your own privacy policy and the agreement your
customers will ask you to sign.

---

## 10. It was all true the day you deployed, and nobody has looked since

**The risk.** The review describes the version you shipped in March. Since then the
code changed, new library flaws were published, and the app has been quietly failing
at 3am for a fortnight.

**Dibbla does.** Once you switch them on, runs scheduled checks against your
deployed app (`dibbla-checks.yaml`) and an overnight maintenance agent that reads
logs, source and check history and files a proposal with a diff. Both are off until
enabled, for the app and for the organization. The agent never deploys on its own: you approve or deny, and the person
whose agent wrote a proposal cannot be the one who approves it.

**Ask your agent.**

> "Add a dibbla-checks.yaml that signs in and loads the two pages my customers use
> most, so a scheduled check tells me when they break. Then show me how to enable
> the maintenance agent for this app and where its proposals appear."

**How you know it's done.** `dibbla apps checks list <alias>` shows your checks and
`dibbla apps checks history <alias>` shows them passing; `dibbla apps maintenance
status <alias>` says the agent is on; `dibbla apps proposals list <alias>` is where
its suggestions arrive. And `REVIEW.md`'s date matches roughly what you last
shipped — when it is far behind, re-run this file's points 1 to 9 before the next
deploy.

---

## Where the words come from

<details>
<summary>OWASP Top 10 (2021) and GDPR mapping for each point</summary>

The ten points above are written from the risk backwards. This table maps them onto
the categories a security consultant or a procurement questionnaire will use. Each
one corresponds to a row your agent checks in [guardrails.md](guardrails.md).

| Point | Category |
|---|---|
| 1. Another customer's data | A01:2021 Broken Access Control (IDOR) |
| 2. Admin page on the open internet | A01:2021 Broken Access Control · A05:2021 Security Misconfiguration |
| 3. Form field reaching the database or another browser | A03:2021 Injection (SQL, command, XSS) |
| 4. Secret in the code | A02:2021 Cryptographic Failures · CWE-798 Hardcoded Credentials |
| 5. Weak own login | A07:2021 Identification and Authentication Failures |
| 6. Unlimited guessing | A04:2021 Insecure Design (missing rate limiting and lockout) |
| 7. Library with a known flaw | A06:2021 Vulnerable and Outdated Components |
| 8. Fetching a user-supplied address | A10:2021 Server-Side Request Forgery |
| 9. Personal data you cannot delete, or log | GDPR Art. 5(1)(c) and (e), Art. 17, Art. 28 — not an OWASP category |
| 10. Nobody looked since | A08:2021 Software and Data Integrity Failures · A09:2021 Security Logging and Monitoring Failures |

Point 9 is the only one that is not an OWASP category, and the only one a customer
is likely to raise with you directly. [gdpr.md](gdpr.md) is that point in full —
the articles, what the platform handles, and the documents you are expected to have.

Dibbla's own controls are described as aligned with ISO/IEC 27001:2022 Annex A.
Dibbla AB is not ISO 27001 certified, and nothing in this file should be presented
as a certification.

</details>
