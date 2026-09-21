# Dibbla CLI Skill

You are an expert in using the `dibbla` command-line tool.

**This skill is the CLI. The Dibbla connector has its own.** The MCP connector
at `https://mcp.dibbla.com/platform` — used by claude.ai, Claude Cowork, Claude
Code, Codex CLI and ChatGPT — is covered by the `dibbla-platform` skill, which
the connector itself serves and which is versioned with the platform capability
contract. That skill is written for an agent that has tool calls and may have no
shell, so it names no `dibbla` commands. Install this skill for a surface with a
shell and a signed-in CLI; install that one for a surface whose only access is
the `platform_*` tools. Handing an agent the wrong one gives it thousands of
lines of instructions it cannot act on.

## Installation

The `dibbla` CLI can be installed via Homebrew (on macOS or Linux), `curl` (macOS/Linux), PowerShell (Windows), or by using `go install`. For detailed, up-to-date installation instructions, refer to the project's `README.md` file.

## Tool Description

The `dibbla` CLI is used to scaffold new projects and manage applications, databases, and secrets on the Dibbla platform.

## Authentication

Most commands that interact with the Dibbla platform require an API token.

- **Local use:** Run `dibbla login` to store the token securely in the OS credential store (macOS Keychain, Windows Credential Manager, etc.). Use `dibbla login [api_url]` to target a different API (e.g. `dibbla login api.your-domain.com`). Use `dibbla logout` to remove stored credentials.
- **Several servers at once:** A login is stored as a named **context** — an API URL, its token, and the organization pinned on that server. Logging in to a second server therefore *adds* a context instead of replacing the first, and `dibbla context use <name>` switches between them. See `dibbla context`.
- **CI:** Set `DIBBLA_API_TOKEN` (and optionally `DIBBLA_API_URL`); the CLI uses env vars in CI and reads neither the keychain nor the context list.
- **Fallback:** The token can also be provided via the `DIBBLA_API_TOKEN` environment variable or a `.env` file.

If the token is missing, the tool will prompt the user to run `dibbla login` or set `DIBBLA_API_TOKEN`. Get your token at `https://app.dibbla.com/api-keys` — or, for a self-hosted or customer instance, at that instance's own portal (`app.<its-domain>/api-keys`). A token minted on the wrong instance will not work there.

## Commands

Here is a breakdown of the available commands and their usage:

### `login`

Store your API token securely as a named context. The token is validated against the API before storage.

-   **Usage:** `dibbla login [api_url]`
-   **Arguments:**
    -   `api_url` (optional): API base host or URL (e.g. `api.your-domain.com` or `https://api.your-domain.com`). If omitted, the API URL of the context you are currently on is used, and `https://api.dibbla.com` only when there is none — so a bare `dibbla login` while working against a customer instance re-authenticates there rather than silently re-targeting production.
-   **Flags:**
    -   `--api-key`: API token. If omitted, the user is prompted to enter it.
    -   `--context <name>`: name the context to create or refresh. Without it, the context is keyed on the API URL — the same URL refreshes, a new URL creates a new context.
    -   `--no-switch`: store the login without making it the context in use.
-   **Example:** `dibbla login` — `dibbla login --api-key ak_xxx` — `dibbla login --context lab --api-url https://api.your-domain.com --api-key ak_yyy`

### `logout`

Log out of the context in use: remove its token from the OS credential store and from its credentials file, and remove the context. Other contexts are untouched, so logging out of production does not log you out of a customer instance. Also clears the organization selected with `dibbla org use` for whatever is removed.

-   **Usage:** `dibbla logout [--context <name>] [--all]`
-   **Flags:**
    -   `--context <name>`: log out of that context instead of the one in use.
    -   `--all`: log out of every context and delete the context list.
-   **Example:** `dibbla logout` — `dibbla logout --context lab` — `dibbla logout --all`

### `context`

Show and switch the API server the CLI talks to. A context is a named login target — an API URL, its token, and the organization pinned on that server — so you can stay logged in to production, a customer instance and a development cluster at the same time.

The list is a plain, editable file at `~/.config/dibbla/config.yaml` and holds no secrets; tokens stay in the OS keyring under per-context keys, falling back to a per-context credentials file on hosts without a keyring.

-   **Usage:** `dibbla context list` — `dibbla context use <name>` — `dibbla context current` — `dibbla context rename <old> <new>` — `dibbla context rm <name>`
-   **Flags:**
    -   `--json` (on `list`): machine-readable output; each row carries `current` and `logged_in`.
    -   `--force` (on `rm`): required to remove the context in use.
    -   `--context <name>`: global flag available on *every* command, applying to that one invocation.
-   **Precedence:** `DIBBLA_API_TOKEN`/`DIBBLA_API_URL` (shell or `.env`) > `--context` > `DIBBLA_CONTEXT` > the selected context > `https://api.dibbla.com`.
-   **Upgrading:** an existing login is imported into a context automatically on first run, keeping its organization pin. No re-login is needed and there is nothing to do.
-   **Not context-aware:** `dibbla update` and the installer are machine-wide by design.
-   **Example:** `dibbla context list` — `dibbla apps list --context lab` — `dibbla context use lab`

### `org`

Show and switch the organization the CLI acts as. On a given server your API token belongs to your user rather than to one organization, so switching needs no new login — the selection travels with each request and the API verifies your membership before honoring it. With nothing selected, your account's default organization is used and no organization header is sent at all.

The selection is stored **on the active context**, and `org list` shows the organizations on **that context's server**, because an organization id only means anything on the server that issued it. Switching context switches organization with it.

-   **Usage:** `dibbla org list` — `dibbla org use <name|slug|id>` — `dibbla org clear`
-   **Flags:**
    -   `--json` (on `list`): machine-readable output; each entry carries `active`.
    -   `--org <id>`: global flag available on *every* command, applying to that one invocation.
-   **Precedence:** `--org` > `DIBBLA_ORG_ID` > the active context's pin > your account's default.
-   **Matching:** `use` takes a name, slug, or id, case-insensitively. A name shared by two organizations is reported as ambiguous rather than guessed at — pass the slug or id instead.
-   **Example:** `dibbla org use acme` — `dibbla --org <id> apps list` — `dibbla org clear`

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

#### `apps releases`

List the releases the platform still holds for an app: one immutable image per successful deploy (`dep_…` id), newest first, with digest, deploy time, author, and which one is running. A release marked `gone` was swept by registry retention and cannot be rolled back to; `+config` means the platform remembers its port/env/resources (what recreating a missing deployment needs).

-   **Usage:** `dibbla apps releases <alias>`
-   **Flags:** `--json`: print the raw API document.
-   **Errors:** 404 `NOT_FOUND` when the alias has neither a deployment nor any saved release in your organization.
-   **Example:** `dibbla apps releases myapp`

#### `apps rollback`

Switch a running app to an earlier release's image **without a build** — the way back when a deploy went wrong or the build service/registry is down. A rolling update; the app answers with the earlier version within about a minute. Env, resources and the login gate are inherited from the running app (like `deploy --update` with no flags); secrets are untouched. If the deployment is missing (a `--force` deploy removed it and the build failed), rollback recreates it from the release's image with the configuration the platform saved.

-   **Usage:** `dibbla apps rollback <alias> [--to <dep-id>]`
-   **Flags:**
    -   `--to <dep-id>`: Release to roll back to (from `apps releases`). Default: the previous release (newest one not running).
    -   `-y`, `--yes`: Skip the confirmation prompt (required in scripts/agents — without a terminal the command refuses with exit 5).
    -   `--json`: Print the JSON response body.
-   **Errors:** `RELEASE_GONE` (410) — the image was removed by retention; the message names the releases still available. `RELEASE_NOT_FOUND` (404) — not a release of this app, or no previous release. `RELEASE_CONFIG_UNKNOWN` / `ROLLBACK_UNSUPPORTED` (409) — the deployment is gone with no saved config, or the app is multi-service/stateful: `dibbla deploy` from source instead.
-   **Example:** `dibbla apps rollback myapp -y` — **Specific:** `dibbla apps rollback myapp --to dep_k3f9a -y`

#### `apps checks`

Inspect and run an app's **application checks** — the assertions in `dibbla-checks.yaml` that prove the running app still does what it is for. This is not a `healthcheck:` in `dibbla.yaml`: that is a kubelet probe about the container, and it cannot see a broken signup form. The alias is always positional; a single check id is always the `--check` flag.

-   **`apps checks list <alias> [--json]`** — the definitions in the promoted snapshot, each with its kind, schedule, classification and enabled state. `configured: false` (the org capability is on, the app ships no file) is exit **0** — "no checks" is an answer, not an error.
-   **`apps checks run <alias> [--check <id>] [--async|--follow] [--quiet|--json]`** — run now, the same execution path as the schedule. **The exit code is the product outcome:** `0` pass, `8` fail, `9` error, `10` indeterminate, `12` canceled, `13` skipped_concurrent. Transport failures keep the CLI-wide ladder (`3` auth, `4` not found, `5` validation, `6` conflict, `7` timeout, `1` unexpected). Gate CI on the code, never on output text — exit 8 is the check working, not the command failing. `--async` returns the execution id immediately; `--follow --json` is NDJSON with exactly one terminal `summary` line carrying `outcome` and `exit_code`.
-   **`apps checks history <alias> [--check <id>] [--since 24h] [--limit N] [--json]`** — past executions, newest first, each with an outcome, a stable machine code and a bounded, redacted summary. History survives disablement.
-   **`apps checks enable|disable <alias> [--yes]`** — start or stop the schedule. Requires owner/admin, and `enable` requires configured checks. **Shipping the file does not start anything**; this command does. `disable` keeps definitions and history readable.
-   **Errors:** the org capability being off is a `404` with `APPLICATION_CHECKS_DISABLED` (exit 4). If a user says "I added the file and nothing happens", check the org capability first and the per-app `enable` second.
-   **Example:** `dibbla apps checks run myapp --check home-page` — **CI:** `dibbla apps checks run myapp --quiet || exit $?`

#### `apps maintenance`

Inspect, enable and run an app's **maintenance agent** — an opt-in overnight look at logs, source and check history. It never deploys on its own. The alias is always positional.

-   **`apps maintenance status <alias> [--json]`** — effective settings and the latest run. Org capability off is `404` with `MAINTENANCE_AGENT_NOT_FOUND` (exit **4**), not a missing alias.
-   **`apps maintenance enable|disable <alias> [--yes]`** — per-app switch. Requires owner/admin. Shipping code does not start anything; this command does.
-   **`apps maintenance run <alias> [--async|--follow] [--quiet|--json] [--mode nightly|check-triage] [--check-run <id>] [--idempotency-key <key>]`** — start one run. **Product exits:** `0` found_nothing/proposed/budget_exhausted/skipped/cancelled, `11` finding_recorded. Transport keeps `1/3/4/5/6/7`. `--follow --json` is NDJSON with exactly one terminal `summary`. Reusing `--idempotency-key` replays the original execution.
-   **`apps maintenance runs <alias> [--limit N] [--json]`** — history, newest first, with summary/fingerprint/proposal when present.
-   **Example:** `dibbla apps maintenance run myapp --follow --json`

#### `apps proposals`

List, inspect and decide the app's **change queue**. Eligibility is the API `decision` object — the CLI never computes who may approve. The maintenance author cannot approve its own proposal.

-   **`apps proposals list <alias> [--json]`** — empty queue is exit 0.
-   **`apps proposals show <alias> <proposal-id> [--diff] [--json]`** — `--diff --json` is one `type: proposal_review` document with unmodified `proposal` and `diff` API objects.
-   **`apps proposals approve|deny|retry <alias> <proposal-id> [--yes]`** — POST to the server-owned decision endpoint. Typed conflict (e.g. `PROPOSAL_NOT_READY`) is exit 6.
-   **Example:** `dibbla apps proposals show myapp pr_0123456789abcdef0123 --diff --json`

#### `dibbla-checks.yaml` (the file those commands operate on)

Lives at the app root beside `dibbla.yaml`, and is promoted into an immutable, content-addressed snapshot by a **successful** deploy — editing it in the repo changes nothing until the next deploy. `version: 1`, then `checks:` (1–100), each with `id` (`^[a-z][a-z0-9-]*$`), `name`, **`description`** and `kind`, plus optional `schedule: nightly` (the only value; no raw cron), `failure_threshold` (default 2), `cooldown` (default 30m) and `run_deadline` (default 2m). Unknown keys are a rejected file — and so is a **missing** one: `description` is required on every check in all four kinds, and a file without it is rejected at deploy time, before the build, with `APPLICATION_CHECKS_DESCRIPTION_REQUIRED` naming the check that lacks it.

`description` (1–1000 chars) is the one field written for a human rather than the runner: *why the check exists and what it means that it failed*, not what it technically does — the steps already say that. It is what the console shows beside the check and what whoever is woken by the notification reads first. Write the sentence they would want at 03:00.

-   **`http_sequence`** — `steps:` where each step has **both** `request: {method, route, path}` and `expect: {status?, body_contains?, json_has?}`. `method` is `GET`, `HEAD` or `OPTIONS` only; `route` is a logical route id from the manifest (`public` for a plain web app) and `path` is relative — never a host or URL.
-   **`browser_journey`** — `steps:` where each step is exactly one of `navigate: {route, path}`, `click: {control}`, `fill: {control, value: {literal|secret_ref}}`, `assert_text: {text}`. `control` is a control's lowercase id, not its visible label. **`click` or `fill` require `identity_grant: <id>`** on the check — omit it and the file is rejected. A `fill` literal on a control whose id looks like a credential is rejected with `APPLICATION_CHECKS_INLINE_SECRET`; use `secret_ref`.
-   **`semantic`** — `request:`, `deterministic_expect:` and `judge: {rubric: <path>, output: {pass: boolean, reason: string}}`. `judge.output` is a **type declaration**: the literal words `boolean` and `string`, not example values.
-   **`composite`** — `checks: [<ids from this file>]` plus `reducer: all_required`.

When drafting one for a user, show the draft before writing the file, and never invent fields — the schema rejects unknown keys rather than ignoring them.

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
    -   `--api-url <url>`: API endpoint forwarded to `login` (e.g. `https://api.your-domain.com`).
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

Prints a psql-compatible connection string for connecting to a database via the Dibbla database proxy. Host and `sslmode` are derived from `DIBBLA_API_URL`: the `api.` host maps to the matching `db.` host on the same base domain, so `api.dibbla.com` → `db.dibbla.com` with `sslmode=require`; `localhost` / `127.0.0.1` use `sslmode=disable`. Override with `DIBBLA_DB_HOST` / `DIBBLA_DB_PORT` / `DIBBLA_DB_SSLMODE`. Uses your current API token as the password.

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
-   **Roles:** Reading a value needs the deploy roles (owner, admin, developer); a viewer can list, not get. `env pull` follows the same rule.
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

### `env`

Values live in Dibbla, names live in the code. Secrets and the variables the platform generates (`DATABASE_URL_*`, `STORAGE_*`, `DIBBLA_*`) are injected into the app when it runs; `.env.example` in the repository lists the names the app needs (one `NAME= # what it is` per line — the one `.env*` file the platform keeps in the app's repo). `dibbla env pull` fetches the values to a local `.env.local` so the app can run on this machine — that file never goes back: `.gitignore`, the VCS filter and the push hook all refuse it.

#### `env pull`

Writes the app's environment — as the running container sees it — to `.env.local`.

-   **Usage:** `dibbla env pull [-d <alias>] [-s <service>] [--replace] [--stdout] [--json]`
-   **App:** the one this folder is linked to (`dibbla clone` / `dibbla link`), or `--deployment <alias>`.
-   **Flags:**
    -   `--service`, `-s`: resolve one service's view of a multi-service app (its per-service secrets, its `DIBBLA_SVC_*`).
    -   `--replace`: rewrite `.env.local` from scratch. Without it an existing file is updated in place — keys Dibbla knows are refreshed, your other lines stay.
    -   `--stdout`: print `KEY=value` lines instead of writing a file (`eval "$(dibbla env pull --stdout)"`).
    -   `--json`: the API document — `variables[]` with `name`, `value` and `source` (`global` / `deployment` / `service` / `inline` / `platform`).
-   **Resolution:** exactly the runtime's — global secrets < deployment-wide < per-service < inline env (`deploy -e`, manifest) < injected `DIBBLA_*`. `DATABASE_URL_*` and `STORAGE_*` are in the set and reach the real database and buckets. `DIBBLA_IDENTITY_TOKEN_FILE` is left out (a file that exists only in the pod).
-   **The file:** mode 0600, first line `# Pulled from Dibbla — lives only on this machine; run 'dibbla env pull' again to refresh.` If `.gitignore` lacks a `.env.local` line, one is added and the command says so.
-   **Output:** names and counts, never values. Reminds you that a local run with this file uses the app's real database and buckets — Dibbla has one environment per app; offer a local Postgres if the person wants isolation.
-   **Roles:** owner, admin or developer — the same rule as `secrets get`; a viewer is refused.
-   **New variable:** `dibbla secrets set NAME value -d <alias>` → add `NAME= # what it is` to `.env.example` → `dibbla env pull`. Never `.env.local` by hand as the only place: it does not travel, so the deployed app would start without it.
-   **Example:** `dibbla env pull` — **One service:** `dibbla env pull -d shop -s worker` — **Into the shell:** `eval "$(dibbla env pull --stdout)"`

### `domains`

The `domains` command puts a deployed app on the user's own hostname (bring your own domain). It is the answer to "connect my domain" / "use www.example.com" — **not** the manifest's `domain:` field. The app keeps `https://<alias>.dibbla.com` alongside the custom hostname; login and sessions work on both.

The flow: `add` → the user creates **one CNAME** at their registrar → `verify` until active.

#### `domains add`

Connects a hostname and prints the DNS record to create.

-   **Usage:** `dibbla domains add <alias> <hostname> [--json]`
-   **Arguments:** `alias` (required) — the app; `hostname` (required) — e.g. `www.example.com` (no scheme, no path).
-   **Output:** `Type: CNAME`, `Host: www` (the label; `@` for a bare domain), `Target: cname.dibbla.com`, the full record line, the bare-domain advice, the current status and the `verify` command to run next. Relay the record to the user verbatim.
-   **Bare domain (apex):** recommend `www`. Most registrars (One.com, Loopia, GoDaddy, Namecheap) cannot put a CNAME on `example.com`; connect `www.example.com` and set up an HTTP redirect from the bare domain to www at the registrar. An apex hostname is accepted and flagged (`is_apex`) with the same advice.
-   **Errors:** `DOMAIN_TAKEN` (exit 6) — already connected to an app, here or in another organization; the message never says whose. `DOMAIN_INVALID` (exit 5) — not a bare DNS name / wildcard / IP / one of the platform's own domains. `DOMAINS_NOT_CONFIGURED` (503) — the installation has the feature off.
-   **Example:** `dibbla domains add myapp www.example.com`

#### `domains list`

-   **Usage:** `dibbla domains list <alias> [--json]`
-   **Output:** Every hostname on the app with hostname status, certificate status and active yes/no, plus a one-line explanation for each hostname that is not active yet. Statuses are refreshed from the edge on every read. A hostname that was disconnected shows status `disconnected` (parked — see `domains remove`).

#### `domains verify`

Fetches the hostname's live status from the edge and explains it.

-   **Usage:** `dibbla domains verify <alias> <hostname> [--json]`
-   **Verdicts:** `waiting for DNS` (the CNAME is not visible yet — propagation takes minutes, occasionally up to an hour; ask again rather than re-adding), `issuing certificate` (DNS is right, the certificate is a minute or two away), `active — serving with a valid certificate`, `disconnected` (parked after `domains remove`; `domains add` connects it again at once), or `error (…)` with the provider's reason. When waiting, the CNAME instruction is printed again.
-   **Exit:** 0 whatever the status (read the verdict); 4 when the hostname is not connected to this app.

#### `domains remove`

-   **Usage:** `dibbla domains remove <alias> <hostname> [--yes | -y]`
-   **Behaviour:** Disconnects the hostname from the app; the user's DNS record is untouched. The hostname is **parked**, not deleted: while the CNAME still points at the platform, visitors see Dibbla's "This site isn't connected" page instead of an edge error, and the hostname stays reserved for the organization. `dibbla domains add` of the same hostname (this or another of the organization's apps) connects it again at once, without a new certificate. The platform releases a parked hostname when the CNAME no longer points at it, or 30 days after disconnecting, whichever comes first — tell the user to remove the CNAME at the registrar when they are done with the domain. Pass `--yes` when running as an agent.

### `deploy`

The `deploy` command deploys a project to the Dibbla platform. **Detection is by file:** if `dibbla.yaml` (or `dibbla.yml`) is present at the deploy root, the multi-service path runs (manifest parse + resolve + parallel build + atomic apply with rollback). Otherwise the legacy single-`Dockerfile` path runs unchanged.

-   **Usage:** `dibbla deploy [path]`
-   **Arguments:**
    -   `path` (optional): The path to the project to deploy. Defaults to the current directory.
-   **Flags:**
    -   `--alias`, `-a`: Custom alias name (default: directory name).
    -   `--message`, `-m`: **Required for agents.** Deploy message used as the VCS commit subject in the app's Dibbla-managed git history (and on the GitHub mirror, if configured). Treat it like a git commit subject: present-tense imperative, under ~72 chars, covering what changed and why. Max 500 chars. Examples: `-m "fix: handle null org in /api/me"`, `-m "feat: add nightly db backup workflow"`, `-m "chore: bump node to 20.14"`. For retries/mechanical redeploys still say so: `-m "redeploy: retry after CF 524"`. Never omit `-m` — a blank deploy history is a bug, not a default.
    -   `--force`, `-f`: Recreate the deployment after a successful build if the alias already exists (brief restart while the new pod starts). The existing app is replaced only once the new image is built and pushed; a failed build leaves it running untouched and returns `BUILD_FAILED`.
    -   `--update`, `-u`: Rolling update of existing deployment (zero downtime). Mutually exclusive with `--force`.
    -   `--env`, `-e`: Set environment variable KEY=value (repeatable, Docker-style).
    -   `--cpu <value>`: CPU request (e.g. `500m`). **Ignored under multi-service** — set CPU per service in `dibbla.yaml`.
    -   `--memory <value>`: Memory request (e.g. `512Mi`). **Ignored under multi-service.**
    -   `--port <value>`: Container port (e.g. `3000`). **Ignored under multi-service.**
    -   `--favicon <url>`: Favicon URL (e.g. `https://example.com/favicon.ico`).
    -   `--target-env <name>`: Manifest env block to resolve (defaults to `prod` server-side). Multi-service only.
    -   `--profile <name>`: Activate a manifest profile (repeatable). Multi-service only.
    -   `--no-public`: Allow a deploy with no `public: true` service (worker- or cron-only deploys). Multi-service only.
    -   `--skip-review`: Bypass the pre-deploy gate (`REVIEW.md` + handbook). **Humans only** for trivial fixes — agents must run the guardrails workflow and emit `REVIEW.md` instead.
-   **Pre-deploy gate:** The CLI refuses to upload unless `REVIEW.md` and a user handbook (`docs/index.md` or `APP.md`) exist at the deploy root. Run the [pre-deploy guardrails](#pre-deploy-guardrails) checklist and write `REVIEW.md` before invoking `deploy`.
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

The `skills` command installs the skill files that teach AI coding agents (Claude Code, Cursor, Gemini CLI, Opencode, Codex, Copilot, Windsurf, Aider, etc.) how to use the Dibbla CLI. The skill content is embedded in the binary via `//go:embed`, so **`dibbla skills install` requires no network** and the installed skill version is always locked to the CLI version.

The same files are additionally published over HTTP at `https://dibbla.com/.well-known/agent-skills/index.json`, for agents that have no `dibbla` binary. Each entry carries a `sha256:` digest to verify against, and the bytes are mirrored from a tagged CLI release — so the version-locking story holds there too; only the transport differs.

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

### `clone` / `link`

Connects a local folder to the Dibbla-managed git repo of a deployed app. Each `dibbla deploy` writes a commit to a platform-managed bare repo; that repo is the app's version history, and `clone` lets you fetch it locally so you (or a coding agent) can inspect exactly what was deployed, diff between deploys, and continue working from it. Dibbla is the app's `origin`.

-   **Usage:** `dibbla clone <app> [--into <dir>]` or `dibbla link <app>` (the same command with `--into .` as the default — link the folder you are standing in). `<org>/<app>` is accepted; the org is derived from your token.
-   **Flags:**
    -   `--into <dir>`: Destination directory (default: `./<app>`; `link` defaults to `.`).
    -   `--yes` / `-y`: Link a folder that has its own git history without the prompt (agents: pass it when you mean it).
    -   `--ref <sha>`: Fresh clones only — commit to check out after cloning (default: latest on `main`).
-   **The folder's state decides what happens — never run `git clone` by hand:**
    -   Empty or missing folder → `git clone`; origin = Dibbla, `main` checked out.
    -   Files but no `.git` → without `--into` the app is cloned into `./<app>/` and the CLI says so; with `--into .` the folder is linked in place and the files are kept.
    -   A repo with no commits (a `git init` an agent already ran) → remote added, `main` fetched and checked out. No "destination path already exists" error.
    -   A repo with its own commits and no Dibbla remote → the CLI explains that every file on disk is kept but the history starts over from Dibbla's `main`, asks for confirmation (`--yes`), moves the old commits to a `pre-dibbla-<timestamp>` branch, and leaves `git status` showing disk vs Dibbla as changes to commit. Two histories are never merged.
    -   Already linked to this app → `git pull --ff-only`, "already linked — updated".
    -   `./<app>` exists and is not a clean clone target → `clone` without `--into` refuses with a hint (`--into ./<app>` to link it, or another dir). Nothing is touched.
-   **`dibbla status` in a linked folder** shows the app, org, `N commit(s) ahead / behind Dibbla` (after a fetch) and whether the commit the app runs is this folder's HEAD.
-   **Wrong organization:** a 403/404 names the org the CLI acted as (`Access denied for organization Acme (…)` / `App not found in organization Acme (…)`) and how to switch: `dibbla org list`, then `dibbla org use <name>`.
-   **Authentication:** Reuses the token from `dibbla login` / `DIBBLA_API_TOKEN` — no separate clone credential. `dibbla login` registers a git credential helper (`dibbla git-credential`) in your user git config for Dibbla's git host only, so `git clone`, `git pull` and `git push` against Dibbla authenticate from the stored login and the token never lands in `~/.git-credentials` or `.git/config`. Other remotes are unaffected. Not logged in or token expired → git prints `Run dibbla login`.
-   **Deliver changes with `deploy`, never `git push`.** From the clone directory, `dibbla deploy . --alias <app> -m "…" --update` is how a change is saved and shipped. The platform answers `git push` with 403 by design — the repo is written by deploys. Don't answer that 403 by proposing a GitHub/GitLab remote: Dibbla already holds the history, and a missing `origin` is not a problem. Tell the user (in their language) that Dibbla saves what is deployed, then deploy. The same holds for a local git repo with no remote: "save" means `dibbla deploy --update`.
-   **Continuing work from another machine:** `dibbla login` → `dibbla clone <app> --into <dir>` → edit → `dibbla deploy . --alias <app> -m "…" --update`. The deploy history is the sync: only deployed state travels (no uncommitted/undeployed edits from the other machine, no `.env`/secrets — those stay on the platform). If the team already keeps the source on GitHub/GitLab, clone from there and use `dibbla clone` to inspect what is running. See `examples.md` → "Continue work on another machine".
-   **Example:** `dibbla clone my-app` — **This folder:** `dibbla link my-app` (`--yes` when it has its own commits) — **Pin commit:** `dibbla clone my-app --ref abc1234` — **Custom dir:** `dibbla clone my-app --into ./checkout`

### Version control API (for scripting / agents)

Three read-only endpoints expose the same data surfaced by the console's Version Control card. Authenticate with `Authorization: Bearer $DIBBLA_API_TOKEN`.

-   `GET /api/deploy/deployments/<app>/vcs/info` — returns default branch, latest commit, clone URL, CLI command, and `running_sha` when the latest commit matches the Running deployment.
-   `GET /api/deploy/deployments/<app>/vcs/commits?limit=<n>&before=<sha>` — paginated commit list (newest first). The `deploy_id` field (from the `Deploy-Id:` trailer) correlates commits with deployments.
-   `GET /api/deploy/deployments/<app>/vcs/commits/<sha>` — commit detail with the file list at that tree.

Prefer `dibbla clone` over shelling out to `git clone` by hand — it resolves the canonical clone URL via `/vcs/info`, so it keeps working if the git host moves.

## Working through `/platform` instead of the CLI

The same platform is reachable over MCP at the OAuth-protected `/platform`
endpoint. The rule binding the two surfaces: **what a signed-in human can do
with the `dibbla` CLI, an OAuth grant with the right scope can do through
`/platform`.** Connect a client with `dibbla mcp platform`, and deploying,
restarting, configuring, setting secrets, provisioning databases and buckets,
applying workflows, running checks and deleting are tool calls rather than shell
commands.

Three things to know before looking for a tool:

- **Parity is measured in capabilities, not in tools.** Several commands map to
  one capability, and — more often — one tool delivers several capabilities. A
  full write grant lists **32 tools** for the whole platform, so do not expect a
  tool per command. Every destructive operation is still a read-only *plan*
  followed by an *execute* that carries a human's approval.
- **A tool is a flow, and the step is a parameter.** `platform_apps` lists your
  apps when you omit `alias` and reads one when you name it; `platform_operation`
  takes a `view` of `status`, `events`, `logs` or `output`;
  `platform_files` takes an `action`; `platform_destructive_plan` takes
  the `resource` to destroy. If you cannot find a tool for something, look for
  the parameter on the tool that owns the flow — see
  `.claude/skills/dibbla/platform.md` § 13 for the map.
- **What is not remote is written down.** The exceptions are `local-only` rows in
  the platform capability contract, each with a technical reason: building the
  deploy archive, running a local pipeline, revealing a credential in plaintext,
  reading a `.env` file, dumping a database through the caller's `pg_dump`,
  cloning to disk, scaffolding files, the keyring, the local context and org
  selection, updating the binary. See `.claude/skills/dibbla/platform.md` § 13
  for the full table, and
  <https://docs.dibbla.com/reference/platform-contract> for the authoritative
  one.

The rule is enforced rather than described: `dibbla-cli` fails its own build when
a command has no capability row, and `app-hosting-service` fails its own when a
row names a tool that is not in `tools/list` — or when the surface grows past
its 30-tool budget.

## Pre-deploy guardrails

Before calling `dibbla deploy`, you MUST review the application code and present findings to the user. **Never deploy autonomously** — always wait for explicit user confirmation.

**Enforced by the CLI.** `dibbla deploy` refuses to upload unless `REVIEW.md` and a user handbook (`docs/index.md` or `APP.md`) are present at the deploy root. The `--skip-review` flag exists for humans making trivial one-line fixes; agents must run the full checklist and emit `REVIEW.md` instead of passing the flag.

Run these six checks and report each as BLOCKER or WARNING:

1. **Security (OWASP Top 10)** — Hardcoded secrets, SQL/command injection, XSS, `.env` files in deploy dir, broken access control/IDOR (A01), SSRF (A10) and weak app-managed login (A07: unsalted/fast hashes, non-expiring sessions) are **BLOCKERs**. Missing CSRF, input validation, security headers, vulnerable dependencies (A06: `npm audit`/`govulncheck`/`pip-audit`) and missing rate limiting on login/reset/OTP (A04) are warnings.
2. **Database usage** — N+1 queries (query inside a loop) are **BLOCKERs**. Unbounded SELECTs, missing connection pooling, missing error handling are warnings.
3. **REST/API calls** — Outbound HTTP calls without timeouts are **BLOCKERs**. Missing retry/backoff, excessive polling (<5s), hardcoded URLs are warnings.
4. **External write safety** — Unbounded write loops to external systems are **BLOCKERs**. Missing rate limiting, missing idempotency, fire-and-forget writes are warnings.
5. **Personal data (GDPR)** — Write the inventory into the report even when all is well: which fields hold personal data and in which tables/buckets, how one person is deleted, whether they appear in logs, and which third parties receive them (mail, AI gateway, analytics, error trackers). Personal data stored without that inventory, or with no way to delete a person, is a **BLOCKER**. Incomplete deletion (no cascade, copies in indexes/storage), personal data in logs, unnamed third-party recipients and consent-less trackers are warnings. An app with no personal data says so in one line. This inventory is the owner's GDPR checklist.
6. **Support reachability** — When `dibbla.yaml` enables `support:` but the app exposes no visible way to reach it (no `/_platform/support.js` widget tag, no portal support link), report a **warning** and suggest the one-line tag. Never a blocker.

Present a checklist report to the user. If any BLOCKER is found, offer to fix it and wait for confirmation — do NOT deploy. If only warnings, ask the user whether to fix or proceed. If all clear, ask "Ready to deploy?" and wait for confirmation. Then write the report to `REVIEW.md` at the deploy root (see `.claude/skills/dibbla/guardrails.md` § Step 3.5 for the exact format).

## General Behavior

- The tool is interactive and will prompt for missing information.
- Always provide clear and direct commands.
- When scripting, use flags like `--yes` to avoid interactive prompts.
- Pay attention to the output for success messages, error details, and status information.
- The CLI performs update checks in the background for interactive TTY sessions. If the network is unavailable, failed checks are cached for 24 hours to avoid repeated request timeouts on every command invocation.
