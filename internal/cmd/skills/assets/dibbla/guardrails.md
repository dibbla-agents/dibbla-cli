# Dibbla CLI — Pre-deploy guardrails

Before calling `dibbla deploy`, you **MUST** complete every check below that applies, and present findings to the user. **Never deploy autonomously** — always wait for explicit user confirmation.

Most checks are mandatory on every deploy. The exceptions are the three that state their own trigger in their heading line — read each check's opening sentence rather than a list here, because a list here is a second inventory that can disagree with the file. Today those three are Check 6 (running task files from URLs), Check 7 (a `dibbla.yaml` at the deploy root) and Check 9 (that manifest setting `support.enabled: true`). Check 5 (personal data) is mandatory even when the app holds none — the report then says so in one line.

> **Enforced by the platform, on every deploy path.** `dibbla deploy` refuses to upload when `REVIEW.md` is missing at the deploy root, when no user handbook (`docs/index.md` or `APP.md`) is present, or when that handbook's `subtitle:` frontmatter is missing, empty, still a placeholder (`TBD`/`TODO`/`{{…}}`/`<one short…>`), or over the 140-byte hard cap. The server applies the same gate to the extracted source of **every** deploy — a `git push` to `main` of a linked folder, `platform_deployment_start` over MCP, a linked GitHub repository — and answers `REVIEW_INCOMPLETE` with the same message and hints, so a deploy that never touches the CLI is gated too. The only way past the gate is `dibbla deploy --skip-review` on an archive deploy, which is reserved for humans making one-line fixes — agents must run this checklist and write `REVIEW.md` (see Step 3.5) rather than passing the flag. A push has no flag: the commit itself must carry `REVIEW.md` and the handbook.

**Explaining any of this to the person who owns the app?** Use [secure-app.md](secure-app.md) — the same ten risks in one plain sentence each, with who handles what, a sentence the owner can paste back to you, and what this report should show them afterwards. It is written for a reader who does not read code.

**Check 5 (personal data) is the one an owner gets asked about by their customers.** [gdpr.md](gdpr.md) is that check written for them: what the platform handles (EU hosting, log and backup retention, what deletion removes), and the seven things that stay theirs — the inventory, their privacy policy, a data processing agreement for their own customers, access and erasure requests, retention in their own code, and personal data in logs and third parties.

---

## Severity levels

- **BLOCKER** — Must fix before deploying. Do NOT call `dibbla deploy`.
- **WARNING** — Should fix. Present to the user and deploy only if they explicitly confirm.

---

## Check 1: Security (OWASP Top 10)

Mandatory for every deploy. Scan all application source files for:

| What to check | Severity | Examples |
|----------------|----------|----------|
| Hardcoded secrets (API keys, passwords, tokens, connection strings) | BLOCKER | `const apiKey = "sk-..."`, `password = "admin123"`, strings matching `sk-`, `ak_`, `ghp_`, `password\s*=\s*["'][^"']+` |
| SQL injection (string concatenation/interpolation in queries) | BLOCKER | `` `SELECT * FROM users WHERE id = ${id}` ``, `"SELECT * FROM users WHERE id = " + id` |
| Command injection (unsanitized input in shell commands) | BLOCKER | `exec("rm " + userInput)`, `os.system(f"ls {path}")`, `child_process.exec(userInput)` |
| XSS (unsanitized user input rendered in HTML) | BLOCKER | `innerHTML = userInput`, `dangerouslySetInnerHTML` without sanitization |
| A dotenv file with values that git could see | BLOCKER | `.env` or `.env.local` present **and not listed in `.gitignore`**. A `.env.local` that *is* in `.gitignore` is expected — it is what `dibbla env pull` writes for local runs. `.env.example` (names only, committed) is expected too, never a finding. The platform strips/refuses every `.env*` except `.env.example`/`.env.sample` anyway; the check is that git never sees the values. |
| Missing CSRF protection on state-changing endpoints | WARNING | POST/PUT/DELETE routes with no CSRF token or SameSite cookie |
| Insecure deserialization / eval | WARNING | `eval()` on user input, `pickle.loads()` on untrusted data, `yaml.load()` without `SafeLoader` |
| Missing input validation on API endpoints | WARNING | No request body validation, no type checking on route params |
| Sensitive data in logs | WARNING | Logging passwords, tokens, session ids or API keys to stdout/console. Personal data in logs (names, emails, IPs) belongs to Check 5 — report it there, not twice. |
| Missing security headers | WARNING | No `helmet()` (Node), no CORS configuration, no `Content-Security-Policy` |
| Broken access control / IDOR (A01) — a route returns customer data without requiring login, or without filtering on the caller's user/org | BLOCKER | `app.get("/orders/:id", (req, res) => db.orders.find(req.params.id))` with no auth middleware and no `WHERE user_id = <caller>`; `/api/users/:id` where `:id` comes from the URL and is never compared to the session user; an `X-User-Id`/`X-Org-Id` header or `?org=` param trusted as-is. For every route that reads or writes customer data, confirm two things: it requires authentication, and every query is scoped to the caller (`user_id`/`org_id` from the session, never from the request). Behind Dibbla's `require_login`, the `X-User-*` headers set by the proxy are the trusted identity — anything the client sends is not. |
| Server-side request forgery, SSRF (A10) — the server fetches a URL the user supplied | BLOCKER | `fetch(req.body.url)`, `requests.get(request.args["callback"])`, `http.Get(webhookURL)` where the URL comes from the request or from user-editable settings (webhooks, avatar URLs, "import from URL", link previews, PDF/image fetchers). Allowlist scheme (`https` only) and host, resolve and reject private/link-local ranges (`10/8`, `172.16/12`, `192.168/16`, `127/8`, `169.254.169.254`, `::1`), disable redirects or re-validate after each one, and never forward the response body raw. |
| Vulnerable or outdated dependencies (A06) — a dependency with a known CVE, or no audit run at all | WARNING | `npm audit --audit-level=high` / `pnpm audit`, `govulncheck ./...`, `pip-audit`, `cargo audit`, `bundle audit`, `composer audit` reports a high/critical finding; a lockfile missing so the build resolves unpinned versions; a pinned base image years old (`FROM node:14`). Run the audit for the project's ecosystem before deploy and list any high/critical finding with the fix version. Escalate to BLOCKER when the finding is critical and reachable (e.g. a deserialization or auth-bypass CVE in a framework the app exposes). |
| Weak authentication in app-managed login (A07) — the app stores or checks credentials itself | BLOCKER | Passwords stored in plaintext or hashed with `md5`/`sha1`/`sha256` and no salt — use `bcrypt`, `scrypt` or `argon2id`; sessions/JWTs that never expire (`expiresIn` missing, `maxAge` unset), a session ID not rotated at login, a password reset token that is guessable or never invalidated; `cookie: { secure: false, httpOnly: false }`; login that reports "unknown user" vs "wrong password" separately. Applies only when the app has its own login — if it delegates to Dibbla `require_login` / OAuth, this row is N/A (note that in the report). |
| Missing rate limiting on abuse-prone endpoints (A04) — login, signup, password reset, OTP/2FA, contact/email forms, expensive search or export | WARNING | `POST /login`, `POST /reset-password`, `POST /verify-code`, `POST /contact` with no rate-limit middleware (`express-rate-limit`, `slowapi`, `golang.org/x/time/rate`, `rack-attack`) and no lockout/backoff after repeated failures; an unauthenticated endpoint that triggers outbound email/SMS or an LLM call per request. Escalate to BLOCKER when the endpoint checks a short secret (OTP, PIN, reset code) — without a limit it is brute-forceable in minutes. |

---

## Check 2: Database usage

Mandatory for every deploy. Scan all database-related code for:

| What to check | Severity | Examples |
|----------------|----------|----------|
| N+1 queries (query inside a loop) | BLOCKER | `for user in users: db.query("SELECT * FROM orders WHERE user_id = ...")` |
| Unbounded queries (SELECT without LIMIT) | WARNING | `SELECT * FROM large_table` with no `LIMIT` or pagination |
| Missing connection pooling | WARNING | Creating a new DB connection per request instead of using a pool |
| Missing error handling on DB operations | WARNING | No try/catch around queries, no transaction rollback on failure |
| Schema migrations without safeguards | WARNING | `DROP TABLE`, `DROP COLUMN` without backup or confirmation |

---

## Check 3: REST / API call patterns

Mandatory for every deploy. Scan all outbound HTTP/API call code for:

| What to check | Severity | Examples |
|----------------|----------|----------|
| No timeout on outbound HTTP calls | BLOCKER | `fetch(url)` or `requests.get(url)` with no timeout option |
| Missing retry with exponential backoff | WARNING | Single HTTP call with no retry logic for transient failures (5xx, network errors) |
| Excessive polling (interval < 5 seconds) | WARNING | `setInterval(poll, 1000)`, tight polling loops |
| No error handling on API responses | WARNING | Not checking HTTP status codes, not handling network errors |
| Hardcoded external URLs | WARNING | Third-party API URLs inline in source instead of env vars / config |
| Missing rate limiting on inbound endpoints | WARNING | Public-facing API routes with no rate limiting middleware. Login, reset, OTP and other abuse-prone endpoints are covered by the A04 row in Check 1 — report them there, not twice. |

---

## Check 4: External system write safety

Mandatory for every deploy. Scan code that writes to external systems (APIs, queues, email, SMS, webhooks, third-party services):

| What to check | Severity | Examples |
|----------------|----------|----------|
| Unbounded writes in a loop (no batching) | BLOCKER | `for item in items: api.post("/send", item)` — should batch or throttle |
| No rate limiting on outgoing calls | WARNING | Sending hundreds of emails/SMS/webhooks with no throttle or delay |
| Missing idempotency on write operations | WARNING | No idempotency key on payment, order creation, or webhook delivery calls |
| Fire-and-forget writes (no error handling) | WARNING | Write calls with no error capture, no retry, no dead-letter handling |
| Missing queue for bulk operations | WARNING | Synchronously sending thousands of notifications instead of using a job queue |

---

## Check 5: Personal data (GDPR)

Mandatory for every deploy. Most apps hold data about identifiable people — an email in a `users` table, a name on an invoice, an IP address in a request log — and under the GDPR the app's owner must be able to say what is held, where, how it is deleted and who else receives it. This check produces that inventory. **Its output is the customer's GDPR checklist, so write it down even when everything passes.** If the app genuinely stores or processes no personal data, the report says so in one line and you are done.

Personal data is any information relating to an identifiable person: names, email addresses, phone numbers, postal addresses, national ID numbers, IP addresses, device ids, user ids from an identity provider (including the `X-User-*` headers the platform injects), location, photos, free-text fields a person types about themselves or others, and anything that can be joined back to such a field.

| What to check | Severity | Examples |
|----------------|----------|----------|
| Personal data stored without an inventory in the report | BLOCKER | Any table, collection, bucket, file or cache that holds a personal-data field, when `REVIEW.md` does not list it as `<store>.<field>` under this check. `users.email`, `orders.shipping_address`, `sessions.ip`, `uploads/avatars/`, a Redis key holding a profile. The inventory is the deliverable of this check; a missing inventory is the finding. |
| Special-category or high-risk data not called out by name | WARNING | Health, ethnicity, religion, political opinions, union membership, sexual orientation, biometrics, criminal records, children's data, national ID / personal identity numbers, payment card numbers. Name each such field explicitly, confirm with the user that it is needed and that they know the stricter rules, and flag it if stored in clear text or reachable by every role. |
| No way to delete a person | BLOCKER | Personal data is stored, and there is no endpoint, admin action, CLI command or documented SQL procedure that removes one person's rows. A deploy that collects data it cannot erase leaves the owner unable to honour an erasure request. |
| Deletion is incomplete | WARNING | A delete path exists but leaves copies: missing `ON DELETE CASCADE` or explicit child deletes on `orders`, `comments`, `audit_log`; derived tables, search indexes, object-storage files, caches, queued jobs or exports still hold the person. Name each store the delete path does not reach. |
| Personal data in logs | WARNING | `console.log(req.body)`, `logger.info("login", user)`, request logging that prints emails, names or full IPs to stdout, error reports carrying the request payload. Platform logs are retained and readable by the whole org; log the id, not the person. Special-category data or national ID numbers in logs are a BLOCKER. |
| Personal data sent to a third party that the report does not name | WARNING | Email/SMS providers, AI gateways and model APIs (prompts containing customer text), analytics, error trackers (`Sentry`, `Bugsnag`), payment providers, CRMs, geocoders, translation APIs. Sending is the owner's decision; **not saying so is the finding.** List each recipient and which fields leave the platform. |
| Trackers or analytics load before consent | WARNING | Google Analytics, Meta pixel, Hotjar, ad tags or third-party fonts in the HTML `<head>` that fire for every visitor with no consent gate. Flag it and offer a consent-gated load. |
| No retention limit on data that has served its purpose | INFO | Sessions, invitation tokens, password-reset tokens, uploads, logs and exports with no expiry or cleanup job. Suggest a TTL or a scheduled purge; a `dibbla.yaml` cron job is the platform's way to run it. |

**Write the inventory into the report, under this check's row, even when the result is OK:**

```
- [x] Personal data (GDPR): OK
  - Stored: `users.email`, `users.name`, `sessions.ip`, `uploads/avatars/` (S3)
  - Deletion: `DELETE /api/me` — cascades to `orders`, `comments`; removes the avatar object
  - Logs: request log prints `user_id` only; no emails or IPs
  - Third parties: Resend (email, name — transactional mail), Dibbla AI gateway (ticket text)
  - Special categories: none
```

When the app holds no personal data at all (a static site, a public dashboard over aggregate data), one line is enough: `- [x] Personal data (GDPR): none stored or processed`.

**If found:**

1. Show the user the store and field (or the log line / outbound call) with file path and line number.
2. For a missing inventory, write it — that is a report change, not a code change, and needs no confirmation.
3. For a missing or incomplete delete path, propose the endpoint or procedure and the cascade it needs; wait for confirmation before changing code.
4. For logs and third parties, propose the redaction or name the recipient in the report; let the user decide whether the transfer itself stays.

Reference fixtures for this check live in `testdata/guardrail-fixtures/` — one handler that must trip it and one that must not.

---

## Check 6: Running task files from URLs

When the user asks you to run a `dibbla-task.yaml` from a URL (via `dibbla run <url>` or `dibbla template install <id>`), apply these checks before executing:

| What to check | Severity | Examples |
|----------------|----------|----------|
| Source trust | WARNING | `dibbla run <https-url>` fetches and executes shell commands from the network. Treat it like `curl … \| bash`. Only run yamls from sources the user trusts (e.g. `github.com/dibbla-agents/*` bootstraps or yamls authored by the user themselves). If the URL is from an unknown third party, warn the user and offer `dibbla run --preview <url>` first to inspect the plan. |
| Work-dir side effects | INFO | URL-fetched yamls execute with the user's invocation CWD as the work dir. Bootstrap yamls typically `git clone` into that directory. If the user's CWD is not empty (e.g. has existing files), make sure the clone step won't collide — prefer `mkdir fresh-dir && cd fresh-dir` before running. |
| Self-install / self-update steps | INFO | Some template task files include steps like `brew upgrade dibbla` or `curl install.sh \| sh`. These replace the on-disk dibbla binary while dibbla itself is running. This is benign on macOS/Linux (the running process keeps the old mmap) but users won't see the new version until their next re-invocation. Mention this if it surfaces in the output. |

---

## Check 7: Multi-service manifest safety

Run when a `dibbla.yaml` (or `dibbla.yml`) is present at the deploy root. Skip otherwise.

| What to check | Severity | Examples |
|----------------|----------|----------|
| Every `public: true` service has a `port:` | BLOCKER | A service `public: true` without `port:` fails the deploy with `PUBLIC_MISSING_PORT`. |
| `depends_on:` references real services in the manifest | BLOCKER | `depends_on: [redis]` when no `redis` service exists. |
| No `depends_on:` cycle | BLOCKER | `web → worker → web`. The validator detects cycles. |
| `expose_to:` references real services in the manifest | BLOCKER | `expose_to: [api]` when no `api` service exists. |
| Resource sums fit org quota (8 services, 20 replicas, 8 CPU, 16Gi mem, 50Gi PVC) | BLOCKER | A `replicas: 12` per service that pushes the total over 20. Surfaces as `QUOTA_EXCEEDED`. |
| Image refs include a tag | BLOCKER | `image: redis` (rejected); use `image: redis:7`. |
| No reserved service names (`proxy`, `auth`, `system`, `dibbla`, `kube-*`) | BLOCKER | `services: { proxy: ... }`. |
| Build context exists in the archive | BLOCKER | `build: ./web` when no `./web` directory exists. |
| Init containers exit cleanly | WARNING | An init that runs forever (e.g. `command: [sh, -c, "while true; do sleep 60; done"]`) blocks the rollout and times out the deploy. Inits are for migrations, schema sync, asset hydration — not long-running processes. |
| Healthcheck `failure_threshold` ≥ 3 in production | WARNING | `failure_threshold: 1` will kill the pod on a single transient failure. |
| Build-time secrets referenced in `build.secrets:` exist as dibbla secrets | BLOCKER | `secrets: [{id: npm, source: NPM_TOKEN}]` requires `dibbla secrets list -d <alias>` to show `NPM_TOKEN`. |
| Multiple `public: true` services | INFO | Each gets `<alias>-<service>.<base-domain>`; the lex-first one also gets the bare `<alias>.<base-domain>`. Confirm the user knows the URL shape and which service owns the bare alias. |
| Per-service auth missing on a sensitive public service | WARNING | If a public service name suggests an admin/internal UI (`pgadmin`, `adminer`, `mailhog`, `bull`, `redis-commander`, `grafana`, `prometheus`, or names containing `admin` / `internal` / `debug` / `tools`), require explicit user confirmation that **either** the service has `auth.require_login: true` set, **or** it's gated by `profiles: [dev]`. Shipping an admin UI publicly without auth is a top OWASP-class mistake. |
| Hostname collision with existing alias | BLOCKER | Multi-public deploys produce hyphenated hostnames `<alias>-<service>`. If any existing alias in the org matches one of those strings, the deploy fails with `ALIAS_HOSTNAME_COLLISION`. Surface to the user before deploy by checking `dibbla apps list` for collisions, especially on aliases that already contain hyphens. |

When a `dibbla.yaml` is present, run `dibbla manifest validate` before the deploy and confirm the result with the user. For env-aware / quota / cross-service-reference checks, also run `dibbla preview --target-env <env>` — the local validator can't see env-aware values or org quotas. Validating first also sidesteps a known issue on CLI ≤ v1.2.43 where a deploy that fails local validation prints nothing and exits 0 (see manifest.md §18) — never interpret a silent `dibbla deploy` as success.

---

## Check 8: User handbook (end-user documentation)

Mandatory for every deploy. The platform renders a user-facing handbook inside `app.dibbla.com` under "My Apps → {alias}" — this is the only documentation surface end users see. See [user-docs.md](user-docs.md) for the full audience guidance, file conventions, tone rules, and paste-ready templates.

| What to check | Severity | Examples |
|----------------|----------|----------|
| At least one of `docs/index.md` or `APP.md` exists at the project root | BLOCKER | Neither file present at the deploy root. The platform refuses the deploy (`REVIEW_INCOMPLETE`) on every path — refuse to deploy until the user agrees to ship documentation. |
| When `docs/` exists, `docs/index.md` is present | BLOCKER | A `docs/` folder with no `index.md` — the deploy will fail with a clear error. Generate the landing page from the template in [user-docs.md](user-docs.md). |
| The landing page (`docs/index.md` or `APP.md`) has a `subtitle:` frontmatter, and it is end-user-facing | BLOCKER | Missing frontmatter, or `subtitle:` absent, or the value still contains placeholders (`TBD`, `TODO`, `<one short…>`, `{{app_name}}`), or it leaks technical detail (framework names, "deployed via X", "Node.js", env-var names). The card on the My Apps grid relies on this single line — without it, end users see "Deployed application" as the blurb. Write a real subtitle following the rules in [user-docs.md](user-docs.md). |
| Subtitle is ≤ 140 bytes (target ≤ 70 chars), one sentence, plain text | BLOCKER | The bundler rejects subtitles over 140 bytes. The auth-ui My Apps card is ~180px wide and CSS-clamps to two lines, so anything past ~70 English chars gets visually clipped. Trim until it fits one tight sentence — start with a verb ("Track…", "Send…", "Manage…"), drop filler like "This is an app for…". No emoji, no markdown, no multi-line. |
| Handbook content is for the **end user**, not for developers | BLOCKER | `docs/` or `APP.md` describes the dev stack ("Built with Vite + React + TailwindCSS"), env vars (`DATABASE_URL`), Docker/Dockerfile, deploy commands, source paths, or framework names. Strip and rewrite for the end user (see [user-docs.md](user-docs.md) anti-examples table). |
| Per-page size ≤ 200 KiB; total bundle ≤ 800 KiB | BLOCKER | The deploy fails with `Invalid user docs:` or `User docs bundle too large:` — split into smaller pages or move large assets out of `docs/`. |
| `_nav.yaml` references valid page slugs (if present) | BLOCKER | `_nav.yaml` mentions a slug for which no `.md` exists — the deploy fails with `references missing page`. |
| Handbook covers Welcome, Getting Started, and FAQ at minimum | WARNING | A handbook with only one page and no "How do I…" guidance leaves users stuck — propose adding the three core sections from [user-docs.md](user-docs.md) before deploy. |
| No placeholder text (`TBD`, `TODO`, `lorem ipsum`, `{{app_name}}` placeholders) remains | WARNING | The templates use `{{app_name}}` / `{{org_name}}` — these MUST be replaced with real values before deploy. |
| Cross-page relative links resolve to real pages | WARNING | `[Foo](./foo.md)` where `docs/foo.md` doesn't exist — broken links don't block the deploy but show a "page not found" card to the user. |

**Workflow when handbook is missing:**

1. Tell the user the handbook is missing.
2. Offer to generate a starter handbook from the templates in [user-docs.md](user-docs.md) — `docs/index.md` + `docs/getting-started.md` + `docs/faq.md` at minimum.
3. Fill in the templates with content specific to the app (do not invent features that don't exist; ask the user what the app does and what the user-facing flows are).
4. **Write a real `subtitle:` frontmatter on `docs/index.md`** — one short user-facing sentence, ≤ 70 chars (hard cap 140 bytes), starts with a verb, sentence case, ends with a period. This is what shows on the card.
5. Show the user the generated files and wait for explicit confirmation.
6. Only then deploy.

**Workflow when content is technical, not user-facing:**

1. Show the user the offending lines (with file path + line number).
2. Propose a rewrite that strips dev-stack info and reframes the content for an end user.
3. Apply the rewrite and re-run Check 8.

---

## Check 9: Support reachability (P-0024)

Run when `dibbla.yaml` sets `support.enabled: true`. Skip otherwise.

| What to check | Severity | Examples |
|----------------|----------|----------|
| The app gives users a visible way to reach support | WARNING | `support.enabled: true` but no `<script src="/_platform/support.js"></script>` tag anywhere in the app's HTML and no link to `app.<domain>/apps/<alias>/support` — the org opted into tickets its users cannot file. Suggest adding the one-line widget tag to the app's layout. Deliberately a warning, never a blocker: the handbook gate is already blocking, and two blocking UX gates would be too coercive. |
| `visibility` matches the app's audience | INFO | A public app (`require_login: false` / policy open) with default `visibility: app` means every signed-in user can read every ticket — fine for a community tool, wrong if tickets may carry private detail. Mention `visibility: own`. |

---

## Check 10: Build-context readiness (P-0009)

Runs on **every** deploy that has a `Dockerfile` — which is every deploy.

`deploy-api` silently strips eight regenerable directories out of the uploaded archive **before** it becomes the Docker build context. A `COPY` of one of them builds fine on the developer's machine and fails on the platform with `BUILD_FAILED` on the `copy-source` step, ending `"/vendor": not found` — pointing at a directory the user can see sitting in their working tree. Catch it here, before the deploy is attempted.

The eight: `node_modules/` · `.git/` · `__pycache__/` · `.venv/` · `vendor/` · `.next/` · `dist/` · `.cache/`
(Source: `app-hosting-service/deploy-api/internal/extractor/extractor.go`, `skippedDirs`, as of 2026-08-23. Full rationale in `reference.md` → deploy → "Build-context strip (`skippedDirs`)".)

| What to check | Severity | Examples |
|----------------|----------|----------|
| A `COPY` or `ADD` in the `Dockerfile` whose **source operand resolves into one of the eight stripped directories** | **BLOCKER** | `COPY vendor/ ./vendor/`, `COPY dist ./dist`, `COPY .next ./.next`, `COPY ./web/dist /usr/share/nginx/html`. Fix by regenerating in the build: `RUN go mod download` (Go), `RUN npm ci && npm run build` (Node), `RUN pip install -r requirements.txt` (Python) — or move the artifact into a build stage and use `COPY --from=`. |

**Two precision rules. Without them this check is noise, and a noisy check trains agents to ignore it.**

1. **`COPY --from=<stage>` is exempt — always.** `COPY --from=builder /app/dist ./dist` copies from an earlier *build stage*, not from the upload archive. It is the pattern this check steers people towards, so flagging it would be actively harmful. The same applies to `COPY --from=<image>`.
2. **Match on the source operand, not on the line.** The check fires when a `COPY`/`ADD` *source* path resolves into one of the eight directories. It does **not** fire because one of the names appears somewhere else on the line. `COPY . .` does not fire (it copies whatever survived, which is correct). `COPY src/dist.go ./` does not fire — `dist.go` is a file, not the `dist/` directory. `COPY dist/ ./dist` does fire.

**Why BLOCKER and not WARNING** — this is a deliberate departure from the precedent Check 9 set (decision log D27 made support reachability a warning). Check 9 predicts a product-quality shortfall whose worst case is still a working deploy. This one predicts a **deterministic build failure**: the file is not there, `COPY` fails, the deploy fails, every time. That is the definition of BLOCKER above.

**If found:**

1. Show the user the offending `COPY`/`ADD` line with its file path and line number.
2. Explain that the directory is stripped server-side and will not be in the build context, however present it looks locally.
3. Propose the in-build regeneration (or the `--from=` stage) and apply it, then re-run Check 10.

Reference fixtures for this check live in `testdata/guardrail-fixtures/` — one Dockerfile that must trip it and one that must not.

---

## Interactive workflow

### Step 1: Run all applicable checks

Review the application source code against every check above, skipping only those whose own opening line says they do not apply to this deploy. Note each finding with its file path and line number.

### Step 2: Present the report

Show the user a guardrails report in this format:

```
## Pre-deploy guardrails report

- [x] Security (OWASP Top 10): OK
- [ ] Database usage: 1 BLOCKER, 2 warnings
  - BLOCKER: N+1 query in `src/routes/orders.js:42` — query inside forEach loop
  - WARNING: Unbounded SELECT in `src/models/users.js:18` — add LIMIT or pagination
  - WARNING: No connection pooling — consider using a connection pool
- [x] REST/API calls: 1 warning
  - WARNING: No timeout on fetch in `src/services/payment.js:23` — add a timeout
- [x] External writes: OK
- [ ] Personal data (GDPR): 1 warning
  - Stored: `users.email`, `users.name`, `orders.shipping_address`
  - Deletion: `DELETE /api/me` — cascades to `orders`
  - WARNING: `src/middleware/log.js:14` logs `req.body` on every request, including email and address — log `user_id` only
  - Third parties: Stripe (email, address — payment), Postmark (email, name — receipts)
- [ ] User handbook: 1 BLOCKER
  - BLOCKER: No `docs/index.md` or `APP.md` at project root — generate from templates in user-docs.md.
- [ ] Build-context readiness: 1 BLOCKER
  - BLOCKER: `Dockerfile:12` — `COPY vendor/ ./vendor/` copies a directory the server strips from the build context. Replace with `RUN go mod download`.

**Result: BLOCKED** — 3 blockers must be fixed before deploying.
```

### Step 3: Wait for user confirmation

- **If BLOCKERs found:** Tell the user what needs fixing and offer to fix it. Wait for their confirmation before making changes. After fixing, re-run the checks and present an updated report. Do NOT deploy until all blockers are resolved and the user confirms.
- **If only WARNINGs:** Ask the user: *"There are N warnings. Should I fix these before deploying, or proceed as-is?"*
- **If all clear:** Ask the user: *"All guardrails checks passed. Ready to deploy?"*

### Step 3.5: Write REVIEW.md

After completing the guardrails review and before deploying, write a `REVIEW.md` file in the project root directory. This file is read by the platform and displayed as a review status indicator in the deployments dashboard.

**Format:**

```markdown
---
Review-status: Ok | Warnings | Critical
One-Sentence-Summary: "<brief summary of findings>"
---

<full guardrails report from Step 2>
```

**Status mapping:**
- `Ok` — every applicable check passed with no blockers or warnings
- `Warnings` — no blockers found, but warnings are present (user chose to proceed)
- `Critical` — blockers were found and fixed before deploying

Always write this file, even when all checks pass. The platform shows a red indicator when REVIEW.md is missing, **and every deploy — `dibbla deploy`, `git push`, MCP — is refused without it (`REVIEW_INCOMPLETE`).**

### Step 4: Deploy only after confirmation

Only call `dibbla deploy` after the user has explicitly confirmed.
