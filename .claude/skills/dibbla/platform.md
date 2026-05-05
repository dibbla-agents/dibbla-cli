# Dibbla CLI — Platform compatibility

What your application must look like to build and run on Dibbla. Use this as the developer-facing mental model for Dockerfile shape, runtime expectations, env vars, and the upload boundary. Cross-links: [reference.md](reference.md) for full CLI flag/syntax detail, [guardrails.md](guardrails.md) for the pre-deploy **security** review (distinct from the compatibility checklist in §10 below).

---

## 1. Scope check — is this even a Dibbla project?

Before applying any of this, confirm at least one Dibbla marker exists in the project:

- A `.dibblaignore` at the deploy root.
- A `dibbla-task.yaml` file (handled by `dibbla run`).
- An `AGENTS.md` or `GEMINI.md` containing the `<!-- >>> dibbla skill >>> -->` markers written by `dibbla skills install`.
- A `.claude/skills/dibbla/` directory in the project.

If none of those are present, this is probably not a Dibbla-connected project — surface that to the user and stop applying Dibbla-specific advice. Generic Dockerfile/security feedback is fine; platform-specific guidance below is not.

---

## 2. Mental model

Your repo is bundled into a tar.gz, your `Dockerfile` is built into a container image, and the image is started behind a public URL at `https://<alias>.dibbla.com`. The platform terminates TLS upstream — your app speaks plain HTTP on whatever port you tell it to listen on. That's the whole shape; everything below is detail.

---

## 3. Dockerfile contract

A `Dockerfile` at the deploy root is **required**. The platform does not run buildpacks or auto-detect languages; if there is no `Dockerfile`, the build fails with `Dockerfile not found in the root of the archive`. Bundled templates in `dibbla-agents/dibbla-public-templates` ship working multi-stage examples — copy a pattern when scaffolding rather than inventing one.

- **Multi-stage builds** keep runtime image size and attack surface small. Build in one stage, copy artifacts into a minimal runtime stage (`node:20-alpine`, `python:3.12-slim`, `gcr.io/distroless/*`, `golang:*` → `gcr.io/distroless/static`).
- **Port matching is mandatory.** Whatever port you `EXPOSE` and listen on inside the container must match the value passed to `dibbla deploy --port <N>`. If you skip `--port`, the platform's default (`80`) is used. So a Node app that `EXPOSE`s `3000` must deploy with `--port 3000` — otherwise traffic is routed to a port nothing is bound to and the public URL hangs or 502s.
- **Listen on `0.0.0.0`, not `127.0.0.1`.** The container's loopback is unreachable from the routing layer; binding to localhost makes the app appear dead.
- **Run as non-root.** The platform does **not** force a non-root user — a Dockerfile that ends up as `root` runs as root. Add `RUN useradd -m app && USER app` (or equivalent in your base image) in the runtime stage.
- **`HEALTHCHECK` is optional but useful.** The platform performs its own post-deploy health check; an in-Dockerfile `HEALTHCHECK` improves debuggability when the platform reports unhealthy.
- **Do not bake secrets in.** `ARG` values and bake-time `ENV` lines land in the image and registry. Use runtime secret injection (§5) instead.

---

## 4. Runtime contract

- **Filesystem is ephemeral.** Anything written to disk is gone on restart, scale-out, or redeploy. Persist via managed Postgres (§6) or external object storage. Do not write user-visible state to local files.
- **Single ingress port.** Only the port matched to `--port` (or `80` by default) is reachable from the public URL. Side-channel ports, sidecar listeners, and direct container-to-container networking are not exposed.
- **Plain HTTP inside the container.** TLS is terminated by the platform's routing layer; do **not** run your own TLS in the container — it'll only see plain HTTP from the proxy.
- **PID 1.** Whatever your `CMD`/`ENTRYPOINT` runs is PID 1 — forward signals correctly, or use a small init shim (`tini`, `dumb-init`) if you spawn children.

---

## 5. Environment variables your app can rely on

User-app environment variables come from three places:

- **Secrets** — `dibbla secrets set <name> <value>` (org-global) or `dibbla secrets set <name> <value> -d <alias>` (scoped to one app). Injected as env vars at runtime. Secret name regex: `^[a-zA-Z][a-zA-Z0-9_]{0,127}$`.
- **`--env KEY=VAL`** on `dibbla deploy` and `dibbla apps update`. Persist across redeploys — set once, they stick.
- **Auto-injected database URL.** `dibbla db create <name>` creates a secret named `DATABASE_URL_<UPPERCASED_UNDERSCORED_NAME>` (e.g. `db create my-db` → `DATABASE_URL_MY_DB`). **There is no plain `DATABASE_URL`** unless you set one explicitly. App code must read the suffixed variable.

Use these channels — never hardcode secrets in the image, in source files committed to VCS, or in `.env` files in the deploy directory (§7 strips them anyway).

---

## 6. Managed Postgres — self-signed TLS

The Dibbla-managed Postgres serves a **self-signed certificate**. Connections are still encrypted in transit; trust is enforced by network isolation (the database is only reachable from inside the deployment cluster), not CA-rooted cert identity. Two consequences for app code:

- **Do not use `sslmode=disable`.** That drops encryption — wrong fix.
- **Do not require strict CA verification.** A default `sslmode=verify-full` client will refuse the cert. Either strip `sslmode` from the injected URL and configure SSL explicitly with `rejectUnauthorized: false` (Node `pg`, Prisma adapter), or use `sslmode=no-verify` (recent Prisma) or `sslmode=require` with no CA file (psycopg2).

Working snippets for `pg`, Prisma, and psycopg2 live in `reference.md` → "TLS for application database clients" (~line 336). Copy from there rather than inventing a new pattern — naive `ssl: { rejectUnauthorized: false }` is silently shadowed when the URL carries a stricter `sslmode`, which is the most common foot-gun.

---

## 7. Upload boundary — what gets shipped, what gets stripped

Two filtering layers sit between your working tree and the build:

1. **CLI-side exclusion list** strips, before the archive leaves your laptop:
   - SSH keys: `id_rsa`, `id_ed25519`, `id_ecdsa`, `id_dsa`
   - Service-account JSON: `credentials.json`, `service-account.json`
   - Production env files: `.env.production`, `.env.prod`
   - Generic key/cert files: `*.pem`, `*.key`
   - Native binaries: `.exe`, `.dll`, `.so`, `.dylib`, `.bat`, `.cmd`
2. **Server-side denylist** additionally strips `.env`, `.env.*`, `node_modules/`, `dist/`, `.venv/`, `.git/` from the managed VCS history and surfaces each match as a warning in `DeployResponse.vcs_filtered`.

The CLI enforces a **50 MB archive cap** before upload. Server-side per-file and per-commit size caps are hard rejections — exceeding them fails the deploy with `ErrCodeVCSFiltered`.

Use `.dibblaignore` (gitignore syntax, at the deploy root) to silence server-side warnings on intentionally-excluded paths and to keep generated/large artifacts out of Dibbla's managed VCS history. Full syntax in `reference.md` (~line 178). Note: `.dibblaignore` does **not** change what the Docker build sees — it's a VCS filter, not a build-context filter. If you need to keep something out of the image, use a `.dockerignore` as well.

---

## 8. Public URL & access control

- Default public URL: `https://<alias>.dibbla.com`.
- Gate access with `dibbla deploy --require-login` (any authenticated Dibbla user) plus `--access-policy invite_only` (only explicitly invited users) or `--access-policy all_members` (all org members).
- Request additional Google OAuth scopes (Drive, Calendar, etc.) via `--google-scopes`.
- TLS certificates and routing are managed by the platform — no app-side configuration needed.

---

## 9. Authentication contract — reading the user inside your app

When you deploy with `--require-login`, the platform proxy handles the OAuth flow upstream and only forwards the request to your container after the user is authenticated and authorised by the configured access policy. Your app never sees unauthenticated traffic and never has to validate a JWT itself — by the time a request reaches you, identity has been verified and access policy has been enforced. **You just read headers.**

### The injected headers

The proxy strips any inbound `X-User-*` / `X-Session-Id` headers from the client and replaces them with proxy-set values. Trust them as they arrive — they cannot be spoofed:

| Header | Meaning |
|---|---|
| `X-User-ID` | Stable unique user identifier — use this as the primary key in your DB |
| `X-User-Email` | User's email address |
| `X-User-Name` | Display name |
| `X-User-Org` | Organisation ID the user is acting as |
| `X-User-Org-Role` | Role within that org (e.g. `owner`, `member`) |
| `X-User-Org-Slug` | Organisation slug (URL-safe) |
| `X-User-GlobalAdmin` | `true` if the user is a Dibbla global admin (otherwise absent) |
| `X-Session-Id` | JWT `jti` for browser sessions; empty for API-token requests |

Minimal example (Node/Express):

```js
app.use((req, res, next) => {
  req.user = req.header('x-user-id') ? {
    id:    req.header('x-user-id'),
    email: req.header('x-user-email'),
    name:  req.header('x-user-name'),
    org:   req.header('x-user-org'),
    role:  req.header('x-user-org-role'),
  } : null;
  next();
});
```

Same pattern in any language — read the headers, hydrate a session/user object. **Never** verify, decode, or trust a JWT from the client; there isn't one in the auth contract for app-side use.

### Access policy is proxy-enforced, not app-enforced

`--access-policy invite_only` / `all_members` / `email_domain` is enforced at the proxy. Unauthorised users see a platform-rendered 403 denial page and your container is never reached. Inside your app:

- If the request reached your app, the user is authorised — don't re-check org membership / invite status.
- Do still enforce **per-resource** authorisation in your app (e.g. "this user can view their own todos but not someone else's") using `X-User-ID` / `X-User-Org`.

### Google OAuth scopes (`--google-scopes`)

When the user lacks the requested scopes (e.g. `https://www.googleapis.com/auth/drive.readonly`), the proxy intercepts the request and redirects to a re-consent flow. By the time a request reaches your app with `--google-scopes` set, the user has already granted those scopes.

To **call Google APIs as the user from your backend**, do not try to manage tokens yourself. Call the platform-mediated endpoint pattern:

```
GET /_platform/google/<resource>
```

The proxy forwards these to the platform's Google broker, which retrieves a fresh access token from auth-service and proxies the call. This means **you never see, store, or refresh a Google access token** — refresh is handled transparently. Each call gets a fresh token.

### Logout

If you need to log the user out, redirect them to `/__auth/logout`. The proxy clears the `auth_token` cookie for your app's domain and redirects back to `/`. The user's broader Dibbla portal session is left intact (logout is scoped to your app). Implementing your own session cookies on top of the proxy auth is unnecessary — the proxy cookie is the auth boundary.

### Without `--require-login`

If you do not pass `--require-login`, the proxy injects **no** `X-User-*` headers, even if the user happens to be logged in to Dibbla. This prevents identity leaking into apps that don't expect auth. Either:
- Leave the app fully public, or
- Deploy with `--require-login` so the platform handles auth, or
- Implement your own auth from scratch (cookies/JWT/etc.) — the platform will not interfere.

Mixing modes (e.g. some routes public, others authed) is the app's job — the proxy is binary per deployment.

### Local development

Headers are not magically injected when you run `docker run` locally. Either:

- Mock them with `curl -H "X-User-ID: dev-user" -H "X-User-Email: dev@example.com" ...`, or
- Add a dev-mode bypass in your app (`if process.env.NODE_ENV === 'development' && !req.user) req.user = MOCK_USER`), or
- Run your app behind a local reverse proxy that injects the headers.

Never ship a dev bypass to production. Gate it behind a build flag or env-var check that is impossible to flip on Dibbla (e.g. only honoured when `NODE_ENV !== 'production'`, and ensure you set `NODE_ENV=production` via `--env` at deploy).

---

## 10. What the platform does *not* do for you

The platform is intentionally minimal at the container boundary. It does **not**:

- Force a non-root user (your Dockerfile owns this).
- Mount the root filesystem read-only.
- Run image vulnerability scans.
- Filter outbound network egress.
- Apply request-size limits or rate limits.

These are application-side concerns. For the security review before deploy, run the OWASP-driven checklist in `guardrails.md` — it is distinct from the compatibility checklist in §11.

---

## 11. Compatibility checklist (will it boot and serve traffic?)

**Compatibility, not security.** This list answers "will my app run on Dibbla?" — not "is my app safe to expose?" The security half lives in `guardrails.md` and must be run separately before any deploy.

- [ ] `Dockerfile` at the deploy root.
- [ ] Image `EXPOSE`s and listens on the same port passed to `--port` (or `80` if `--port` is omitted).
- [ ] App binds to `0.0.0.0`, not `127.0.0.1`.
- [ ] `USER` directive sets a non-root user in the runtime stage.
- [ ] All secrets come from `dibbla secrets set` or `--env`, never hardcoded or baked into the image.
- [ ] Postgres client configured to accept the self-signed cert (see §6); `sslmode=disable` is **not** used.
- [ ] If `--require-login`, app reads `X-User-*` headers (see §9) and does not attempt JWT verification.
- [ ] No `.env`, `*.pem`, `*.key`, SSH keys, or `service-account.json` in the deploy directory.
- [ ] `.dibblaignore` covers build outputs and any large generated files.
- [ ] App tolerates an ephemeral local filesystem; persistent state lives in Postgres or external storage.
- [ ] Pre-deploy security review (`guardrails.md`) completed and `REVIEW.md` written before deploying.
