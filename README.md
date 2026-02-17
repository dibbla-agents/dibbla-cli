# Dibbla CLI

A command-line tool to scaffold and manage Dibbla worker projects.

## Installation

### Homebrew (macOS / Linux)

```bash
brew install dibbla-agents/dibbla/dibbla
```

### macOS / Linux (curl)

```bash
curl -fsSL https://raw.githubusercontent.com/dibbla-agents/dibbla-cli/main/install.sh | sh
```

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/dibbla-agents/dibbla-cli/main/install.ps1 | iex
```

### Go developers

```bash
go install github.com/dibbla-agents/dibbla-cli/cmd/dibbla@latest
```

> **Note:** Make sure `$(go env GOPATH)/bin` is in your `PATH`.

### Manual download

Download the latest binary for your platform from [GitHub Releases](https://github.com/dibbla-agents/dibbla-cli/releases).

## Usage

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
dibbla deploy --force
```

### Manage Applications

```bash
dibbla apps list
dibbla apps delete my-app
```

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
├── cmd/dibbla/
│   └── main.go              # Entry point
├── internal/
│   ├── cmd/
│   │   ├── root.go          # Root command + version
│   │   ├── skill.md         # Embedded for --skill-prompt (synced from SKILL.md)
│   │   ├── create.go        # Create commands
│   │   ├── deploy.go        # Deploy command
│   │   ├── apps.go          # Apps management
│   │   ├── db.go            # Database management (list, create, delete, restore, dump)
│   │   └── secrets.go       # Secrets management (list, set, get, delete)
│   ├── create/
│   │   └── goworker.go      # Go worker generator logic
│   ├── db/
│   │   └── db.go            # Database API client
│   ├── deploy/
│   │   └── deploy.go        # Deploy API client + archive build
│   ├── apps/
│   │   └── apps.go          # Apps (deployments) API client
│   ├── config/
│   │   └── config.go        # CLI config (env, .env)
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
