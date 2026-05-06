---
name: dibbla
description: Use the Dibbla CLI to scaffold projects, run dibbla-task.yaml pipelines locally (dibbla run), install project templates (dibbla template list/install), install this skill into a project so other AI coding agents read it too (dibbla skills install dibbla), uninstall the CLI itself and clean up its config/credentials/skill artifacts (dibbla uninstall, with --dry-run, --keep-config, --keep-skills, --skill-only), deploy apps, manage apps/databases/secrets, design/iterate workflows on the Dibbla platform, and build Go workers against the Dibbla Go SDK (`github.com/dibbla-agents/sdk-go`). Use when the user wants to run a local task file or template from a URL, install a starter template, install the dibbla skill into a project or home dir for Claude Code/Cursor/Gemini CLI/Opencode/Codex, log in (including from non-TTY contexts via `dibbla login --browser`), uninstall dibbla (e.g. "how do I remove dibbla", "uninstall dibbla", "clean up dibbla skill files everywhere it was installed"), deploy, manage apps/databases/secrets, design and operate workflows, or implement Dibbla functions and jobs in Go via the SDK — author a slim YAML workflow that wires an LLM agent to tools, validate it with `dibbla wf validate`, deploy it with `wf create`/`wf update`, iterate it via `nodes add`/`edges add`/`inputs set`/`tools add`, snapshot with `revisions create`, roll back with `revisions restore`, execute via `wf execute` or the HTTP endpoint exposed by `wf api-docs`, and ship custom Go functions/jobs by registering them on a `*sdk.Server` and connecting it to the platform.
when_to_use: Also trigger on Dockerfile review/authoring, `.dibblaignore`, deploy-readiness, auth integration, runtime log access, build-time vs runtime env vars (Vite/Next/CRA), or platform-compatibility questions for apps destined for Dibbla — e.g. "review my Dockerfile for Dibbla", "is my app ready to deploy", "how do I read the logged-in user", "what headers does Dibbla inject", "why does my Postgres TLS connection fail", "how do I view logs / debug a 500", "why is my VITE_* env var undefined", or "what should be in .dibblaignore". Also trigger on multi-service / `dibbla.yaml` questions — "how do I declare a worker alongside my web app", "what's `dibbla.yaml`", "how do I restart just the worker", "how do I validate my manifest locally", "what does `dibbla preview` do", "how do I scope a secret to one service", "how do I have multiple public URLs per app", "how do I lock down pgadmin in prod but leave it open in dev", "how do I substitute env vars in dibbla.yaml", "what's the auth field on a service", "how do I pass a build-time secret to docker build", "what does ALIAS_HOSTNAME_COLLISION mean", "how does service discovery work between services in one deploy", "how do I add an init container for migrations", "how do I run a cron job in my deploy", "how do I expose a custom domain", "how do I pass a build-time secret to docker build", "what's `expose_to`", "what's the difference between profiles and env-aware fields". Also trigger on workflow design/authoring questions — e.g. "build a workflow that asks an LLM and calls a weather tool", "wire this tool to that agent", "what node types exist", "why does my workflow fail validation", "what's `UNSATISFIED_INPUT`", "snapshot the workflow before I edit it", "what functions are in the registry", "how do I call my workflow over HTTP". Also trigger on Go SDK / `sdk-go` worker questions — e.g. "build a Go worker that registers a function", "implement a JobHandler that reports progress", "register a function with sdk.New", "what's the difference between SimpleFunction and Function in sdk-go", "how do I get a Google OAuth token from inside a workflow function", "why won't `internal/state` import from my own module", "the JobHost API doesn't exist anymore — what replaced it", "what env vars does the Go SDK read", "how do I call `server.RegisterJob`", "how do I deploy my Go worker to Dibbla". Skill ships platform.md (Dockerfile contract, port-matching, runtime env, build-time-vs-runtime env, Postgres TLS, `X-User-*` auth headers, Google OAuth brokering, upload boundary, multi-service runtime contract, compatibility checklist), manifest.md (full `dibbla.yaml` schema — services, jobs, env-aware fields, profiles, service discovery, expose_to/NetworkPolicy, volumes, init containers, healthchecks, multi-public, custom domains, cron, build secrets, quotas), workflows.md (slim YAML format, node-type roles, agent+tool wiring, validator errors, revisions, execution semantics, canonical shapes), sdk-go.md (Go SDK reference: server bootstrap with `sdk.New`, `SimpleFunction` vs advanced `Function[In, Out]`, the `JobHandler` interface + `server.RegisterJob`, the `Logger` task/progress API, OAuth via `gs.OAuth`, the `internal/` import footgun that restricts external modules to `SimpleFunction`, gotchas), plus `dibbla logs <app>` reference for live debugging.
---

# Dibbla CLI

The `dibbla` CLI scaffolds projects and manages **applications**, **databases**, **secrets**, and **workflows** on the Dibbla platform. Deployed apps are available at `https://<alias>.dibbla.com`.

## Prerequisites

**Install the CLI** if it isn't already on the user's `PATH`:

| Platform | Command |
|----------|---------|
| macOS (Homebrew) | `brew install dibbla-agents/tap/dibbla` |
| macOS / Linux (shell installer) | `curl -fsSL https://install.dibbla.com/install.sh \| sh` |
| Windows (PowerShell) | `powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://install.dibbla.com/install.ps1 \| iex"` |
| Verify | `dibbla --version` |

The shell installer drops the binary into `~/.local/bin` and adjusts `PATH` if needed. Self-update is available inside task files via the same installer URL.

**Deploying requires a `Dockerfile`** at the root of the directory you pass to `dibbla deploy`. The CLI does **not** auto-detect languages or generate a Dockerfile — if it's missing, the backend rejects the build with log output. All bundled templates in `dibbla-agents/dibbla-public-templates` ship a working Dockerfile you can copy (typically multi-stage: Node → JS build → Go → binary → small runtime image, `EXPOSE 80`).

## Commands at a glance

| Area       | Commands |
|------------|----------|
| Run        | `run [path\|url]`, `run --preview`, `run --env KEY=VAL`, `run --env-file <file>`, `run --work-dir <dir>`, `run --format plain\|gh` |
| Template   | `template list [--refresh] [-v]`, `template install <id> [<dir>] [--force]` |
| Skills     | `skills list`, `skills install <id>` (`--user`, `--force`, `--no-agents`) — install AI-agent guidance into `.claude/skills/` + `AGENTS.md` + `GEMINI.md` |
| Setup      | `init` (interactive setup wizard: update → login → install dibbla skill), `update [--check] [--version vX.Y.Z]` (self-update; defers to brew/apt/rpm/scoop/choco when one owns the binary), `uninstall [--dry-run] [--keep-config] [--keep-skills] [--skill-only]` (removes binary on script installs, keychain creds, `~/.config/dibbla/`, `~/.dibbla/`, and skill files at every recorded install root; for package-manager installs prints the native uninstall command instead of touching the binary) |
| Login      | `login [api_url]`, `login --browser`, `login --api-key <token>`, `login --api-url <url>`, `login --write-env`, `login --no-keychain`, `logout` |
| Feedback   | `feedback <message>`, `feedback list`, `feedback delete <id>` |
| Deploy     | `deploy [path] -m "<msg>" [--alias name] [--update] [--require-login] [--access-policy] [--google-scopes] [--target-env <env>] [--profile <p>]` — deploy from directory; `-m` becomes the VCS commit subject. `--target-env` / `--profile` / `--no-public` only apply when a `dibbla.yaml` is at the deploy root |
| Manifest   | `manifest validate [path]` — local schema check for `dibbla.yaml` (no network) |
| Preview    | `preview [path] [--target-env <env>] [--profile <p>] [--no-public]` — server-authoritative dry run; full env-aware resolution + quota check, no build, no apply |
| Apps       | `apps list`, `apps update <alias>`, `apps delete <alias>`, `apps restart <alias> --service <name>` (per-service rolling restart) |
| Logs       | `logs <app>` (last 15m), `logs <app> --since 24h`, `logs <app> -f` (follow), `logs <app> -n 200` (tail), `logs <app> --grep <regex>`, `logs <app> --json` — runtime logs from Loki; `logs <app> --service <name>` filters to one service; `logs <app> --service <name> --pod-stream` streams pod logs via the K8s API when Loki isn't available |
| Db         | `db list`, `db create`, `db delete`, `db dump`, `db restore`, `db connect` |
| Secrets    | `secrets list`, `secrets set`, `secrets get`, `secrets delete` (global, `-d <alias>` for deployment-wide, or `-d <alias> --service <name>` for per-service) |
| Admin      | `admin reconcile` — force one orphan-resource sweep on the deploy-api instance (gated by `DIBBLA_ADMIN_TOKEN`) |
| Workflows  | `workflows list`, `get`, `create`, `update`, `delete`, `validate`, `execute`, `url`, `api-docs` |
| Nodes      | `nodes add <wf>`, `nodes remove <wf> <id>` |
| Edges      | `edges add <wf> "<edge>"`, `edges remove`, `edges list` |
| Inputs     | `inputs set <wf> <node> <input> <value>` |
| Tools      | `tools add <wf> <agent> <tool>`, `tools remove` |
| Revisions  | `revisions list <wf>`, `revisions create`, `revisions restore` |
| Functions  | `functions list`, `functions get <server> <name>` |

## Agent guidelines

**Interactive prompts:** The following commands prompt for confirmation and will block if run non-interactively. Always pass `--yes` (or `-y`) when running these as an agent:
- `dibbla apps delete <alias> --yes`
- `dibbla db delete <name> --yes`
- `dibbla secrets delete <name> --yes`
- `dibbla workflows delete <name> --yes`
- `dibbla nodes remove <wf> <id> --yes`
- `dibbla feedback delete <id> --yes`

**Deploying an app for the first time:**
1. Check if the app already exists: `dibbla apps list`
2. If it does **not** exist, deploy with all required environment variables included in the deploy command — there is no app to attach them to yet:
   ```bash
   dibbla deploy . --alias my-app -m "feat: initial deploy" \
     -e DATABASE_URL=postgres://... -e API_KEY=secret -e NODE_ENV=production
   ```
3. If it **already** exists, use `--update` for a zero-downtime rolling update:
   ```bash
   dibbla deploy . --alias my-app -m "fix: resolve 500 on /search" --update
   ```
   To change env vars on an existing app, use `apps update` instead:
   ```bash
   dibbla apps update my-app -e NEW_VAR=value
   ```

**Key rules:**
- **Every `dibbla deploy` must include `-m "<message>"`.** The value becomes the git commit subject in the app's Dibbla-managed VCS history (and on the GitHub mirror, if configured). Treat it like a git commit: present-tense imperative, under ~72 chars, covering what changed and why — e.g. `-m "fix: handle null org in /api/me"`, `-m "feat: add nightly db backup workflow"`, `-m "chore: bump node to 20.14"`. For retries or mechanical redeploys, still say so explicitly: `-m "redeploy: retry after CF 524"`. Max 500 chars. Never run `dibbla deploy` without `-m`; a blank deploy history is a bug, not a default.
- `--force` causes downtime (tears down and redeploys). Prefer `--update` for existing apps.
- `--force` and `--update` are mutually exclusive.
- Environment variables set via `deploy -e` or `apps update -e` persist across updates — you only need to pass them once.
- **Login guard:** Use `--require-login` to require authentication. Combine with `--access-policy invite_only` to restrict to invited users, or `all_members` for org-wide access. Use `--google-scopes` to request additional Google OAuth scopes (e.g. Drive, Calendar).
- Use `--quiet` / `-q` on `db list`, `db delete`, `db connect` for machine-readable output in scripts.
- `db create --deployment <alias>` scopes the database and its auto-created secret to a specific deployment. The scoped secret is named `DATABASE_URL_<UPPERCASED_UNDERSCORED_NAME>` (e.g. `DATABASE_URL_MY_DB` for database `my_db`), **not** a plain `DATABASE_URL` — app code must read the suffixed env var.
- `db connect` prints a psql-compatible connection string via the Dibbla database proxy. Use `-q` for scripting: `psql $(dibbla db connect mydb -q)`.
- **524 on deploy ≠ failure.** `dibbla deploy` holds a single HTTP connection during the backend build; builds over ~100s may return a Cloudflare 524 on the client even when the backend succeeds. Wait 2–5 minutes, then run `dibbla apps list` to check. Do **not** retry with `--force` — use `--update` if you must retry.
- **Output modes:** `dibbla deploy` streams a live buildkit-style step view when stdout is a TTY and switches to ISO-timestamped log lines (`<ts> [info] build step=N/M …`) when stdout is piped or in CI. Add `--quiet` for a single-line success/failure (script-friendly) or `--json` for a single structured object on stdout. On build failure the non-TTY mode also writes one structured JSON line to **stderr** with shape `{"event":"deploy.failed","step":"go-build","step_index":N,"step_count":M,"errors":[{file,line,col,message}],"retry_cmd":"…","api_error_code":"BUILD_FAILED"}` — coding agents should read this from stderr to locate failing files without scraping the human-readable build output. Add `--verbose-build` to ship the full server build log instead of the elided tail when parsed compile diagnostics aren't enough. Build failures exit `2`; other errors exit `1`.
- **`.dibblaignore` controls Dibbla's managed VCS history**, not what the Docker build sees. The backend always strips `.env`, `node_modules/`, `dist/`, `*.pem`, `*.key` and similar from VCS and reports each hit in `DeployResponse.vcs_filtered` as a warning. Adding those paths (or any generated/large artifact) to `.dibblaignore` at the deploy root silences the warning and keeps VCS clean. Per-file and per-commit size caps are hard rejections — committing a large build artifact will fail the deploy with `ErrCodeVCSFiltered`; the fix is to add the path to `.dibblaignore`. Full details in `reference.md` → deploy → `.dibblaignore`.
- Managed Postgres uses a **self-signed TLS cert**. App clients (pg, psycopg2, Prisma) need explicit SSL handling — see `reference.md` "TLS for application database clients" for working snippets.

**Designing a multi-service manifest:** Before authoring a `dibbla.yaml`, work through these design questions and confirm a plan with the user. Skipping this step at design time leads to retrofits that touch every consumer service (env vars, `depends_on`, service-discovery references), so it's worth the 60 seconds upfront.

- *Which services should exist in only some envs?* (e.g. an inline DB container in dev, a managed/external DB in prod) → put `profiles: [dev]` on the env-specific service. Decide this **upfront** because consumers will need env-aware values for the URL/host that points to it.
- *Which fields differ across envs?* (`replicas`, `image`, `MONGO_URL`, `LOG_LEVEL`, …) → use **env-aware** field maps (§ 6 in [manifest.md](manifest.md)). Different mechanism from profiles: profiles toggle whether a service exists at all; env-aware fields shape an existing service.
- *Where will the data layer live in prod?* If managed/external, the consumer needs an env-aware `MONGO_URL` / `DATABASE_URL` (`default:` → external value, `dev:` → `${DIBBLA_SVC_*}`) **and** the inline copy needs `profiles: [dev]`. The two mechanisms are paired — see § 7 in [manifest.md](manifest.md) for a worked example.
- *How will the user iterate locally?* The platform does **not** run `dibbla.yaml` locally — there is no `dibbla up`. Mirror the manifest into a `docker-compose.yml` next to it for tight inner-loop dev (see [examples.md](examples.md) "Local iteration with docker-compose"). The two diverge in details (no `${DIBBLA_SVC_*}`, no NetworkPolicy, no env-aware resolution) but match in shape.
- *Will any public service be sensitive in prod?* (admin UIs, debug consoles, mail catchers, internal dashboards) → per-service `auth:` block with `require_login: true` and an `access_policy:`, or gate the whole service with `profiles: [dev]`. Shipping an admin UI publicly without auth is a top OWASP-class mistake; the guardrails check enforces this in [guardrails.md](guardrails.md).

**Pre-deploy guardrails:** Before calling `dibbla deploy`, you MUST complete the pre-deploy checklist and present findings to the user. Always wait for explicit user confirmation before deploying or fixing issues — never deploy autonomously. The guardrails workflow also writes a `REVIEW.md` file to the project root — the platform reads this and displays a review status indicator in the dashboard. See [guardrails.md](guardrails.md) for the full checklist.

**Workflows:** A workflow is a typed DAG of function calls — nodes name a `function` from the registry, edges carry data port-to-port, an `api` node + `api_response` node make it callable over HTTP. Author in **slim YAML** (the format `wf get`/`wf create -f` consume); never hand-write the verbose React-Flow JSON. Minimal shape:

```yaml
name: my_workflow
nodes:
  - {id: api_input,  type: api,          inputs: [question], outputs: [question]}
  - {id: greet,      type: function,     function: handlebars_template,
     server: go-function-server1,        inputs: {script: "Hello {{question}}!"},
     outputs: [error, output]}
  - {id: api_response, type: api_response, linked_to: api_input, inputs: [response]}
edges:
  - api_input.question -> greet.question
  - greet.output -> api_response.response
```

Before authoring anything non-trivial, run `dibbla fn list` to see what functions exist and `dibbla wf get <existing> -o yaml` on a similar workflow for shape — the function registry, not the YAML, is the source of truth. Pick the iteration loop that matches the change size: small tweak → patch HEAD with `nodes add`/`edges add`/`inputs set`/`tools add`; structural change → `wf get … -o yaml` → edit → `wf update -f`. Always `dibbla revisions create <wf>` before either; patches are not auto-snapshotted and `revisions restore` overwrites HEAD (it's not a checkout). For the complete model — node-type roles, the agent+tool pattern, all 13 validator errors and their fixes, execution/HTTP semantics, and the three canonical workflow shapes (transform, agent+tools, multi-stage pipeline) — see [workflows.md](workflows.md).

**Workflow gotchas that bite once:**
- **Pick `reasoning_agent_function` for new agents** — `reasoning_agent_with_thread` has been observed to silently return empty responses with current Claude models. Always wire `agent.error -> api_response.error` so silent failures surface.
- **Production callers must use the gateway URL**, not the URL `wf api-docs` prints. Rewrite host: `https://workflow-server.dibbla.net/api/execute/<name>/<urlid>` (shown by `api-docs`, internal only) → `https://api.dibbla.net/api/wf/execute/<name>/<urlid>` (gateway, accepts `Authorization: Bearer ak_<workflow-api-key>`).
- **After many `wf update` iterations, recreate the workflow before shipping** — the `<urlid>` in the gateway URL can go silently stale (calls hang for ~5 minutes with no error). `wf delete --yes && wf create` gives a fresh id.
- **Node ids collapse to the function name on `wf create`.** Don't pick custom ids; refer to tools by function name.
- **Result cache is 1 hour** on `reasoning_agent_function`. During iterative testing, vary the input or use a `*_no_cache` variant.
- **Always wrap workflow fetches in an `AbortController` with a 30–60s timeout** and log before/after — Node's default 5-minute timeout makes failures look like hangs.

**Building Go workers (sdk-go):** The `github.com/dibbla-agents/sdk-go` Go SDK is how workers register custom **functions** and **jobs** with the platform. A worker is a long-lived gRPC client: `sdk.New(...)` → `server.RegisterFunction(...)` and `server.RegisterJob(...)` → `server.Start()` (which blocks forever). External user modules are restricted to `sdk.NewSimpleFunction[In, Out]` because the advanced `Function[In, Out]` handler signature exposes `internal/types` and `internal/state` — Go's `internal/` rule blocks those imports from any module other than `sdk-go`. The `JobHost` abstraction was removed; jobs register directly via `server.RegisterJob(handler)`. Once the worker is connected, its functions appear in `dibbla functions list` and become callable from workflow YAML by `(server, function)` pair (see [workflows.md](workflows.md) for consumer-side wiring). For the full SDK model — server options, function builders, the `JobHandler` interface, `JobContext` arg helpers, the `Logger` task/progress API, OAuth via `gs.OAuth`, and gotchas — see [sdk-go.md](sdk-go.md).

**Non-TTY / agentic invocation:**
- When running from inside Claude Code's `!` prefix, an agent shell, CI with a browser, or any other non-TTY context, use `dibbla login --browser` instead of bare `dibbla login`. The interactive flow needs stdin for the survey picker; `--browser` skips that and goes straight to browser-based OAuth via a localhost callback.
- For true headless (SSH sessions, cloud VMs, CI runners with no local browser), use `dibbla login --api-key <token>` or set `DIBBLA_API_TOKEN` (and optionally `DIBBLA_API_URL`) env vars — the CLI reads env vars in CI automatically.
- **Cloud VMs / SSH / Docker (no keyring):** `dibbla login --api-key=<t> --api-url=<url> --write-env --no-keychain` validates the token against the API and writes `DIBBLA_API_TOKEN` + `DIBBLA_API_URL` to `./.env` (patching `.gitignore` if needed), without touching the OS keyring. Use this on fresh Ubuntu/EC2/GCE/Docker images where libsecret/gnome-keyring/pass isn't installed. Every subsequent `dibbla *` command in that directory reads credentials from `.env`. Requires CLI ≥ v1.2.4.
- **`.env` in CWD is read by every command, including `login`.** Put `DIBBLA_API_TOKEN=…` and `DIBBLA_API_URL=https://api.dibbla.net` in `./.env` and every `dibbla` invocation from that directory targets that server and token — no `login` call needed. Shell-exported vars still win over `.env` (godotenv does not overwrite). Requires CLI ≥ v1.2.4.
- `DIBBLA_AUTH_SERVICE_URL` is an internal compat alias for `DIBBLA_API_URL`, injected by the steprunner into child processes launched by `dibbla run`. Users should put `DIBBLA_API_URL` in `.env`; `DIBBLA_AUTH_SERVICE_URL` exists so child processes see the same server via the desktop/steprunner convention name.

**Running task files and templates:**
- `dibbla run <path>` executes a `dibbla-task.yaml` pipeline locally. Tool checks, shell commands, background dev servers, and browser-open side effects are all possible — the task file becomes shell under the user's account.
- `dibbla run <https-url>` fetches and executes a yaml from the network. **This is equivalent to `curl | bash`** — only run yamls from sources the user trusts (e.g. `github.com/dibbla-agents/*`). Work-dir defaults to the user's invocation CWD, so bootstrap clones land in the expected directory rather than in a temp dir.
- `dibbla template install <id>` is ergonomic sugar over `mkdir ./<template-path> && cd ./<template-path> && dibbla run <bootstrap-url>`. It refuses if the destination directory exists; pass `--force` to reuse. Use `dibbla template list` to see available ids.
- Prefer `dibbla run --preview` or `dibbla template list` before actually running, so the user can see what will execute.

**Installing this skill into a project (so other agents see it too):**
- `dibbla skills install dibbla` writes the skill files into `./.claude/skills/dibbla/` plus `AGENTS.md` and `GEMINI.md` pointers at the project root. Every major coding agent then picks up the guidance automatically — Claude Code via its native skill path, Cursor/Opencode/Codex/Copilot/Windsurf/Aider via `AGENTS.md` (the 2026 open standard), Gemini CLI via `GEMINI.md`.
- The skill content is embedded in the CLI binary (`go:embed`), so no network is required and the skill version is locked to the CLI version the user has installed. Run `dibbla --version` to see which one.
- Flags: `--user` installs into `$HOME` for machine-wide coverage instead of the current directory; `--no-agents` skips `AGENTS.md` and `GEMINI.md` (Claude Code only); `--force` overwrites skill files that have been edited locally. Unknown files inside `.claude/skills/<id>/` are always preserved.
- The AGENTS.md / GEMINI.md pointer block is marker-delimited (`<!-- >>> dibbla skill >>> -->` … `<!-- <<< dibbla skill <<< -->`) so existing AGENTS.md content outside the markers is preserved byte-for-byte across reruns.
- Re-running is idempotent — if nothing changed, nothing is rewritten (no mtime bump). Use `dibbla skills list` to see what skills the current CLI ships.

## Additional resources

- **Platform compatibility:** see [platform.md](platform.md) for the Dockerfile contract, port-matching, runtime environment, managed Postgres TLS handling, secrets/env-var injection, the auth-header contract (`X-User-*` headers and Google OAuth scope brokering), the upload boundary, the multi-service runtime contract (§ 8.5), and the pre-deploy compatibility checklist. Read this when working in a Dibbla-connected project on Dockerfile, `.dibblaignore`, auth integration, or deploy-readiness questions.
- **Multi-service manifest schema:** see [manifest.md](manifest.md) for the full `dibbla.yaml` schema — services, jobs, env-aware fields, profiles, service discovery (`DIBBLA_SVC_*`), `expose_to`/NetworkPolicy, volumes, init containers, healthchecks, multiple public services, custom domains, cron, build-time secrets, quotas, error codes, and a worked end-to-end example. Read this whenever the user is authoring or reviewing a manifest, or asking a "how do I run X alongside Y in one deploy" question.
- **Workflows:** see [workflows.md](workflows.md) for the complete workflow model — slim YAML format, the three node types and the roles `function` plays (agent / tool / script / data fetcher), the agent+tool wiring pattern, all 13 validator errors with fixes, edges and data flow, the functions registry, the three idiomatic authoring loops, revision semantics, HTTP execution, the canonical workflow shapes, the pre-flight checklist, and footguns. Read this whenever the user asks anything that touches `dibbla wf`/`nodes`/`edges`/`inputs`/`tools`/`revisions`/`functions`.
- **Go SDK:** see [sdk-go.md](sdk-go.md) for the Dibbla Go SDK — `sdk.New` server bootstrap and options, `SimpleFunction` vs advanced `Function[In, Out]`, the `JobHandler` interface and `server.RegisterJob` (the old `JobHost` is gone), the `Logger` task/progress API, OAuth-on-behalf-of-user via `gs.OAuth`, TLS auto-detection, the `internal/` import footgun, and end-to-end deploy. Read this whenever the user is implementing a Dibbla function or job in Go.
- **Full command and flag reference:** see [reference.md](reference.md) for usage, arguments, and all flags.
- **Usage examples:** see [examples.md](examples.md) for copy-paste examples and scripting patterns.
- **Pre-deploy guardrails:** see [guardrails.md](guardrails.md) for the mandatory pre-deploy security checklist (Checks 1–4 always; Check 5 for URL-fetched task files; Check 6 for multi-service manifests).

When suggesting or generating `dibbla` commands, use the reference for exact syntax and the examples for typical workflows. For "is my app ready for Dibbla?" questions, start in `platform.md`. For "build / iterate / debug a workflow" questions, start in `workflows.md`. For "implement a Go function/job for the platform" questions, start in `sdk-go.md`.
