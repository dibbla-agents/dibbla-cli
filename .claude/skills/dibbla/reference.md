# Dibbla CLI — Command reference

Complete usage, arguments, and flags for all commands.

---

## login

Authenticate with the Dibbla API and store the token in the OS credential store.

| Item | Details |
|------|---------|
| **Usage** | `dibbla login [api_url]` |
| **Arguments** | `api_url` (optional) — API endpoint (e.g. `api.dibbla.net` or `https://api.dibbla.net`). If omitted, the URL resolves in this order: `$DIBBLA_API_URL` → `$DIBBLA_AUTH_SERVICE_URL` → default `https://api.dibbla.com`. |
| **Flags** | `--browser` — skip the interactive menu; go directly to browser OAuth. Works in non-TTY contexts (Claude Code `!` prefix, agent shells) because the flow uses a localhost callback, not stdin. |
|  | `--api-key <token>` — pass a pre-generated token; works in any context |
| **Interactive** | Real TTY only: picker for "Log in with browser" or "Paste an API token" |
| **Note** | In CI, set `DIBBLA_API_TOKEN` (and optionally `DIBBLA_API_URL`) instead of running login. |

### logout

| Item | Details |
|------|---------|
| **Usage** | `dibbla logout` |
| **Output** | Removes stored token + api_url from the OS credential store |

---

## run

Run a `dibbla-task.yaml` pipeline locally using the dibbla-tasks steprunner.

| Item | Details |
|------|---------|
| **Usage** | `dibbla run [path-or-url]` |
| **Arguments** | (omitted) — runs `./dibbla-task.yaml` from the current directory |
|  | `<local-path>` — runs the given file (work_dir defaults to the yaml's parent directory) |
|  | `<https-url>` — fetches the yaml (5 MB max, 30 s timeout) and runs it with work_dir = your invocation CWD |
| **Flags** | `--preview` — parse and print the execution plan without running anything |
|  | `--env KEY=VAL` — set/override an env var for all steps (repeatable) |
|  | `--env-file <path>` — load env vars from a `.env`-style file |
|  | `--work-dir <dir>` — override working directory for command steps |
|  | `--format plain\|gh` — output format (default `plain`; `gh` emits GitHub Actions workflow commands) |
| **Env injected into steps** | `DIBBLA_API_TOKEN`, `DIBBLA_AUTH_SERVICE_URL` (both when logged in); `DIBBLA_CMD` (path to the running dibbla binary — used by bootstrap yamls for recursive invocation regardless of PATH state) |
| **Security** | URL-fetched yamls become shell commands on the user's machine. Treat them as `curl \| bash` — only run yamls from trusted sources (e.g. `github.com/dibbla-agents/*`). |
| **Exit code** | `0` on success; `1` on step failure or setup error |

---

## template

Discover and install Dibbla templates from the hosted manifest.

Manifest URL (default): `https://raw.githubusercontent.com/dibbla-agents/dibbla-public-templates/master/templates.json`. Override with `DIBBLA_TEMPLATES_URL` to point at a staging or local manifest.

Cache lives at `~/.dibbla/templates-cache.json`. Resolution: fresh cache (< 1 h) → used silently; stale (1–24 h) → network fetch attempted, falls back to cache on failure; cold (> 24 h) → must fetch, else embedded fallback (2 templates: `getting-started`, `expense-reporter`).

### template list

| Item | Details |
|------|---------|
| **Usage** | `dibbla template list` |
| **Flags** | `--refresh` — force re-fetch of the manifest, bypassing fresh cache |
|  | `-v`, `--verbose` — print the manifest source used (cache / network / embedded) |
| **Output** | Table: `ID  NAME  CATEGORY  DESCRIPTION` |

### template install

| Item | Details |
|------|---------|
| **Usage** | `dibbla template install <id> [<dir>]` |
| **Arguments** | `id` (required) — template slug from the manifest (e.g. `getting-started`, `expense-reporter`, `crm`, `presentation`) |
|  | `dir` (optional) — destination directory; defaults to the manifest's `template_path` for that id (e.g. `./expense-reporter-template-1`) |
| **Flags** | `--force` — overwrite (reuse) the destination directory if it already exists |
|  | `--refresh` — force re-fetch of the manifest before installing |
| **Behavior** | `mkdir` destination → `chdir` into it → run the template's `bootstrap_url`. The bootstrap clones the project subtree from the templates repo into CWD and recursively invokes `dibbla run ./dibbla-task.yaml` inside the cloned directory. |
| **Refuses** | If the destination directory already exists and `--force` is not passed |
| **Exit code** | `0` on success; `1` on any failure (manifest lookup, mkdir, bootstrap pipeline) |

---

## feedback

Send, list, and manage feedback.

| Item | Details |
|------|---------|
| **Usage** | `dibbla feedback <message>` |
| **Arguments** | `message` (required) — all arguments are joined into one message |
| **Output** | `Feedback <id> received. Thank you!` |

### feedback list

| Item | Details |
|------|---------|
| **Usage** | `dibbla feedback list` |
| **Output** | Table: ID, USER, DATE, MESSAGE |

### feedback delete

| Item | Details |
|------|---------|
| **Usage** | `dibbla feedback delete <feedback-id>` |
| **Arguments** | `feedback-id` (required) — the feedback UUID |
| **Flags** | `--yes`, `-y` — skip confirmation |

---

## deploy

Deploy a containerized app from a directory. App URL: `https://<alias>.dibbla.com`.

| Item | Details |
|------|---------|
| **Usage** | `dibbla deploy [path]` |
| **Arguments** | `path` (optional) — directory to deploy; default `.` |
| **Flags** | `--alias`, `-a` — custom alias name (default: directory name) |
|| | `--force`, `-f` — force redeploy if alias exists (causes downtime) |
| | `--update`, `-u` — rolling update of existing deployment (zero downtime) |
| | `--env`, `-e` — env var `KEY=value` (repeatable) |
| | `--cpu` — CPU request (e.g. `500m`) |
| | `--memory` — Memory request (e.g. `512Mi`) |
| | `--port` — Container port (e.g. `3000`) |
| | `--favicon` — Favicon URL (e.g. `https://example.com/favicon.ico`) |
| | `--require-login` — Require authentication to access the app |
| | `--access-policy` — Access policy: `all_members` or `invite_only` |
| | `--google-scopes` — Google OAuth scope URL (repeatable) |
| **Note** | `--force` and `--update` are mutually exclusive |

---

## apps

### apps list

| Item | Details |
|------|---------|
| **Usage** | `dibbla apps list` |
| **Output** | Table: ALIAS, URL, STATUS, LAST DEPLOYED |

### apps update

| Item | Details |
|------|---------|
| **Usage** | `dibbla apps update <alias>` |
| **Arguments** | `alias` (required) — deployment alias |
| **Flags** | `--env`, `-e` — env var `KEY=value` (repeatable) |
| | `--replicas` — desired replica count |
| | `--cpu` — CPU request/limit (e.g. `500m`, `1`) |
| | `--memory` — Memory request/limit (e.g. `256Mi`, `512Mi`) |
| | `--port` — Container port (1–65535) |
| | `--favicon` — Favicon URL (use `""` to clear) |
| | `--require-login` — Require login: `true` or `false` |
| | `--access-policy` — Access policy: `all_members`, `invite_only`, or `""` to clear |
| | `--google-scopes` — Google OAuth scope URL (repeatable, use `""` to clear) |
| **Rule** | At least one of: `--env`, `--replicas`, `--cpu`, `--memory`, `--port`, `--favicon`, `--require-login`, `--access-policy`, `--google-scopes` required |

### apps delete

| Item | Details |
|------|---------|
| **Usage** | `dibbla apps delete <alias>` |
| **Arguments** | `alias` (required) |
| **Flags** | `--yes`, `-y` — skip confirmation |

---

## db

### db list

| Item | Details |
|------|---------|
| **Usage** | `dibbla db list [--quiet | -q]` |
| **Flags** | `--quiet`, `-q` — names only, one per line (scripting) |

### db create

| Item | Details |
|------|---------|
| **Usage** | `dibbla db create [name]` or `dibbla db create --name <name>` |
| **Arguments** | `name` (optional as position) — database name |
| **Flags** | `--name` — database name (alternative to argument) |
| | `--deployment <alias>` — scope the database and its `DATABASE_URL` secret to a specific deployment (omit for global) |
| **Rule** | Name required via argument or `--name` |

### db delete

| Item | Details |
|------|---------|
| **Usage** | `dibbla db delete <name>` |
| **Arguments** | `name` (required) |
| **Flags** | `--yes`, `-y` — skip confirmation |
| | `--quiet`, `-q` — errors only (scripting) |

### db dump

| Item | Details |
|------|---------|
| **Usage** | `dibbla db dump <name> [--output <file> | -o <file>]` |
| **Arguments** | `name` (required) |
| **Flags** | `--output`, `-o` — output path; default `<name>.dump` |
| **Output** | Custom-format pg_dump archive |

### db restore

| Item | Details |
|------|---------|
| **Usage** | `dibbla db restore <name> --file <path>` or `-f <path>` |
| **Arguments** | `name` (required) — target database |
| **Flags** | `--file`, `-f` (required) — path to dump file |

### db connect

| Item | Details |
|------|---------|
| **Usage** | `dibbla db connect <name> [--quiet | -q]` |
| **Arguments** | `name` (required) — database name |
| **Flags** | `--quiet`, `-q` — print only the connection string (scripting) |
| **Output** | psql-compatible connection string via Dibbla database proxy (`db.dibbla.com`). Uses API token as password; TLS encrypted. |

---

## secrets

Secrets are **global** (omit `--deployment`) or **deployment-scoped** (`--deployment <alias>` or `-d <alias>`).

### secrets list

| Item | Details |
|------|---------|
| **Usage** | `dibbla secrets list [--deployment <alias> | -d <alias>]` |
| **Flags** | `--deployment`, `-d` — list secrets for this deployment only; omit for global |

### secrets set

| Item | Details |
|------|---------|
| **Usage** | `dibbla secrets set <name> [value] [--deployment <alias> | -d <alias>]` |
| **Arguments** | `name` (required), `value` (optional — if omitted, read from stdin) |
| **Flags** | `--deployment`, `-d` — attach to deployment; omit for global |

### secrets get

| Item | Details |
|------|---------|
| **Usage** | `dibbla secrets get <name> [--deployment <alias> | -d <alias>]` |
| **Arguments** | `name` (required) |
| **Flags** | `--deployment`, `-d` — for deployment-scoped secret |
| **Output** | Secret value only (pipeline-friendly) |

### secrets delete

| Item | Details |
|------|---------|
| **Usage** | `dibbla secrets delete <name> [--deployment <alias>] [--yes | -y]` |
| **Arguments** | `name` (required) |
| **Flags** | `--deployment`, `-d` — for deployment-scoped secret |
| | `--yes`, `-y` — skip confirmation |

---

## workflows

Alias: `wf`. All workflow commands support these persistent flags:

| Flag | Description |
|------|-------------|
| `--output`, `-o` | Output format: `yaml`, `json`, or `table` |
| `--quiet`, `-q` | Minimal output |
| `--verbose`, `-v` | Show HTTP request/response details |

### workflows list

| Item | Details |
|------|---------|
| **Usage** | `dibbla workflows list` |
| **Output** | Table: NAME, LABEL, NODES, HAS_API (default); or JSON/YAML with `-o` |

### workflows get

| Item | Details |
|------|---------|
| **Usage** | `dibbla workflows get <name>` |
| **Arguments** | `name` (required) — workflow name |
| **Flags** | `--revision` — get a specific revision |
| **Output** | YAML (default) or JSON with `-o json` |

### workflows create

| Item | Details |
|------|---------|
| **Usage** | `dibbla workflows create --file <path>` or `-f <path>` |
| **Flags** | `--file`, `-f` (required) — workflow definition file (YAML or JSON) |

### workflows update

| Item | Details |
|------|---------|
| **Usage** | `dibbla workflows update <name> --file <path>` |
| **Arguments** | `name` (required) — workflow to replace |
| **Flags** | `--file`, `-f` (required) — workflow definition file (YAML or JSON) |

### workflows delete

| Item | Details |
|------|---------|
| **Usage** | `dibbla workflows delete <name>` |
| **Arguments** | `name` (required) |
| **Flags** | `--yes` — skip confirmation |

### workflows validate

| Item | Details |
|------|---------|
| **Usage** | `dibbla workflows validate --file <path>` or `-f <path>` |
| **Flags** | `--file`, `-f` (required) — workflow definition to validate (not saved) |

### workflows execute

| Item | Details |
|------|---------|
| **Usage** | `dibbla workflows execute <name>` |
| **Arguments** | `name` (required) — workflow to execute |
| **Flags** | `--data` — inline JSON data to send |
| | `--file`, `-f` — JSON data file |
| | `--node` — target a specific API node ID |

### workflows url

| Item | Details |
|------|---------|
| **Usage** | `dibbla workflows url <name>` |
| **Arguments** | `name` (required) |
| **Flags** | `--revision` — URL for a specific revision |
| **Output** | Plain URL (default); JSON/YAML with `-o` |

### workflows api-docs

| Item | Details |
|------|---------|
| **Usage** | `dibbla workflows api-docs <name>` |
| **Arguments** | `name` (required) |
| **Flags** | `--revision` — docs for a specific revision |
| **Output** | Human-readable endpoint docs (default); JSON/YAML with `-o` |

---

## nodes

### nodes add

| Item | Details |
|------|---------|
| **Usage** | `dibbla nodes add <workflow>` |
| **Arguments** | `workflow` (required) — target workflow name |
| **Flags** | `--file`, `-f` — node definition file (YAML/JSON) |
| | `--inline` — inline node definition (JSON string) |
| **Rule** | Either `--file` or `--inline` is required |

### nodes remove

| Item | Details |
|------|---------|
| **Usage** | `dibbla nodes remove <workflow> <node_id>` |
| **Arguments** | `workflow` (required), `node_id` (required) |
| **Flags** | `--yes` — skip confirmation |

---

## edges

### edges add

| Item | Details |
|------|---------|
| **Usage** | `dibbla edges add <workflow> "<src.port -> tgt.port>"` |
| **Arguments** | `workflow` (required), edge spec (required) |

### edges remove

| Item | Details |
|------|---------|
| **Usage** | `dibbla edges remove <workflow> "<src.port -> tgt.port>"` |
| **Arguments** | `workflow` (required), edge spec (required) |

### edges list

| Item | Details |
|------|---------|
| **Usage** | `dibbla edges list <workflow>` |
| **Arguments** | `workflow` (required) |
| **Output** | Table (default); JSON/YAML with `-o` |

---

## inputs

### inputs set

| Item | Details |
|------|---------|
| **Usage** | `dibbla inputs set <workflow> <node> <input> <value>` |
| **Arguments** | `workflow`, `node`, `input`, `value` (all required) |
| **Flags** | `--null` — set value to null instead of string |

---

## tools

### tools add

| Item | Details |
|------|---------|
| **Usage** | `dibbla tools add <workflow> <agent> <tool>` |
| **Arguments** | `workflow`, `agent` (node ID), `tool` (all required) |

### tools remove

| Item | Details |
|------|---------|
| **Usage** | `dibbla tools remove <workflow> <agent> <tool>` |
| **Arguments** | `workflow`, `agent` (node ID), `tool` (all required) |

---

## revisions

Alias: `rev`.

### revisions list

| Item | Details |
|------|---------|
| **Usage** | `dibbla revisions list <workflow>` |
| **Arguments** | `workflow` (required) |
| **Output** | Table: ID, TIMESTAMP, LABEL (default); JSON/YAML with `-o` |

### revisions create

| Item | Details |
|------|---------|
| **Usage** | `dibbla revisions create <workflow>` |
| **Arguments** | `workflow` (required) |
| **Output** | Revision ID (with `-q` prints only the ID) |

### revisions restore

| Item | Details |
|------|---------|
| **Usage** | `dibbla revisions restore <workflow> <revision_id>` |
| **Arguments** | `workflow` (required), `revision_id` (required) |

---

## functions

Alias: `fn`.

### functions list

| Item | Details |
|------|---------|
| **Usage** | `dibbla functions list` |
| **Flags** | `--server` — filter by server name |
| | `--tag` — filter by tag |
| **Output** | Table: NAME, SERVER, DESCRIPTION, TOOLS (default); JSON/YAML with `-o` |

### functions get

| Item | Details |
|------|---------|
| **Usage** | `dibbla functions get <server> <name>` |
| **Arguments** | `server` (required), `name` (required) |
| **Output** | YAML (default) or JSON with `-o json` |

---

---

## Summary table

| Area | Command | Purpose |
|------|---------|---------|
| Auth | `dibbla login [api_url]` | Interactive browser/paste login (real TTY) |
| Auth | `dibbla login --browser` | Non-TTY browser OAuth (Claude Code, agent shells) |
| Auth | `dibbla login --api-key <token>` | Headless token login (CI, scripted) |
| Auth | `dibbla logout` | Clear stored credentials |
| Run | `dibbla run [path\|url]` | Execute a dibbla-task.yaml pipeline locally |
| Run | `dibbla run --preview <arg>` | Parse + print execution plan (no execution) |
| Template | `dibbla template list` | List available templates from the hosted manifest |
| Template | `dibbla template install <id> [<dir>]` | Materialize a template into a directory and run its bootstrap |
| Feedback | `dibbla feedback <message>` | Send feedback |
| Feedback | `dibbla feedback list` | List feedback |
| Feedback | `dibbla feedback delete <id>` | Delete feedback |
| Deploy | `dibbla deploy [path]` | Deploy app from directory |
| Apps | `dibbla apps list` | List deployments |
| Apps | `dibbla apps update <alias> ...` | Update env, replicas, cpu, memory, port, login guard |
| Apps | `dibbla apps delete <alias>` | Delete deployment |
| Db | `dibbla db list [-q]` | List databases |
| Db | `dibbla db create [name]` | Create database |
| Db | `dibbla db delete <name>` | Delete database |
| Db | `dibbla db dump <name> [-o file]` | Download dump |
| Db | `dibbla db restore <name> -f <file>` | Restore from dump |
| Db | `dibbla db connect <name> [-q]` | Print connection string |
| Secrets | `dibbla secrets list [-d alias]` | List global or app secrets |
| Secrets | `dibbla secrets set <name> [value] [-d alias]` | Create/update secret |
| Secrets | `dibbla secrets get <name> [-d alias]` | Print secret value |
| Secrets | `dibbla secrets delete <name> [-d alias]` | Delete secret |
| Workflows | `dibbla workflows list` | List all workflows |
| Workflows | `dibbla workflows get <name>` | Get workflow definition |
| Workflows | `dibbla workflows create -f <file>` | Create workflow from file |
| Workflows | `dibbla workflows update <name> -f <file>` | Replace workflow definition |
| Workflows | `dibbla workflows delete <name>` | Delete workflow |
| Workflows | `dibbla workflows validate -f <file>` | Validate without saving |
| Workflows | `dibbla workflows execute <name>` | Execute workflow |
| Workflows | `dibbla workflows url <name>` | Get UI URL |
| Workflows | `dibbla workflows api-docs <name>` | Show API endpoint docs |
| Nodes | `dibbla nodes add <wf> -f <file>` | Add node to workflow |
| Nodes | `dibbla nodes remove <wf> <id>` | Remove node |
| Edges | `dibbla edges add <wf> "<edge>"` | Add edge |
| Edges | `dibbla edges remove <wf> "<edge>"` | Remove edge |
| Edges | `dibbla edges list <wf>` | List edges |
| Inputs | `dibbla inputs set <wf> <node> <input> <val>` | Set node input |
| Tools | `dibbla tools add <wf> <agent> <tool>` | Add tool to agent |
| Tools | `dibbla tools remove <wf> <agent> <tool>` | Remove tool from agent |
| Revisions | `dibbla revisions list <wf>` | List revisions |
| Revisions | `dibbla revisions create <wf>` | Create snapshot |
| Revisions | `dibbla revisions restore <wf> <id>` | Restore revision |
| Functions | `dibbla functions list` | List available functions |
| Functions | `dibbla functions get <server> <name>` | Get function details |
