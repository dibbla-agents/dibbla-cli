# Dibbla Skills

Agent skills for the [Dibbla](https://dibbla.com) platform. These skills teach AI coding agents (Claude Code, Cursor, Codex, etc.) how to use the Dibbla CLI to deploy apps, manage databases, secrets, and workflows.

## Install

### Claude Code

```bash
npx skills add dibbla-agents/skills --skill dibbla-cli -a claude-code -y
```

### All agents

```bash
npx skills add dibbla-agents/skills
```

### Manual

Copy the `dibbla-cli/` folder into your project's `.claude/skills/` directory.

## What's included

### dibbla-cli

The **dibbla-cli** skill gives your AI agent full knowledge of the `dibbla` CLI:

- Deploying and managing applications
- Multi-service deployments via `dibbla.yaml` (web + worker + redis style apps), with init containers, healthchecks, cron jobs, build-time secrets, custom domains, multiple public URLs (hyphenated subdomain scheme), per-service auth (env-aware require_login + access_policy), per-service secret scoping, and shell `${VAR}` substitution at deploy time
- Per-service operations: `dibbla apps restart --service`, `dibbla logs --service` (Loki) / `--pod-stream` (K8s API), `dibbla secrets --service`
- Manifest validation (`dibbla manifest validate`) and server-authoritative preview (`dibbla preview`)
- Creating and connecting to managed databases
- Managing secrets (global, per-deployment, or per-service)
- Building and executing workflows, nodes, edges, and revisions
- Scripting patterns and agent-friendly flags (`--yes`, `--quiet`)

## Links

- [Dibbla Platform](https://dibbla.com)
- [Dibbla CLI Installation](https://install.dibbla.com)
- [Get an API Token](https://app.dibbla.com/api-keys)

---

> This repo is automatically synced from the Dibbla CLI source. Do not edit directly.
