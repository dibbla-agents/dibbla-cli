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
- Use `--quiet` / `-q` on `db list`, `db delete`, `db connect` for machine-readable output in scripts.
- `db create --deployment <alias>` scopes the database and its auto-created `DATABASE_URL` secret to a specific deployment.
- `db connect` prints a psql-compatible connection string via the Dibbla database proxy. Use `-q` for scripting: `psql $(dibbla db connect mydb -q)`.

## Additional resources

- **Full command and flag reference:** see [reference.md](reference.md) for usage, arguments, and all flags.
- **Usage examples:** see [examples.md](examples.md) for copy-paste examples and scripting patterns.

When suggesting or generating `dibbla` commands, use the reference for exact syntax and the examples for typical workflows.
