# Dibbla CLI — Command reference

Complete usage, arguments, and flags for `deploy`, `apps`, `db`, and `secrets`.

---

## deploy

Deploy a containerized app from a directory. App URL: `https://<alias>.dibbla.app`.

| Item | Details |
|------|---------|
| **Usage** | `dibbla deploy [path]` |
| **Arguments** | `path` (optional) — directory to deploy; default `.` |
| **Flags** | `--alias`, `-a` — custom alias name (default: directory name) |
|| | `--force`, `-f` — force redeploy if alias exists |
| | `--env`, `-e` — env var `KEY=value` (repeatable) |
| | `--cpu` — CPU request (e.g. `500m`) |
| | `--memory` — Memory request (e.g. `512Mi`) |
| | `--port` — Container port (e.g. `3000`) |

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
| **Rule** | At least one of: `--env`, `--replicas`, `--cpu`, `--memory`, `--port` required |

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
| Secrets | `dibbla secrets list [-d alias]` | List global or app secrets |
| Secrets | `dibbla secrets set <name> [value] [-d alias]` | Create/update secret |
| Secrets | `dibbla secrets get <name> [-d alias]` | Print secret value |
| Secrets | `dibbla secrets delete <name> [-d alias]` | Delete secret |
