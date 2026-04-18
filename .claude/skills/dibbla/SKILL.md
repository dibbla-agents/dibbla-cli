---
name: dibbla
description: Use the Dibbla CLI to scaffold projects, run dibbla-task.yaml pipelines locally (dibbla run), discover and install project templates (dibbla template list/install), deploy apps, and manage applications, databases, secrets, and workflows on the Dibbla platform. Use when the user wants to run a local task file or a template from a URL, install a starter template, log in (including from non-TTY contexts like Claude Code via `dibbla login --browser`), deploy, list/update/delete apps, create/list/delete/dump/restore databases, manage secrets, or manage workflows (create/execute/validate workflows, manage nodes/edges/inputs/tools, revisions, and browse functions).
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
| Login      | `login [api_url]`, `login --browser`, `login --api-key <token>`, `logout` |
| Feedback   | `feedback <message>`, `feedback list`, `feedback delete <id>` |
| Deploy     | `deploy [path] [--alias name] [--require-login] [--access-policy] [--google-scopes]` — deploy from directory |
| Apps       | `apps list`, `apps update <alias>`, `apps delete <alias>` |
| Db         | `db list`, `db create`, `db delete`, `db dump`, `db restore`, `db connect` |
| Secrets    | `secrets list`, `secrets set`, `secrets get`, `secrets delete` (global or `-d <alias>`) |
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
   dibbla deploy . --alias my-app -e DATABASE_URL=postgres://... -e API_KEY=secret -e NODE_ENV=production
   ```
3. If it **already** exists, use `--update` for a zero-downtime rolling update:
   ```bash
   dibbla deploy . --alias my-app --update
   ```
   To change env vars on an existing app, use `apps update` instead:
   ```bash
   dibbla apps update my-app -e NEW_VAR=value
   ```

**Key rules:**
- `--force` causes downtime (tears down and redeploys). Prefer `--update` for existing apps.
- `--force` and `--update` are mutually exclusive.
- Environment variables set via `deploy -e` or `apps update -e` persist across updates — you only need to pass them once.
- **Login guard:** Use `--require-login` to require authentication. Combine with `--access-policy invite_only` to restrict to invited users, or `all_members` for org-wide access. Use `--google-scopes` to request additional Google OAuth scopes (e.g. Drive, Calendar).
- Use `--quiet` / `-q` on `db list`, `db delete`, `db connect` for machine-readable output in scripts.
- `db create --deployment <alias>` scopes the database and its auto-created `DATABASE_URL` secret to a specific deployment.
- `db connect` prints a psql-compatible connection string via the Dibbla database proxy. Use `-q` for scripting: `psql $(dibbla db connect mydb -q)`.

**Pre-deploy guardrails:** Before calling `dibbla deploy`, you MUST complete the pre-deploy checklist and present findings to the user. Always wait for explicit user confirmation before deploying or fixing issues — never deploy autonomously. The guardrails workflow also writes a `REVIEW.md` file to the project root — the platform reads this and displays a review status indicator in the dashboard. See [guardrails.md](guardrails.md) for the full checklist.

**Non-TTY / agentic invocation:**
- When running from inside Claude Code's `!` prefix, an agent shell, CI with a browser, or any other non-TTY context, use `dibbla login --browser` instead of bare `dibbla login`. The interactive flow needs stdin for the survey picker; `--browser` skips that and goes straight to browser-based OAuth via a localhost callback.
- For true headless (SSH sessions, cloud VMs, CI runners with no local browser), use `dibbla login --api-key <token>` or set `DIBBLA_API_TOKEN` (and optionally `DIBBLA_API_URL`) env vars — the CLI reads env vars in CI automatically.
- The CLI accepts `DIBBLA_AUTH_SERVICE_URL` as a fallback for `DIBBLA_API_URL` when invoked from inside a `dibbla run` task. The steprunner injects the former (matching desktop/steprunner convention), so inner `dibbla login --api-key=$DIBBLA_API_TOKEN` calls target the right service without needing an explicit URL arg.

**Running task files and templates:**
- `dibbla run <path>` executes a `dibbla-task.yaml` pipeline locally. Tool checks, shell commands, background dev servers, and browser-open side effects are all possible — the task file becomes shell under the user's account.
- `dibbla run <https-url>` fetches and executes a yaml from the network. **This is equivalent to `curl | bash`** — only run yamls from sources the user trusts (e.g. `github.com/dibbla-agents/*`). Work-dir defaults to the user's invocation CWD, so bootstrap clones land in the expected directory rather than in a temp dir.
- `dibbla template install <id>` is ergonomic sugar over `mkdir ./<template-path> && cd ./<template-path> && dibbla run <bootstrap-url>`. It refuses if the destination directory exists; pass `--force` to reuse. Use `dibbla template list` to see available ids.
- Prefer `dibbla run --preview` or `dibbla template list` before actually running, so the user can see what will execute.

## Additional resources

- **Full command and flag reference:** see [reference.md](reference.md) for usage, arguments, and all flags.
- **Usage examples:** see [examples.md](examples.md) for copy-paste examples and scripting patterns.
- **Pre-deploy guardrails:** see [guardrails.md](guardrails.md) for the mandatory pre-deploy checklist.

When suggesting or generating `dibbla` commands, use the reference for exact syntax and the examples for typical workflows.
