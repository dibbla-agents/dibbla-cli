# Dibbla CLI

A command-line tool to scaffold and manage Dibbla worker projects.

## Installation

### Homebrew (macOS / Linux)

```bash
brew install dibbla-agents/dibbla/dibbla
```

### macOS / Linux (curl)

```bash
curl https://install.dibbla.com -fsS | sh
```

### Windows (PowerShell)

```powershell
irm https://install.dibbla.com/install.ps1 | iex
```

### Go developers

```bash
go install github.com/dibbla-agents/dibbla-cli/cmd/dibbla@latest
```

> **Note:** Make sure `$(go env GOPATH)/bin` is in your `PATH`.

### Manual download

Download the latest binary for your platform from [GitHub Releases](https://github.com/dibbla-agents/dibbla-cli/releases).

## Usage

### First-time setup

After installing dibbla, run:

```bash
dibbla init
```

This is a one-shot wizard that runs three steps in order, each as a child of the running binary so each step picks up the current code:

1. `dibbla update --yes` — make sure you're on the latest release.
2. `dibbla login` — store your API token in the OS keyring (skipped if a token is already configured; force with `--re-login`).
3. `dibbla skills install dibbla` — install the AI-agent skill into the current project (or `$HOME` with `--user`).

Failure policy: `update` and `skill install` warn and continue; `login` is a hard stop because everything else needs auth. The command is safe to re-run — each step detects "already done."

| Flag | Description |
|------|-------------|
| `-y`, `--yes` | Skip prompts where possible (forwarded to `update`) |
| `--skip-update` | Skip the update step |
| `--skip-skill` | Skip installing the skill |
| `--user` | Install the skill into `$HOME` instead of the current project |
| `--re-login` | Run `login` even if a token is already configured |
| `--api-url <url>` | API endpoint forwarded to `login` |

To pre-set the token without an interactive prompt, export `DIBBLA_API_TOKEN=<token>` before running `dibbla init`. Don't pass tokens via flag — they end up in `ps` output.

### Authentication

For local use, log in once; your API token is stored securely in the OS credential store (e.g. macOS Keychain):

```bash
dibbla login                    # default: https://api.dibbla.com (prompts for token)
dibbla login --api-key TOKEN    # pass token on the command line
dibbla login api.dibbla.net     # use a different API endpoint
dibbla logout                   # remove stored credentials
```

In CI, set environment variables instead of using `login`:

- `DIBBLA_API_TOKEN` (required for API commands)
- `DIBBLA_API_URL` (optional; default is `https://api.dibbla.com`)

Get your API token at [app.dibbla.com/api-keys](https://app.dibbla.com/api-keys).

### Update notifications

On interactive terminals, `dibbla` checks for new releases in the background at most once every 24 hours. The check is non-blocking, so fast commands like `--help` and `--version` return immediately.

If the update request fails (for example due to network issues or blocked GitHub access), the check timestamp is still refreshed to avoid repeated slow retries on every invocation.

Set `DIBBLA_NO_UPDATE_NOTIFIER=1` to disable update notifications.

### Self-update

When the notifier reports a newer version, run `dibbla update` to upgrade in place:

```bash
dibbla update                  # latest, with confirmation
dibbla update --check          # only report drift; non-zero exit if behind
dibbla update --version v1.4.2 # pin / downgrade to a specific tag
dibbla update --yes            # skip the confirmation prompt
```

`dibbla update` detects how the binary was installed:

- **Homebrew / apt / rpm / scoop / choco**: prints the right upgrade command for your package manager (`brew upgrade dibbla`, etc.). It does not run the command itself, so there's no implicit `sudo`.
- **Script install** (from `install.dibbla.com`, lands in `~/.local/bin` or `%LOCALAPPDATA%\dibbla`): downloads the matching release archive, verifies its SHA-256 against `checksums.txt`, and atomically replaces the binary.
- **`go install` / development builds (`Version == "dev"`)**: refuses to self-replace; rebuild from source instead.

Re-running the `curl … | sh` (or `irm … | iex`) installer also picks up `dibbla update` automatically: if a working dibbla is already on `PATH` and recognizes the `update` subcommand, the installer delegates to it instead of overwriting the binary in place. That way running the installer on a Homebrew or apt install prints the right `brew upgrade` / `apt-get install --only-upgrade` command rather than silently replacing the package-manager copy.

To skip the delegation and reinstall from scratch — useful when the existing `dibbla update` is broken or when installing into a different `DIBBLA_INSTALL_DIR` — set `DIBBLA_INSTALLER_FORCE=1`. Note that the variable has to be set on the **sh** side of the pipe (env vars don't cross `|`):

```bash
# Right — env var reaches sh
curl -fsS https://install.dibbla.com | DIBBLA_INSTALLER_FORCE=1 sh

# Wrong — env var is set for curl, sh never sees it
DIBBLA_INSTALLER_FORCE=1 curl -fsS https://install.dibbla.com | sh
```

On Windows it's simpler since there's no pipe-to-shell idiom: `$env:DIBBLA_INSTALLER_FORCE=1; irm https://install.dibbla.com/install.ps1 | iex`.

### Create a Go Worker Project

```bash
dibbla create go-worker my-worker
```

Or run without arguments for interactive mode:

```bash
dibbla create go-worker
```

### Deploy an Application

```bash
dibbla deploy
dibbla deploy ./myapp
dibbla deploy --alias my-api       # Custom alias (default: directory name)
dibbla deploy --force
dibbla deploy --cpu 500m --memory 512Mi --port 3000
dibbla deploy -e NODE_ENV=production -e LOG_LEVEL=info
```

#### Deploy a multi-service app (`dibbla.yaml`)

Bundle multiple containers into one alias by adding a `dibbla.yaml` at the deploy root. Detection is automatic: present ⇒ multi-service path; absent ⇒ legacy single-`Dockerfile` path. Min example:

```yaml
# dibbla.yaml
version: 1
services:
  web:
    build: ./web
    port: 3000
    public: true
    environment:
      REDIS_URL: ${DIBBLA_SVC_REDIS_URL}     # service-discovery substitution
  worker:
    build: ./worker
  redis:
    image: redis:7
    port: 6379
```

```bash
dibbla deploy --alias myapp -m "feat: ship multi-service"
dibbla deploy --alias myapp --target-env staging --profile mailcatcher -m "deploy: staging"
dibbla deploy --alias daily --no-public -m "feat: cron-only deploy"
```

The whole graph is built and applied atomically (rollback-on-failure). For env-aware fields, profiles, init containers, healthchecks, custom domains, cron jobs, multiple public services, per-service auth, build-time secrets, shell variable substitution, and the runtime contract for service discovery + NetworkPolicy, see [`.claude/skills/dibbla/manifest.md`](.claude/skills/dibbla/manifest.md).

**Multiple public URLs.** Two services with `public: true` get one URL each — the lex-first one at `https://<alias>.dibbla.com` (bare alias for backcompat); subsequent ones at `https://<alias>-<service>.dibbla.com`. Per-service auth (`auth.require_login`, `auth.access_policy`) is env-aware so `pgadmin` can be open in dev and locked down in prod with one manifest.

**Shell variable substitution.** Compose-style `${VAR}` and `${VAR:-default}` placeholders in `dibbla.yaml` are resolved from your shell env when `dibbla deploy` runs. `DIBBLA_*` is reserved for server-side discovery vars and passes through unchanged.

#### Validate and preview before deploying

```bash
dibbla manifest validate                        # local schema check (no network)
dibbla manifest validate ./myapp --json         # CI / pre-commit hook friendly
dibbla preview --target-env prod                # server-authoritative dry run
dibbla preview --profile mailcatcher --json     # raw PreviewResponse for jq
```

`manifest validate` covers parse + schema only. `preview` resolves env-aware fields, applies profiles, and runs the org quota check — server-side, no build, no apply.

#### Operate a multi-service app

```bash
dibbla apps restart myapp --service worker     # rolling restart of one service
dibbla logs myapp --service web -f              # filter Loki to one service
dibbla logs myapp --service web --pod-stream -f # K8s-direct stream when Loki isn't set up
dibbla secrets set NPM_TOKEN xxx -d myapp -s web  # per-service secret (only web sees it)
dibbla secrets list -d myapp                     # deployment-wide entries (service_name='')
```

### Manage Applications

```bash
dibbla apps list
dibbla apps update my-app -e NODE_ENV=production --replicas 2
dibbla apps update my-app --cpu 500m --memory 512Mi --port 3000
dibbla apps delete my-app
```

### View Logs

```bash
dibbla logs my-app                            # Last 15 minutes (default), then exit
dibbla logs my-app --since 24h                # Last 24 hours
dibbla logs my-app --since 10m -f             # Backfill 10 min, then stream new lines
dibbla logs my-app -n 200                     # Last 200 lines
dibbla logs my-app --grep "timeout"           # Server-side regex filter
dibbla logs my-app --json | jq .              # Raw NDJSON for tooling
```

| Flag | Description |
|------|-------------|
| `--since <duration>` | Window to fetch (Go duration; default `15m`, server cap `24h`) |
| `-f`, `--follow` | Stream new log lines as they arrive |
| `-n`, `--tail <N>` | Show only the last N lines (instead of the `--since` window) |
| `--grep <regex>` | Server-side regex line filter |
| `--limit <N>` | Cap lines fetched in range mode |
| `--json` | Emit raw NDJSON instead of the formatted human output |
| `--no-color` | Disable color (auto-disabled when stdout isn't a TTY) |

### Manage Databases

```bash
dibbla db list
dibbla db list -q              # names only, one per line (for scripting)
dibbla db create mydb
dibbla db create --name mydb
dibbla db delete mydb
dibbla db delete mydb --yes
dibbla db delete mydb --yes -q # quiet: no progress or success output
dibbla db restore mydb --file backup.dump
dibbla db dump mydb
dibbla db dump mydb --output mydb.dump
```

| Command | Description |
|---------|-------------|
| `db list` | List all managed databases (`-q`: names only, one per line) |
| `db create [name]` | Create a new database (name via argument or `--name`) |
| `db delete <name>` | Delete a database (`-y` skip confirmation, `-q` quiet output) |
| `db restore <name> -f <file>` | Restore from a dump file (e.g. pg_dump custom format) |
| `db dump <name> [-o file]` | Download a database dump (default: `<name>.dump`) |

### Manage Secrets

Secrets can be global or scoped to a deployment. Omit `--deployment` for global secrets.

```bash
dibbla secrets list
dibbla secrets list --deployment myapp
dibbla secrets set API_KEY "my-secret-value"
echo "secret" | dibbla secrets set API_KEY
dibbla secrets set API_KEY "value" --deployment myapp
dibbla secrets get API_KEY
dibbla secrets get API_KEY --deployment myapp
dibbla secrets delete API_KEY
dibbla secrets delete API_KEY --deployment myapp --yes
```

| Command | Description |
|---------|-------------|
| `secrets list [-d deployment]` | List secrets (global or for one deployment) |
| `secrets set <name> [value] [-d deployment]` | Create or update a secret (value from arg or stdin) |
| `secrets get <name> [-d deployment]` | Print a secret's value |
| `secrets delete <name> [-d deployment]` | Delete a secret (`-y` to skip confirmation) |

### Prompts

| Prompt | Required | Default |
|--------|----------|---------|
| Project name | Yes (if not provided as arg) | - |
| Hosting type | Yes | Dibbla Cloud |
| API Token | No | Placeholder in .env |
| Include frontend | No | No |

### Example Session

```
$ dibbla create go-worker
>> Dibbla Go Worker Generator

Checking prerequisites...
  [OK] Go: go1.23.4

? Project name: my-worker
? Hosting type: Dibbla Cloud
? API Token (from app.dibbla.com/settings/api-keys): ****
? Include frontend? No

Creating project...
  Cloning template...
  Configuring module path...
  Creating .env...
  Removing frontend (not selected)...
  Cleaning up...
  Running go mod tidy...

[*] Ready! Run your worker:
   cd my-worker
   go run ./cmd/worker
```

## Development

```bash
# Run locally
go run ./cmd/dibbla create go-worker test-project

# Build
go build -o dibbla ./cmd/dibbla

# Test
go test ./...
```

### Releasing

Releases are automated via GitHub Actions. To publish a new version:

```bash
git tag v1.0.0
git push origin v1.0.0
```

This triggers GoReleaser to build binaries for all platforms and create a GitHub Release.

## Project Structure

```
dibbla-cli/
├── .claude/skills/dibbla/   # Claude Code skill (reference + examples for LLMs)
│   ├── SKILL.md             # Skill entrypoint
│   ├── reference.md         # Full command/flag reference
│   └── examples.md          # Copy-paste examples
├── cmd/dibbla/
│   └── main.go              # Entry point
├── internal/
│   ├── cmd/
│   │   ├── root.go          # Root command + version
│   │   ├── login.go         # Login command (store API token in OS keychain)
│   │   ├── logout.go        # Logout command (remove stored credentials)
│   │   ├── skill.md         # Embedded for --skill-prompt (synced from SKILL.md)
│   │   ├── create.go        # Create commands
│   │   ├── deploy/          # Deploy-related commands
│   │   │   ├── register.go  # Command registration + requireToken
│   │   │   ├── deploycmd.go # Deploy command
│   │   │   ├── apps.go      # Apps management
│   │   │   ├── db.go        # Database management (list, create, delete, restore, dump)
│   │   │   └── secrets.go   # Secrets management (list, set, get, delete)
│   │   ├── logs/            # Per-app log streaming command (`dibbla logs <app>`)
│   │   └── wf/              # Workflow commands
│   ├── apiclient/
│   │   └── client.go        # HTTP API client + token validation
│   ├── config/
│   │   └── config.go        # CLI config (env, .env, keychain)
│   ├── credential/
│   │   └── store.go         # OS credential store (keyring)
│   ├── create/
│   │   └── goworker.go      # Go worker generator logic
│   ├── db/
│   │   └── db.go            # Database API client
│   ├── deploy/
│   │   └── deploy.go        # Deploy API client + archive build
│   ├── apps/
│   │   └── apps.go          # Apps (deployments) API client
│   ├── applogs/
│   │   └── applogs.go       # Streaming client for the per-app /logs endpoint
│   ├── secrets/
│   │   └── secrets.go       # Secrets API client
│   ├── platform/
│   │   └── platform.go      # Cross-platform helpers (icons, exec)
│   ├── preflight/
│   │   └── checks.go        # Pre-flight checks
│   └── prompt/
│       └── prompt.go        # Interactive prompts
├── install.sh               # macOS/Linux installer
├── install.ps1              # Windows installer
├── .goreleaser.yml          # Cross-platform build config
└── go.mod
```
