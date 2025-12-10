# Dibbla CLI

A command-line tool to scaffold Dibbla worker projects.

## Installation

### Go developers

```bash
go install github.com/dibbla-agents/dibbla-cli/cmd/dibbla@latest
```

### Pre-built binaries

Download from [GitHub Releases](https://github.com/dibbla-agents/dibbla-cli/releases).

## Usage

### Create a Go Worker Project

```bash
dibbla create go-worker my-worker
```

Or run without arguments for interactive mode:

```bash
dibbla create go-worker
```

### Prompts

| Prompt | Required | Default |
|--------|----------|---------|
| Project name | Yes (if not provided as arg) | - |
| API Token | No | Placeholder in .env |
| Include frontend | No | No |

### Example Session

```
$ dibbla create go-worker
🚀 Dibbla Go Worker Generator

Checking prerequisites...
  ✅ Go: go1.23.4

? Project name: my-worker
? API Token (from app.dibbla.com/settings/api-keys): ak_xxxxx
? Include frontend? No

Creating project...
  Cloning template...
  Configuring module path...
  Creating .env...
  Removing frontend (not selected)...
  Cleaning up...
  Running go mod tidy...

🎉 Ready! Run your worker:
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

## Project Structure

```
dibbla-cli/
├── cmd/dibbla/
│   └── main.go              # Entry point
├── internal/
│   ├── cmd/
│   │   ├── root.go          # Root command
│   │   └── create.go        # Create commands
│   ├── create/
│   │   └── goworker.go      # Go worker generator logic
│   ├── preflight/
│   │   └── checks.go        # Pre-flight checks
│   └── prompt/
│       └── prompt.go        # Interactive prompts
└── go.mod
```

## Future Commands

- `dibbla create python-worker` - Python worker scaffold
- `dibbla create node-worker` - Node.js worker scaffold
- `dibbla doctor` - Diagnose common issues

