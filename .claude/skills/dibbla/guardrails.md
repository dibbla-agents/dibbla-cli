# Dibbla CLI — Pre-deploy guardrails

Before calling `dibbla deploy`, you **MUST** complete all four checks below and present findings to the user. **Never deploy autonomously** — always wait for explicit user confirmation.

---

## Severity levels

- **BLOCKER** — Must fix before deploying. Do NOT call `dibbla deploy`.
- **WARNING** — Should fix. Present to the user and deploy only if they explicitly confirm.

---

## Check 1: Security (OWASP Top 10)

Scan all application source files for:

| What to check | Severity | Examples |
|----------------|----------|----------|
| Hardcoded secrets (API keys, passwords, tokens, connection strings) | BLOCKER | `const apiKey = "sk-..."`, `password = "admin123"`, strings matching `sk-`, `ak_`, `ghp_`, `password\s*=\s*["'][^"']+` |
| SQL injection (string concatenation/interpolation in queries) | BLOCKER | `` `SELECT * FROM users WHERE id = ${id}` ``, `"SELECT * FROM users WHERE id = " + id` |
| Command injection (unsanitized input in shell commands) | BLOCKER | `exec("rm " + userInput)`, `os.system(f"ls {path}")`, `child_process.exec(userInput)` |
| XSS (unsanitized user input rendered in HTML) | BLOCKER | `innerHTML = userInput`, `dangerouslySetInnerHTML` without sanitization |
| `.env` files present in the deploy directory | BLOCKER | `.env`, `.env.local` not in `.gitignore` / `.dockerignore` |
| Missing CSRF protection on state-changing endpoints | WARNING | POST/PUT/DELETE routes with no CSRF token or SameSite cookie |
| Insecure deserialization / eval | WARNING | `eval()` on user input, `pickle.loads()` on untrusted data, `yaml.load()` without `SafeLoader` |
| Missing input validation on API endpoints | WARNING | No request body validation, no type checking on route params |
| Sensitive data in logs | WARNING | Logging passwords, tokens, or PII to stdout/console |
| Missing security headers | WARNING | No `helmet()` (Node), no CORS configuration, no `Content-Security-Policy` |

---

## Check 2: Database usage

Scan all database-related code for:

| What to check | Severity | Examples |
|----------------|----------|----------|
| N+1 queries (query inside a loop) | BLOCKER | `for user in users: db.query("SELECT * FROM orders WHERE user_id = ...")` |
| Unbounded queries (SELECT without LIMIT) | WARNING | `SELECT * FROM large_table` with no `LIMIT` or pagination |
| Missing connection pooling | WARNING | Creating a new DB connection per request instead of using a pool |
| Missing error handling on DB operations | WARNING | No try/catch around queries, no transaction rollback on failure |
| Schema migrations without safeguards | WARNING | `DROP TABLE`, `DROP COLUMN` without backup or confirmation |

---

## Check 3: REST / API call patterns

Scan all outbound HTTP/API call code for:

| What to check | Severity | Examples |
|----------------|----------|----------|
| No timeout on outbound HTTP calls | BLOCKER | `fetch(url)` or `requests.get(url)` with no timeout option |
| Missing retry with exponential backoff | WARNING | Single HTTP call with no retry logic for transient failures (5xx, network errors) |
| Excessive polling (interval < 5 seconds) | WARNING | `setInterval(poll, 1000)`, tight polling loops |
| No error handling on API responses | WARNING | Not checking HTTP status codes, not handling network errors |
| Hardcoded external URLs | WARNING | Third-party API URLs inline in source instead of env vars / config |
| Missing rate limiting on inbound endpoints | WARNING | Public-facing API routes with no rate limiting middleware |

---

## Check 4: External system write safety

Scan code that writes to external systems (APIs, queues, email, SMS, webhooks, third-party services):

| What to check | Severity | Examples |
|----------------|----------|----------|
| Unbounded writes in a loop (no batching) | BLOCKER | `for item in items: api.post("/send", item)` — should batch or throttle |
| No rate limiting on outgoing calls | WARNING | Sending hundreds of emails/SMS/webhooks with no throttle or delay |
| Missing idempotency on write operations | WARNING | No idempotency key on payment, order creation, or webhook delivery calls |
| Fire-and-forget writes (no error handling) | WARNING | Write calls with no error capture, no retry, no dead-letter handling |
| Missing queue for bulk operations | WARNING | Synchronously sending thousands of notifications instead of using a job queue |

---

## Interactive workflow

### Step 1: Run all four checks

Review the application source code against every check above. Note each finding with its file path and line number.

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

**Result: BLOCKED** — 1 blocker must be fixed before deploying.
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
- `Ok` — all four checks passed with no blockers or warnings
- `Warnings` — no blockers found, but warnings are present (user chose to proceed)
- `Critical` — blockers were found and fixed before deploying

Always write this file, even when all checks pass. The platform shows a red indicator when REVIEW.md is missing.

### Step 4: Deploy only after confirmation

Only call `dibbla deploy` after the user has explicitly confirmed.
