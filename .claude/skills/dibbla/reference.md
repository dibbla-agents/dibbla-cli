# Dibbla CLI — Command reference

Complete usage, arguments, and flags for all commands.

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
| **Rule** | At least one of: `--env`, `--replicas`, `--cpu`, `--memory`, `--port`, `--favicon` required |

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

## Summary table

| Area | Command | Purpose |
|------|---------|---------|
| Deploy | `dibbla deploy [path]` | Deploy app from directory |
| Apps | `dibbla apps list` | List deployments |
| Apps | `dibbla apps update <alias> ...` | Update env, replicas, cpu, memory, port |
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
