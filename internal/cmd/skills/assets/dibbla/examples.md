# Dibbla CLI — Examples

Copy-paste examples for common workflows. For full usage and flags see [reference.md](reference.md).

---

## Installing dibbla

```bash
# macOS — Homebrew
brew install dibbla-agents/tap/dibbla

# macOS / Linux — shell installer (drops binary into ~/.local/bin)
curl -fsSL https://install.dibbla.com/install.sh | sh

# Windows — PowerShell
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://install.dibbla.com/install.ps1 | iex"

# Verify
dibbla --version

# Upgrade (same command as install — replaces in place)
curl -fsSL https://install.dibbla.com/install.sh | sh
# …or on macOS Homebrew:
brew upgrade dibbla
```

---

## Login (including Claude Code / agentic tools)

```bash
# Interactive (real TTY — picks between browser OAuth and paste-token)
dibbla login api.dibbla.net

# From inside Claude Code's `!` prefix, an agent shell, or any non-TTY context
! dibbla login --browser api.dibbla.net
# Opens your default browser via localhost callback. No stdin needed.

# Headless (no browser available — CI, SSH, containers)
dibbla login api.dibbla.net --api-key ak_...
# or:
export DIBBLA_API_TOKEN=ak_...
export DIBBLA_API_URL=https://api.dibbla.net
dibbla deploy .          # reads env vars directly; no login needed

# Cloud VM / SSH / Docker (no keyring service installed)
# Validates the token, writes credentials to ./.env, patches .gitignore,
# does NOT touch the OS keyring (libsecret/gnome-keyring/pass may not exist).
# Requires CLI >= v1.2.4.
dibbla login --api-key=ak_... --api-url=https://api.dibbla.net --write-env --no-keychain

# Afterwards, every dibbla command in that directory reads credentials from .env:
dibbla deploy .
dibbla apps list

# Log out (clears keyring)
dibbla logout
```

---

## Running task files locally

```bash
# Run ./dibbla-task.yaml in the current directory
dibbla run

# Run a specific local task file
dibbla run ./setup/dibbla-task.yaml

# Preview (parse + print plan, do not execute)
dibbla run --preview ./dibbla-task.yaml

# Run a bootstrap yaml from GitHub — clones into your CWD and runs setup
mkdir my-project && cd my-project
dibbla run https://raw.githubusercontent.com/dibbla-agents/dibbla-public-templates/master/getting-started.dibbla-task.yaml

# Override env vars and working directory
dibbla run --env PORT=3000 --env-file .env.local --work-dir ./build ./dibbla-task.yaml

# Switch output format for CI / GitHub Actions
dibbla run --format gh ./dibbla-task.yaml
```

---

## Discovering and installing templates

```bash
# See what's available
dibbla template list

# Force re-fetch + show manifest source (cache / network / embedded)
dibbla template list --refresh -v

# Install a template into its default-named directory
dibbla template install expense-reporter
# → creates ./expense-reporter-template-1 and runs the bootstrap pipeline

# Install a template into a custom directory
dibbla template install getting-started my-starter-app

# Reuse an existing destination directory
dibbla template install crm --force
```

---

## Skills (teach AI coding agents about the CLI)

Install the bundled `dibbla` skill so every coding agent in the project reads it automatically. The skill content is embedded in the CLI binary — no network needed, and the skill version is locked to your installed `dibbla` version.

```bash
# see what skills are bundled (one for now: 'dibbla')
dibbla skills list

# install into the current project
dibbla skills install dibbla
# → writes .claude/skills/dibbla/{SKILL.md,examples.md,guardrails.md,reference.md}
#   and an AGENTS.md + GEMINI.md pointer block at the project root

# install into $HOME once so every project picks up the skill
dibbla skills install dibbla --user

# Claude Code only (skip AGENTS.md and GEMINI.md)
dibbla skills install dibbla --no-agents

# re-run is a clean no-op if nothing changed;
# use --force to restore skill files that were edited locally
dibbla skills install dibbla --force
```

**What each output does:**

| File | Used by |
|------|---------|
| `.claude/skills/dibbla/SKILL.md` | Claude Code (native skill format, gives `/dibbla` slash command) |
| `AGENTS.md` | Cursor, Opencode, Codex, Copilot, Windsurf, Aider, Zed, Warp, RooCode (AGENTS.md open standard) |
| `GEMINI.md` | Gemini CLI (its default context filename) |

`AGENTS.md` and `GEMINI.md` use a marker-delimited block (`<!-- >>> dibbla skill >>> -->` … `<!-- <<< dibbla skill <<< -->`). Running `dibbla skills install dibbla` again replaces only that block — any other content you've added to those files is preserved byte-for-byte.

**Inside a `dibbla-task.yaml` bootstrap step:**

```yaml
- id: install-skills
  name: "Install Dibbla Skill"
  type: command
  run: "dibbla skills install dibbla"
  depends_on: ["update-dibbla"]
```

The `depends_on: ["update-dibbla"]` ensures the CLI is fresh enough to have the `skills` command before this step runs.

---

## Feedback

```bash
dibbla feedback "The deploy took too long"
dibbla feedback Everything is on fire
dibbla feedback "Why does the database keep disappearing?"

# List all feedback
dibbla feedback list

# Delete feedback
dibbla feedback delete 550e8400-e29b-41d4-a716-446655440000
dibbla feedback delete 550e8400-e29b-41d4-a716-446655440000 --yes
```

---

## Deploy

```bash
dibbla deploy
dibbla deploy ./my-app
dibbla deploy --alias my-api       # Custom alias instead of directory name
dibbla deploy --force
dibbla deploy --update             # Rolling update (zero downtime)
dibbla deploy -e NODE_ENV=production -e LOG_LEVEL=info
dibbla deploy --cpu 500m --memory 512Mi --port 3000
dibbla deploy --favicon https://example.com/favicon.ico
dibbla deploy ./ --cpu 500m --memory 512Mi -e NODE_ENV=production

# Deploy with login guard
dibbla deploy --alias my-app --require-login
dibbla deploy --alias my-app --require-login --access-policy invite_only
dibbla deploy --alias my-app --require-login --google-scopes https://www.googleapis.com/auth/drive.readonly
```

### Deploy troubleshooting

#### Cloudflare 524 / "timeout occurred" during deploy

`dibbla deploy` holds a single HTTP connection to the backend while the container image is built. Builds that take longer than ~100 seconds (common for Next.js, Rails, or large monorepos) may return a Cloudflare 524 on the client side **even though the backend build is still running and often succeeds.** A 524 is not necessarily a failure.

Recovery:

1. Wait 2–5 minutes for the backend build to finish.
2. Run `dibbla apps list` and look for the alias.
3. If it appears with `running` status, the deploy succeeded — you are done.
4. If the alias does not appear after ~10 minutes, retry with `dibbla deploy --update` (rolling, zero downtime if the previous attempt did quietly succeed). Avoid `--force`, which causes downtime if the deploy actually worked.

---

## Apps

```bash
dibbla apps list
dibbla apps update myapp -e NODE_ENV=production
dibbla apps update myapp -e NODE_ENV=production -e LOG_LEVEL=info
dibbla apps update myapp --replicas 3
dibbla apps update myapp --cpu 500m --memory 512Mi --port 3000
dibbla apps update myapp --replicas 2 --cpu 1 --memory 512Mi -e NODE_ENV=production
dibbla apps update myapp --favicon https://example.com/favicon.ico
dibbla apps update myapp --favicon ""   # Clear favicon

# Login guard settings
dibbla apps update myapp --require-login true
dibbla apps update myapp --require-login false          # Disable login guard
dibbla apps update myapp --access-policy invite_only
dibbla apps update myapp --access-policy ""             # Clear access policy
dibbla apps update myapp --google-scopes https://www.googleapis.com/auth/drive.readonly
dibbla apps update myapp --google-scopes https://www.googleapis.com/auth/drive.readonly --google-scopes https://www.googleapis.com/auth/calendar.readonly
dibbla apps delete my-old-app
dibbla apps delete my-old-app -y
```

---

## Db

```bash
dibbla db list
dibbla db list -q
dibbla db create my-new-db
dibbla db create --name my-new-db
dibbla db create mydb --deployment myapp   # Scope DB + DATABASE_URL secret to a deployment
dibbla db delete my-old-db
dibbla db delete my-old-db --yes
dibbla db delete my-old-db --yes -q
dibbla db dump my-production-db
dibbla db dump my-production-db -o backup.dump
dibbla db restore my-staging-db --file backup.dump
dibbla db restore my-staging-db -f /tmp/backup.dump
dibbla db connect myapp                    # Print connection string with tips
dibbla db connect myapp -q                 # Connection string only (scripting)
psql $(dibbla db connect myapp -q)         # Quick connect
export DATABASE_URL=$(dibbla db connect myapp -q)  # Export as env var
```

---

## Secrets

**Global (omit `-d`):**

```bash
dibbla secrets list
dibbla secrets set API_KEY "my-secret-value"
echo "my-secret-value" | dibbla secrets set API_KEY
dibbla secrets get API_KEY
dibbla secrets delete API_KEY --yes
```

**Per-deployment (`-d` or `--deployment`):**

```bash
dibbla secrets list -d myapp
dibbla secrets set API_KEY "x" -d myapp
dibbla secrets set DATABASE_URL "postgres://..." --deployment myapp
cat private.key | dibbla secrets set SSL_KEY -d myapp
dibbla secrets get API_KEY -d myapp
dibbla secrets delete API_KEY -d myapp -y
```

---

## Workflows

```bash
# List and inspect
dibbla workflows list
dibbla workflows list -o json
dibbla wf list                        # alias

# Get a workflow definition
dibbla workflows get my-workflow
dibbla workflows get my-workflow -o json
dibbla workflows get my-workflow --revision abc123

# Create, update, validate
dibbla workflows create -f workflow.yaml
dibbla workflows update my-workflow -f workflow.yaml
dibbla workflows validate -f workflow.yaml

# Delete
dibbla workflows delete my-workflow
dibbla workflows delete my-workflow --yes

# Execute
dibbla workflows execute my-workflow
dibbla workflows execute my-workflow --data '{"query": "hello"}'
dibbla workflows execute my-workflow -f input.json
dibbla workflows execute my-workflow --node api_node_1

# URL and API docs
dibbla workflows url my-workflow
dibbla workflows api-docs my-workflow
dibbla workflows api-docs my-workflow -o json
```

---

## Nodes

```bash
# Add a node from a file
dibbla nodes add my-workflow -f node.yaml

# Add a node inline
dibbla nodes add my-workflow --inline '{"id":"transform","type":"code","code":"return input"}'

# Remove a node
dibbla nodes remove my-workflow transform
dibbla nodes remove my-workflow transform --yes
```

---

## Edges

```bash
# Add an edge between nodes
dibbla edges add my-workflow "input.output -> transform.input"

# Remove an edge
dibbla edges remove my-workflow "input.output -> transform.input"

# List all edges
dibbla edges list my-workflow
dibbla edges list my-workflow -o json
```

---

## Inputs

```bash
# Set a node input value
dibbla inputs set my-workflow agent1 system_prompt "You are a helpful assistant"
dibbla inputs set my-workflow agent1 temperature 0.7

# Set to null
dibbla inputs set my-workflow agent1 max_tokens ignored --null
```

---

## Tools

```bash
# Add a tool to an agent node
dibbla tools add my-workflow agent1 web_search
dibbla tools add my-workflow agent1 calculator

# Remove a tool
dibbla tools remove my-workflow agent1 web_search
```

---

## Revisions

```bash
# List revisions
dibbla revisions list my-workflow
dibbla rev list my-workflow           # alias
dibbla revisions list my-workflow -o json

# Create a snapshot
dibbla revisions create my-workflow
dibbla revisions create my-workflow -q   # prints only the revision ID

# Restore
dibbla revisions restore my-workflow abc123
```

---

## Functions

```bash
# List available functions
dibbla functions list
dibbla fn list                        # alias
dibbla functions list --server my-server
dibbla functions list --tag search
dibbla functions list -o json

# Get function details
dibbla functions get my-server web_search
dibbla functions get my-server web_search -o json
```

---

## Scripting tips

- Use `-y` / `--yes` to skip confirmations: `apps delete`, `db delete`, `secrets delete`, `workflows delete`, `nodes remove`.
- Use `-q` / `--quiet` on `db list`, `db delete`, `db connect`, and workflow commands for minimal output.
- Use `-o json` on workflow commands for machine-readable output.
- Pipe `secrets get` into env or other commands; use `db list -q` for name-only loops.
- `revisions create -q` prints only the revision ID for scripting.

```bash
# Save a revision and capture the ID
REV=$(dibbla revisions create my-workflow -q)
echo "Created revision: $REV"

# Export a secret
export API_KEY=$(dibbla secrets get API_KEY -d myapp)

# Loop over databases
for db in $(dibbla db list -q); do echo "$db"; done

# Validate before deploying a workflow
dibbla workflows validate -f workflow.yaml && dibbla workflows update my-workflow -f workflow.yaml
```

---

## Agent workflows

Step-by-step patterns for AI agents deploying and managing apps non-interactively.

### Pre-deploy guardrails workflow

```bash
# 1. Code is ready — run guardrails checks by reviewing the source code
#    Check for: security issues, database anti-patterns, unsafe API calls, external write safety
#    See guardrails.md for the full checklist

# 2. Present the guardrails report to the user (example):
#
#    ## Pre-deploy guardrails report
#    - [x] Security (OWASP Top 10): OK
#    - [x] Database usage: OK
#    - [x] REST/API calls: 1 warning
#      - WARNING: No retry logic on payment API call in src/services/payment.js:23
#    - [x] External writes: OK
#    **Result: PASSED with 1 warning**
#
#    Ask: "There is 1 warning. Should I fix this before deploying, or proceed as-is?"

# 3. Wait for user confirmation before deploying or fixing anything

# 4. Deploy only after explicit user approval
dibbla deploy . --alias my-app --update
```

### Deploy a new app (first time)

```bash
# 0. The directory must contain a Dockerfile — dibbla does NOT autodetect
#    languages or run buildpacks. Minimal example:
#
#    FROM node:20-alpine AS build
#    WORKDIR /app
#    COPY package*.json ./
#    RUN npm ci --omit=dev
#    COPY . .
#    EXPOSE 3000
#    CMD ["node", "server.js"]
#
# 1. Check if the app already exists
dibbla apps list

# 2. If NOT listed, deploy with all required env vars in one command
dibbla deploy . --alias my-app \
  -e DATABASE_URL="postgres://user:pass@host:5432/db" \
  -e API_KEY="sk-xxx" \
  -e NODE_ENV=production \
  -e PORT=3000

# 3. Verify it's running
dibbla apps list
```

### Update an existing app (zero downtime)

```bash
# Rolling update — re-deploys the code, keeps existing env vars
dibbla deploy . --alias my-app --update

# To change env vars without redeploying code
dibbla apps update my-app -e LOG_LEVEL=debug -e NEW_VAR=value
```

### Deploy-or-update pattern

```bash
# Check if app exists, then deploy or update accordingly
if dibbla apps list 2>/dev/null | grep -q "my-app"; then
  dibbla deploy . --alias my-app --update
else
  dibbla deploy . --alias my-app \
    -e DATABASE_URL="postgres://..." \
    -e NODE_ENV=production
fi
```

### Full setup: app + database + secrets

```bash
# 1. Create the database (scoped to the deployment).
#    When --deployment is set, the auto-created secret is named
#    DATABASE_URL_<UPPERCASED_NAME>, e.g. DATABASE_URL_MY_APP_DB here.
#    Without --deployment it is named DATABASE_URL.
dibbla db create my_app_db --deployment my-app

# 2. Set additional secrets
dibbla secrets set API_KEY "sk-xxx"

# 3. Deploy with env vars
dibbla deploy . --alias my-app \
  -e API_KEY="sk-xxx" \
  -e NODE_ENV=production

# 4. Verify
dibbla apps list
dibbla secrets list -d my-app

# Alternative: get the connection string directly
export DATABASE_URL=$(dibbla db connect my-app-db -q)
```

### Tear down an app

```bash
# Always use --yes to avoid interactive prompt
dibbla apps delete my-app --yes
dibbla db delete my-app-db --yes
dibbla secrets delete API_KEY --yes
```

### Install a template and start iterating

```bash
# 1. Show available templates to the user
dibbla template list

# 2. Install the one they picked (default destination — ./<template_path>)
dibbla template install expense-reporter

# 3. The bootstrap yaml does the rest automatically:
#    - tool checks (git, node, go, dibbla)
#    - dibbla self-update (auto-installs latest dibbla)
#    - clones the template project into CWD
#    - dibbla login via env-var (DIBBLA_AUTH_SERVICE_URL picked up from parent)
#    - npm install, go build, start dev servers (ports per template)
#    - opens the app in the default browser
#
#    End state: a live local project the user can edit.

# 4. Iteration: re-run the inner dibbla-task.yaml any time
cd expense-reporter-template-1
dibbla run
# idempotent — existing installs skip, stale dev servers are reclaimed
```

### Run a bootstrap yaml directly (without dibbla template install)

```bash
# Same end state, one command:
mkdir my-app && cd my-app
dibbla run https://raw.githubusercontent.com/dibbla-agents/dibbla-public-templates/master/getting-started.dibbla-task.yaml
```
