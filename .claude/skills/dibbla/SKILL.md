---
name: dibbla
description: Use the Dibbla CLI to deploy apps, manage applications, databases, and secrets on the Dibbla platform. Use when the user wants to deploy, list/update/delete apps, create/list/delete/dump/restore databases, or manage secrets (global or per-deployment).
---

# Dibbla CLI

The `dibbla` CLI scaffolds projects and manages **applications**, **databases**, and **secrets** on the Dibbla platform. Deployed apps are available at `https://<alias>.dibbla.app`.

## Commands at a glance

| Area    | Commands |
|--------|----------|
| Deploy | `deploy [path] [--alias name]` — deploy from directory |
| Apps   | `apps list`, `apps update <alias>`, `apps delete <alias>` |
| Db     | `db list`, `db create`, `db delete`, `db dump`, `db restore` |
| Secrets| `secrets list`, `secrets set`, `secrets get`, `secrets delete` (global or `-d <alias>`) |

## Additional resources

- **Full command and flag reference:** see [reference.md](reference.md) for usage, arguments, and all flags.
- **Usage examples:** see [examples.md](examples.md) for copy-paste examples and scripting patterns.

When suggesting or generating `dibbla` commands, use the reference for exact syntax and the examples for typical workflows.
