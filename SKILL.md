# Dibbla CLI Skill

You are an expert in using the `dibbla` command-line tool.

## Installation

The `dibbla` CLI can be installed via Homebrew (on macOS or Linux), `curl` (macOS/Linux), PowerShell (Windows), or by using `go install`. For detailed, up-to-date installation instructions, refer to the project's `README.md` file.

## Tool Description

The `dibbla` CLI is used to scaffold new projects and manage applications, databases, and secrets on the Dibbla platform.

## Authentication

Most commands that interact with the Dibbla platform require an API token.

- **Local use:** Run `dibbla login` to store the token securely in the OS credential store (macOS Keychain, Windows Credential Manager, etc.). Use `dibbla login [api_url]` to target a different API (e.g. `dibbla login api.dibbla.net`). Use `dibbla logout` to remove stored credentials.
- **CI:** Set `DIBBLA_API_TOKEN` (and optionally `DIBBLA_API_URL`); the CLI uses env vars in CI and does not read the keychain.
- **Fallback:** The token can also be provided via the `DIBBLA_API_TOKEN` environment variable or a `.env` file.

If the token is missing, the tool will prompt the user to run `dibbla login` or set `DIBBLA_API_TOKEN`. Get your token at `https://app.dibbla.com/api-keys`.

## Commands

Here is a breakdown of the available commands and their usage:

### `login`

Store your API token securely in the OS credential store. The token is validated against the API before storage.

-   **Usage:** `dibbla login [api_url]`
-   **Arguments:**
    -   `api_url` (optional): API base host or URL (e.g. `api.dibbla.net` or `https://api.dibbla.net`). Default: `https://api.dibbla.com`.
-   **Flags:**
    -   `--api-key`: API token. If omitted, the user is prompted to enter it.
-   **Example:** `dibbla login` — `dibbla login --api-key ak_xxx` — `dibbla login api.dibbla.net`

### `logout`

Remove the API token and optional API URL stored by `dibbla login` from the OS credential store.

-   **Usage:** `dibbla logout`
-   **Example:** `dibbla logout`

### `create`

The `create` command scaffolds new Dibbla projects.

#### `create go-worker`

This command creates a new Go worker project from a template.

-   **Usage:** `dibbla create go-worker [name]`
-   **Arguments:**
    -   `name` (optional): The name of the project. If not provided, the tool will prompt for it.
-   **Workflow:**
    1.  The tool checks if Go is installed.
    2.  It asks for the project name if not provided.
    3.  It confirms the creation path.
    4.  It interactively prompts for the following information:
        -   **Hosting type:** Dibbla Cloud or Self-hosted.
        -   **gRPC address:** If self-hosted.
        -   **TLS:** If self-hosted.
        -   **API Token:** The `DIBBLA_API_TOKEN`.
        -   **Frontend:** Whether to include a starter frontend project.
    5.  It creates the project structure.
-   **Example:** `dibbla create go-worker my-awesome-worker`

### `apps`

The `apps` command manages deployed applications.

#### `apps list`

Lists all deployed applications.

-   **Usage:** `dibbla apps list`
-   **Output:** A table with application alias, URL, status, and last deployment date.
-   **Example:** `dibbla apps list`

#### `apps update`

Updates an existing deployment (env vars, replicas, cpu, memory, port).

-   **Usage:** `dibbla apps update <alias>`
-   **Arguments:**
    -   `alias` (required): The deployment alias to update.
-   **Flags:**
    -   `--env`, `-e`: Set env var KEY=value (repeatable, Docker-style).
    -   `--replicas`: Desired number of replicas.
    -   `--cpu`: CPU request/limit (e.g. 500m, 1).
    -   `--memory`: Memory request/limit (e.g. 256Mi, 512Mi).
    -   `--port`: Container port (1-65535).
    -   `--favicon`: Favicon URL (use `""` to clear).
-   At least one of `--env`, `--replicas`, `--cpu`, `--memory`, `--port`, or `--favicon` is required.
-   **Example:** `dibbla apps update myapp -e NODE_ENV=production` — **Replicas:** `dibbla apps update myapp --replicas 3` — **Resources:** `dibbla apps update myapp --cpu 500m --memory 512Mi --port 3000`

#### `apps delete`

Deletes a deployed application.

-   **Usage:** `dibbla apps delete <alias>`
-   **Arguments:**
    -   `alias` (required): The alias of the application to delete.
-   **Flags:**
    -   `--yes`, `-y`: Skip the confirmation prompt.
-   **Example:** `dibbla apps delete my-old-app -y`

#### `apps restart`

Trigger a K8s rolling restart of one service in a multi-service deployment. Idempotent — calling twice in a row produces two pod rollouts. For single-service / legacy deployments, the conventional service name is `app`.

-   **Usage:** `dibbla apps restart <alias> --service <name>`
-   **Arguments:**
    -   `alias` (required): The deployment alias.
-   **Flags:**
    -   `-s`, `--service <name>` (required): Service to restart. Regex `^[a-z][a-z0-9-]{0,29}$`.
    -   `-q`, `--quiet`: Print only the alias on success (script-friendly).
    -   `--json`: Print the JSON response body.
-   **Errors:** 404 (service not found) prints a hint to run `dibbla apps list`. Bad service-name regex is caught locally before the HTTP call.
-   **Example:** `dibbla apps restart myapp --service worker` — **Quiet:** `dibbla apps restart myapp -s web -q`

### `logs`

Print logs for a deployed app, sourced from the platform's Loki backend. By default returns the last 15 minutes of logs and exits.

-   **Usage:** `dibbla logs <app>`
-   **Arguments:**
    -   `app` (required): The alias of the app whose logs to fetch.
-   **Flags:**
    -   `--since <duration>`: Window to fetch (Go duration; default `15m`, server cap `24h`).
    -   `-f`, `--follow`: Stream new log lines as they arrive (after the `--since` backfill, if any).
    -   `-n`, `--tail <N>`: Show only the last N lines instead of the `--since` window.
    -   `--grep <regex>`: Server-side regex line filter (LogQL `|~`).
    -   `--limit <N>`: Cap lines fetched in range mode (server caps the value).
    -   `--json`: Emit raw NDJSON (one Loki entry per line) instead of the human format.
    -   `--no-color`: Disable color in the human format.
    -   `-s`, `--service <name>`: Filter to a single service in a multi-service deployment (forwarded as `?service=`).
    -   `--pod-stream`: Stream pod logs via the K8s API instead of Loki (requires `--service`). Output is text/plain with `[<pod>] ` line prefixes — useful when Loki isn't configured on the platform.
-   **Authorization:** Returns 404 for apps outside your organization, or for `--pod-stream` when no pods match the service. Returns 503 if Loki isn't configured (`LOKI_URL` unset) or `--pod-stream` is used and Kubernetes isn't configured.
-   **Examples:**
    -   `dibbla logs expense-reporter`
    -   `dibbla logs expense-reporter --since 24h`
    -   `dibbla logs expense-reporter --since 10m -f`
    -   `dibbla logs expense-reporter -n 200`
    -   `dibbla logs expense-reporter --grep "timeout"`
    -   `dibbla logs expense-reporter --json | jq .`

### `init`

One-shot machine setup. Runs `dibbla update`, `dibbla login`, and `dibbla skills install dibbla` in order, each as its own subprocess of the running dibbla binary. Designed for "I just installed dibbla, set me up" — and is safe to re-run (idempotent: each step detects "already done").

-   **Usage:** `dibbla init`
-   **Flags:**
    -   `-y`, `--yes`: Skip prompts where possible (forwarded to `update`).
    -   `--skip-update`: Don't run the update step.
    -   `--skip-skill`: Don't install the dibbla skill.
    -   `--user`: Install the skill into `$HOME` instead of the current project (forwarded to `skills install`).
    -   `--re-login`: Run `login` even if a token is already configured.
    -   `--api-url <url>`: API endpoint forwarded to `login` (e.g. `https://api.dibbla.net`).
-   **Failure policy:** `update` and `skill install` failures warn and continue; `login` failure stops init (everything else needs auth).
-   **Token handling:** Pass an existing `DIBBLA_API_TOKEN` env var to skip the login prompt. **Do not pass tokens via flag** — they appear in `ps` output.
-   **Examples:**
    -   `dibbla init` — set up everything in this project.
    -   `dibbla init --user` — install skill machine-wide instead of per-project.
    -   `dibbla init --skip-update --skip-skill` — just log in (e.g. fresh install, dont care about the skill yet).

### `update`

Update dibbla itself to the latest released version. The command detects how dibbla was installed and either prints the right command for your package manager (Homebrew, apt, rpm, scoop, choco) or self-replaces the binary for installs from the install.dibbla.com script.

-   **Usage:** `dibbla update`
-   **Flags:**
    -   `--check`: Only report whether a newer version is available; exits non-zero if drift exists. Safe to wrap in CI scripts.
    -   `--version <tag>`: Install a specific release tag (e.g. `v1.2.3`) instead of latest. Useful for rolling back.
    -   `--force`: Reinstall even if already on the requested version.
    -   `-y`, `--yes`: Skip the confirmation prompt.
-   **Notes:** Refuses to self-replace `dev` builds. For Homebrew / apt / rpm / scoop / choco installs, prints the upgrade command but does not run it (no implicit sudo). Always verifies the SHA-256 of the downloaded archive against `checksums.txt` from the same release before swapping.
-   **Examples:**
    -   `dibbla update`
    -   `dibbla update --check`
    -   `dibbla update --version v1.4.2 --yes`

### `db`

The `db` command manages managed databases on the Dibbla platform.

#### `db list`

Lists all available databases.

-   **Usage:** `dibbla db list [--quiet | -q]`
-   **Flags:**
    -   `--quiet`, `-q`: Only print database names, one per line (for scripting; no "Retrieving...", no "Found N...").
-   **Example:** `dibbla db list` — **Quiet (scripting):** `dibbla db list -q`

#### `db create`

Creates a new database. Automatically creates a `DATABASE_URL` secret with the connection string.

-   **Usage:** `dibbla db create [name]`
-   **Arguments:**
    -   `name` (optional): The name for the new database.
-   **Flags:**
    -   `--name <name>`: Alternative way to provide the database name.
    -   `--deployment <alias>`: Scope the database and its `DATABASE_URL` secret to a specific deployment. If omitted, the secret is global (available to all deployments).
-   **Example:** `dibbla db create --name my-new-db` — **Scoped:** `dibbla db create mydb --deployment myapp`

#### `db delete`

Deletes a database.

-   **Usage:** `dibbla db delete <name> [--yes] [--quiet]`
-   **Arguments:**
    -   `name` (required): The name of the database to delete.
-   **Flags:**
    -   `--yes`, `-y`: Skip the confirmation prompt.
    -   `--quiet`, `-q`: Suppress progress and success output (errors only; for scripting).
-   **Example:** `dibbla db delete my-old-db --yes` — **Quiet (scripting):** `dibbla db delete my-old-db --yes -q`

#### `db dump`

Downloads a dump of a database.

-   **Usage:** `dibbla db dump <name>`
-   **Arguments:**
    -   `name` (required): The name of the database to dump.
-   **Flags:**
    -   `--output <file>`, `-o <file>`: The path to save the dump file to. Defaults to `<name>.dump`.
-   **Example:** `dibbla db dump my-production-db -o backup.dump`

#### `db restore`

Restores a database from a dump file.

-   **Usage:** `dibbla db restore <name>`
-   **Arguments:**
    -   `name` (required): The name of the database to restore.
-   **Flags:**
    -   `--file <path>`, `-f <path>` (required): The path to the dump file to restore from.
-   **Example:** `dibbla db restore my-staging-db --file backup.dump`

#### `db connect`

Prints a psql-compatible connection string for connecting to a database via the Dibbla database proxy. Host and `sslmode` are derived from `DIBBLA_API_URL`: `api.dibbla.com` → `db.dibbla.com` with `sslmode=require`; `api.dibbla.net` (internal) → `db.dibbla.net` with `sslmode=disable`; `localhost` / `127.0.0.1` also use `sslmode=disable`. Override with `DIBBLA_DB_HOST` / `DIBBLA_DB_PORT` / `DIBBLA_DB_SSLMODE`. Uses your current API token as the password.

-   **Usage:** `dibbla db connect <name> [--quiet | -q]`
-   **Arguments:**
    -   `name` (required): The name of the database to connect to.
-   **Flags:**
    -   `--quiet`, `-q`: Only print the connection string (no labels or tips; for scripting).
-   **Example:** `dibbla db connect myapp` — **Quick connect:** `psql $(dibbla db connect myapp -q)` — **Export:** `export DATABASE_URL=$(dibbla db connect myapp -q)`

### `secrets`

The `secrets` command manages secrets on the Dibbla platform. Secrets have **three** scopes:

-   **Global** (no `--deployment`) — visible to every deployment in the org.
-   **Deployment-wide** (`--deployment <alias>` / `-d <alias>`, no `--service`) — visible to every service in the deployment.
-   **Per-service** (`-d <alias> --service <name>` / `-s <name>`) — visible only to the named service container.

Precedence inside a service container at runtime (highest wins): per-service > deployment-wide > global. `--service` requires `--deployment`. Service names follow `^[a-z][a-z0-9-]{0,29}$`.

#### `secrets list`

Lists secrets (global or for one deployment).

-   **Usage:** `dibbla secrets list [-d <alias>] [-s <service>]`
-   **Flags:**
    -   `--deployment`, `-d`: List only secrets for this deployment. Omit for global secrets.
    -   `--service`, `-s`: Scope to a single service in the deployment (requires `-d`).
-   **Output:** A table with name, deployment (or "(global)"), service (or "(all)") and updated-at.
-   **Example:** `dibbla secrets list` — **Per-app:** `dibbla secrets list -d myapp` — **Per-service:** `dibbla secrets list -d myapp -s web`

#### `secrets set`

Creates or updates a secret.

-   **Usage:** `dibbla secrets set <name> [value] [-d <alias>] [-s <service>]`
-   **Arguments:**
    -   `name` (required): The secret name (e.g. `API_KEY`).
    -   `value` (optional): The secret value. If omitted, the value is read from stdin.
-   **Flags:**
    -   `--deployment`, `-d`: Attach the secret to this deployment. Omit for a global secret.
    -   `--service`, `-s`: Scope to a single service (requires `-d`).
-   **Example:** `dibbla secrets set API_KEY "my-secret"` — **Per-app:** `dibbla secrets set API_KEY "x" -d myapp` — **Per-service:** `dibbla secrets set NPM_TOKEN xxx -d myapp -s web`

#### `secrets get`

Prints a secret's value (suitable for piping).

-   **Usage:** `dibbla secrets get <name> [-d <alias>] [-s <service>]`
-   **Arguments:**
    -   `name` (required): The secret name.
-   **Flags:**
    -   `--deployment`, `-d`: For a deployment-scoped secret.
    -   `--service`, `-s`: For a per-service secret (requires `-d`).
-   **Example:** `dibbla secrets get API_KEY` — **Per-app:** `dibbla secrets get API_KEY -d myapp` — **Per-service:** `dibbla secrets get NPM_TOKEN -d myapp -s web`

#### `secrets delete`

Deletes a secret.

-   **Usage:** `dibbla secrets delete <name> [-d <alias>] [-s <service>] [--yes | -y]`
-   **Arguments:**
    -   `name` (required): The secret name to delete.
-   **Flags:**
    -   `--deployment`, `-d`: For a deployment-scoped secret.
    -   `--service`, `-s`: For a per-service secret (requires `-d`).
    -   `--yes`, `-y`: Skip the confirmation prompt.
-   **Example:** `dibbla secrets delete API_KEY --yes` — **Per-app:** `dibbla secrets delete API_KEY -d myapp -y` — **Per-service:** `dibbla secrets delete NPM_TOKEN -d myapp -s web -y`

### `deploy`

The `deploy` command deploys a project to the Dibbla platform. **Detection is by file:** if `dibbla.yaml` (or `dibbla.yml`) is present at the deploy root, the multi-service path runs (manifest parse + resolve + parallel build + atomic apply with rollback). Otherwise the legacy single-`Dockerfile` path runs unchanged.

-   **Usage:** `dibbla deploy [path]`
-   **Arguments:**
    -   `path` (optional): The path to the project to deploy. Defaults to the current directory.
-   **Flags:**
    -   `--alias`, `-a`: Custom alias name (default: directory name).
    -   `--message`, `-m`: **Required for agents.** Deploy message used as the VCS commit subject in the app's Dibbla-managed git history (and on the GitHub mirror, if configured). Treat it like a git commit subject: present-tense imperative, under ~72 chars, covering what changed and why. Max 500 chars. Examples: `-m "fix: handle null org in /api/me"`, `-m "feat: add nightly db backup workflow"`, `-m "chore: bump node to 20.14"`. For retries/mechanical redeploys still say so: `-m "redeploy: retry after CF 524"`. Never omit `-m` — a blank deploy history is a bug, not a default.
    -   `--force`, `-f`: Force a redeployment if an application with the same alias already exists (causes downtime).
    -   `--update`, `-u`: Rolling update of existing deployment (zero downtime). Mutually exclusive with `--force`.
    -   `--env`, `-e`: Set environment variable KEY=value (repeatable, Docker-style).
    -   `--cpu <value>`: CPU request (e.g. `500m`). **Ignored under multi-service** — set CPU per service in `dibbla.yaml`.
    -   `--memory <value>`: Memory request (e.g. `512Mi`). **Ignored under multi-service.**
    -   `--port <value>`: Container port (e.g. `3000`). **Ignored under multi-service.**
    -   `--favicon <url>`: Favicon URL (e.g. `https://example.com/favicon.ico`).
    -   `--target-env <name>`: Manifest env block to resolve (defaults to `prod` server-side). Multi-service only.
    -   `--profile <name>`: Activate a manifest profile (repeatable). Multi-service only.
    -   `--no-public`: Allow a deploy with no `public: true` service (worker- or cron-only deploys). Multi-service only.
-   **Example:** `dibbla deploy ./my-app -m "feat: initial deploy" --force` — **Rolling update:** `dibbla deploy -m "fix: resolve 500 on /search" --update` — **Multi-service:** `dibbla deploy --alias myapp --target-env prod -m "feat: ship multi-service" --profile observability`

### Multi-service deployments (`dibbla.yaml`)

A `dibbla.yaml` at the deploy root bundles multiple services (e.g. `web + worker + redis`) into one alias, applied atomically. Min example:

```yaml
version: 1
services:
  web:
    build: ./web
    port: 3000
    public: true
    environment:
      REDIS_URL: ${DIBBLA_SVC_REDIS_URL}
  worker:
    build: ./worker
  redis:
    image: redis:7
    port: 6379
```

Validate locally before commit:

```bash
dibbla manifest validate
dibbla preview --target-env prod
```

**Multiple public URLs.** Two services with `public: true` get one URL each: the lex-first ("primary") at `https://<alias>.dibbla.com`; subsequent ones at `https://<alias>-<service>.dibbla.com`. Per-service auth (`auth.require_login`, `auth.access_policy`, `auth.google_scopes`) is env-aware so the canonical pattern works:

```yaml
services:
  web:
    public: true                         # always open
  pgadmin:
    image: dpage/pgadmin4:latest
    port: 80
    public:
      default: false
      dev: true
      prod: true
    auth:
      require_login: { dev: false, prod: true }
      access_policy: { prod: invite_only }
```

**Shell variable substitution.** `${VAR}` and `${VAR:-default}` in `dibbla.yaml` are resolved from your shell env at `dibbla deploy` time (compose-style). `DIBBLA_*` is reserved for the server's discovery contract — those pass through to the server unchanged regardless of your shell.

**Stateful services + TCP routes (F19).** A service with `stateful: true` renders as a Kubernetes StatefulSet plus a headless Service so each pod gets stable per-replica DNS, and each replica owns its own PVC via `volumeClaimTemplates`. Combined with a per-service `routes:` list this lets you expose databases and message brokers over real TLS to your laptop:

```yaml
version: 1
services:
  db:
    image: mongo:7
    port: 27017
    stateful: true                  # → StatefulSet + headless Service
    volumes:
      - path: /data/db
        size: 10Gi
    routes:
      - type: tcp                   # raw TCP route at the edge
        port: 27017
        tls: edge                   # platform-managed wildcard cert
        hostname: my-mongo          # → my-mongo.<base-domain>:443
```

After deploy, connect from your laptop with the connection string the CLI prints (e.g. `mongosh "mongodb://my-mongo.<base-domain>:443/?tls=true"`).

Two limits to know:
1. **TLS-on-connect protocols only** in v1: MongoDB, Redis-with-TLS, AMQPS, NATS-with-TLS, Kafka-with-TLS. Postgres and MySQL use STARTTLS-style upgrades that don't carry SNI in the first packet, so they are deferred.
2. **`replicas > 1` on a stateful service yields N independent pods**, each with its own PVC and its own data. The platform does **not** bootstrap clustering protocols (Mongo replica set, Redis sentinel, etc.). Use `replicas: 1` unless you're wiring clustering yourself with init containers + your own config. Managed-cluster recipes are a follow-up.

`dibbla apps delete` is destructive on stateful services: it deletes the StatefulSet, the IngressRouteTCP CRDs, the DNS record, **and the PVCs** with all their data. There is no `--preserve-volumes` flag in v1 — back up before deleting.

For the full schema (env-aware fields, profiles, init containers, healthchecks, custom domains, cron jobs, build secrets, multiple public services + per-service auth, shell-var substitution, quotas, the runtime contract for service discovery + NetworkPolicy, and stateful services + TCP routes), see `.claude/skills/dibbla/manifest.md` (stateful + routes are § 10.5; runtime model is `platform.md § 8.5` for multi-service basics and `§ 8.6` for the stateful + TCP-routes runtime).

### `manifest`

Local-only schema validation for `dibbla.yaml`. No server roundtrip.

#### `manifest validate`

-   **Usage:** `dibbla manifest validate [path]`
-   **Arguments:**
    -   `path` (optional): A directory (looks for `dibbla.yaml` / `dibbla.yml`) or a manifest file directly. Defaults to `.`.
-   **Flags:**
    -   `--target-env <name>`: Recorded in the report (informational; resolution runs server-side).
    -   `--profile <name>`: Repeatable, informational.
    -   `--no-public`: Informational; the local check accepts both.
    -   `--json`: Emit a structured JSON report.
-   **Coverage:** schema version, service-name regex + reserved names, build/image XOR, image-must-have-tag, port range, ambiguous yaml/yml. Env-aware resolution and quota run server-side — use `dibbla preview` for those.
-   **Example:** `dibbla manifest validate` — **JSON for CI:** `dibbla manifest validate --json | jq -e '.valid'`

### `preview`

Server-authoritative dry run. Uploads the archive and lets the server resolve the manifest + run quota — no build, no apply.

-   **Usage:** `dibbla preview [path]`
-   **Arguments:**
    -   `path` (optional): Directory to preview; default `.`.
-   **Flags:**
    -   `-a`, `--alias <name>`: Override directory-name alias.
    -   `--target-env <name>`: Manifest env (defaults to `prod` server-side).
    -   `--profile <name>`: Repeatable manifest profile.
    -   `--no-public`: Allow worker- or cron-only deploys.
    -   `--port <N>`: Forwarded as the `port` field; used by the no-manifest synthesizer.
    -   `--json`: Emit raw `PreviewResponse` JSON.
-   **Output:** Active services + replica counts, public service name, env-aware-resolved values, skipped services with reasons, quota-check result.
-   **Example:** `dibbla preview --target-env staging` — **JSON:** `dibbla preview --json | jq '.active_services'`

### `admin`

Platform-admin commands gated by `DIBBLA_ADMIN_TOKEN`. The user's normal API token is **not** used.

#### `admin reconcile`

Force one synchronous orphan-resource sweep on the deploy-api instance.

-   **Usage:** `DIBBLA_ADMIN_TOKEN=<tok> dibbla admin reconcile`
-   **Flags:**
    -   `--json`: Emit the raw JSON sweep result.
-   **Auth:** Reads `DIBBLA_ADMIN_TOKEN` from env. `DIBBLA_API_URL` (or default) selects the deploy-api instance.
-   **Output:** `deployments`, `services`, `ingresses` — counts plus the names of the swept K8s objects.
-   **Errors:** Missing token → exit 1 with prompt. 401 → "unauthorized; check DIBBLA_ADMIN_TOKEN". 404 → "admin endpoints not enabled". 503 → "reconciler not configured".
-   **Example:** `DIBBLA_ADMIN_TOKEN=$ADMIN_TOKEN dibbla admin reconcile`

### `skills`

The `skills` command installs the skill files that teach AI coding agents (Claude Code, Cursor, Gemini CLI, Opencode, Codex, Copilot, Windsurf, Aider, etc.) how to use the Dibbla CLI. The skill content is embedded in the binary via `//go:embed`, so no network is required and the skill version is always locked to the CLI version.

#### `skills list`

Lists skills bundled with this `dibbla` version.

-   **Usage:** `dibbla skills list`
-   **Output:** A table with skill id and description (currently just `dibbla`).
-   **Example:** `dibbla skills list`

#### `skills install`

Writes the skill files into the current project (or `$HOME` with `--user`) plus `AGENTS.md` / `GEMINI.md` pointer blocks so other coding agents pick it up.

-   **Usage:** `dibbla skills install <id>`
-   **Arguments:**
    -   `id` (required): Skill id from `dibbla skills list` (currently only `dibbla`).
-   **Flags:**
    -   `--user`: Install into `$HOME` instead of the current working directory (machine-wide coverage).
    -   `--force`: Overwrite skill files that have been edited locally. Only the embedded filenames are touched; user-added files in `.claude/skills/<id>/` are always preserved.
    -   `--no-agents`: Skip writing `AGENTS.md` and `GEMINI.md` at the target root (Claude Code only).
-   **Writes:**
    -   `<root>/.claude/skills/<id>/{SKILL.md,examples.md,guardrails.md,platform.md,reference.md}` — Claude Code's native skill path.
    -   `<root>/AGENTS.md` — marker-delimited pointer block (2026 open standard; read by Cursor, Opencode, Codex, Copilot, Windsurf, Aider, Zed, Warp, RooCode).
    -   `<root>/GEMINI.md` — same block, for Gemini CLI's default context filename.
-   **Idempotent:** Re-running is safe. Identical bytes are a no-op (no mtime bump). CRLF vs LF line endings in `AGENTS.md` / `GEMINI.md` are preserved.
-   **Example:** `dibbla skills install dibbla` — **Machine-wide:** `dibbla skills install dibbla --user` — **Claude Code only:** `dibbla skills install dibbla --no-agents`

### `clone`

Clones the Dibbla-managed git repo for a deployed app. Each `dibbla deploy` writes a commit to a platform-managed bare repo; `clone` lets you fetch that history locally so you (or a coding agent) can inspect exactly what was deployed, diff between deploys, or fork the code back to GitHub/GitLab.

-   **Usage:** `dibbla clone <app>` or `dibbla clone <org>/<app>`
-   **Arguments:**
    -   `app` (required): The app alias. The `<org>/` prefix is accepted but optional — the org is derived from your token.
-   **Flags:**
    -   `--ref <sha>`: Commit SHA to check out after clone (default: latest on `main`).
    -   `--into <dir>`: Destination directory (default: `./<app>`).
-   **Authentication:** Reuses the token from `dibbla login` / `DIBBLA_API_TOKEN` — no separate clone credential. Internally the CLI shells out to `git -c http.extraHeader="Authorization: Bearer <token>" clone ...`, so the token never lands in `~/.git-credentials` or `.git/config`.
-   **Push is rejected.** The platform rejects `git push` by design — these repos are append-only from deploys. If you want to share changes, add a GitHub/GitLab remote locally and push there.
-   **Example:** `dibbla clone my-app` — **Pin commit:** `dibbla clone my-app --ref abc1234` — **Custom dir:** `dibbla clone my-app --into ./checkout`

### Version control API (for scripting / agents)

Three read-only endpoints expose the same data surfaced by the console's Version Control card. Authenticate with `Authorization: Bearer $DIBBLA_API_TOKEN`.

-   `GET /api/deploy/deployments/<app>/vcs/info` — returns default branch, latest commit, clone URL, CLI command, and `running_sha` when the latest commit matches the Running deployment.
-   `GET /api/deploy/deployments/<app>/vcs/commits?limit=<n>&before=<sha>` — paginated commit list (newest first). The `deploy_id` field (from the `Deploy-Id:` trailer) correlates commits with deployments.
-   `GET /api/deploy/deployments/<app>/vcs/commits/<sha>` — commit detail with the file list at that tree.

Prefer `dibbla clone` over shelling out to `git clone` by hand — it resolves the canonical clone URL via `/vcs/info`, so it keeps working if the git host moves.

## Pre-deploy guardrails

Before calling `dibbla deploy`, you MUST review the application code and present findings to the user. **Never deploy autonomously** — always wait for explicit user confirmation.

Run these four checks and report each as BLOCKER or WARNING:

1. **Security (OWASP Top 10)** — Hardcoded secrets, SQL/command injection, XSS, `.env` files in deploy dir are **BLOCKERs**. Missing CSRF, input validation, security headers are warnings.
2. **Database usage** — N+1 queries (query inside a loop) are **BLOCKERs**. Unbounded SELECTs, missing connection pooling, missing error handling are warnings.
3. **REST/API calls** — Outbound HTTP calls without timeouts are **BLOCKERs**. Missing retry/backoff, excessive polling (<5s), hardcoded URLs are warnings.
4. **External write safety** — Unbounded write loops to external systems are **BLOCKERs**. Missing rate limiting, missing idempotency, fire-and-forget writes are warnings.

Present a checklist report to the user. If any BLOCKER is found, offer to fix it and wait for confirmation — do NOT deploy. If only warnings, ask the user whether to fix or proceed. If all clear, ask "Ready to deploy?" and wait for confirmation.

## General Behavior

- The tool is interactive and will prompt for missing information.
- Always provide clear and direct commands.
- When scripting, use flags like `--yes` to avoid interactive prompts.
- Pay attention to the output for success messages, error details, and status information.
- The CLI performs update checks in the background for interactive TTY sessions. If the network is unavailable, failed checks are cached for 24 hours to avoid repeated request timeouts on every command invocation.
