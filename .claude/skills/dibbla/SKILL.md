---
name: dibbla
description: Use the Dibbla CLI to deploy apps, manage applications, databases, secrets, and workflows on the Dibbla platform. Use when the user wants to deploy, list/update/delete apps, create/list/delete/dump/restore databases, manage secrets, or manage workflows (create/execute/validate workflows, manage nodes/edges/inputs/tools, revisions, and browse functions).
---

# Dibbla CLI

The `dibbla` CLI scaffolds projects and manages **applications**, **databases**, **secrets**, and **workflows** on the Dibbla platform. Deployed apps are available at `https://<alias>.dibbla.com`.

## Commands at a glance

| Area       | Commands |
|------------|----------|
| Deploy     | `deploy [path] [--alias name]` — deploy from directory |
| Apps       | `apps list`, `apps update <alias>`, `apps delete <alias>` |
| Db         | `db list`, `db create`, `db delete`, `db dump`, `db restore` |
| Secrets    | `secrets list`, `secrets set`, `secrets get`, `secrets delete` (global or `-d <alias>`) |
| Workflows  | `workflows list`, `get`, `create`, `update`, `delete`, `validate`, `execute`, `url`, `api-docs` |
| Nodes      | `nodes add <wf>`, `nodes remove <wf> <id>` |
| Edges      | `edges add <wf> "<edge>"`, `edges remove`, `edges list` |
| Inputs     | `inputs set <wf> <node> <input> <value>` |
| Tools      | `tools add <wf> <agent> <tool>`, `tools remove` |
| Revisions  | `revisions list <wf>`, `revisions create`, `revisions restore` |
| Functions  | `functions list`, `functions get <server> <name>` |

## Additional resources

- **Full command and flag reference:** see [reference.md](reference.md) for usage, arguments, and all flags.
- **Usage examples:** see [examples.md](examples.md) for copy-paste examples and scripting patterns.

When suggesting or generating `dibbla` commands, use the reference for exact syntax and the examples for typical workflows.
