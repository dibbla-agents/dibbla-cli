---
name: dibbla-ai-gateway
description: Configure local AI coding assistants (Claude Code, opencode, Cursor, Cline, Windsurf, Zed) to send their LLM calls through the Dibbla AI gateway, so every prompt and response is captured under the user's Dibbla org and billed to a single platform-managed provider key. Also covers direct API calls (curl, custom scripts) with the optional `X-Dibbla-App` attribution header.
when_to_use: Trigger when the user wants to point an AI coding assistant or any HTTP-level OpenAI/Anthropic client at the Dibbla AI gateway from a developer laptop or other interactive context (NOT a deployed Dibbla app — for that, see the `dibbla` skill's ai-gateway.md). Specifically use this when the user mentions Claude Code's `ANTHROPIC_BASE_URL`, Cursor's "Override OpenAI Base URL", opencode `providers` config, Cline / Windsurf / Zed custom-provider settings, or asks "how do I make my code assistant log calls to the Dibbla console?". Also use it when the user asks for `dibbla ai url`, `dibbla ai env`, or `dibbla ai test`. Skip if the question is about an app deployed via `dibbla deploy` calling an LLM at runtime — that's the deployed-pod path with `DIBBLA_AI_GATEWAY_URL` already injected.
---

# Dibbla AI gateway — for AI coding assistants and direct API calls

## What this is, in plain words

The Dibbla AI gateway is a **proxy** that sits in front of OpenAI and Anthropic. You point your code assistant (or any OpenAI/Anthropic-compatible client) at the gateway instead of straight at OpenAI/Anthropic. You authenticate with your **Dibbla API token**, not a provider key.

In return:

- **One key for everything.** No need to give a developer or a tool the actual OpenAI / Anthropic key. The gateway holds the platform-managed provider key and swaps it in on the way out.
- **Every call is logged.** Every prompt, response, token count, latency, and tool call lands in `ai_gateway_db` for your Dibbla org and is browsable at `https://ai.dibbla.net/console`. You can see exactly what your assistant asked the model and what came back.
- **Per-user attribution.** The token identifies the user, so the ledger always shows whose call it was.

This skill is for **interactive use from a laptop** — IDEs, terminal assistants, ad-hoc curl. For LLM calls coming from a Dibbla-deployed app (`dibbla deploy`), the `DIBBLA_AI_GATEWAY_URL` env var is already injected into the pod — see the `dibbla` skill's `ai-gateway.md` for that path.

## The two-line setup

The fastest way is to use the helpers built into the Dibbla CLI:

```bash
dibbla ai url           # prints e.g. https://ai.dibbla.net
eval $(dibbla ai env)   # exports ANTHROPIC_BASE_URL, OPENAI_BASE_URL, *_API_KEY
dibbla ai test          # /health + token validation
```

After `eval $(dibbla ai env)` in a shell, any tool in that shell that respects `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` (most of them) routes through the gateway with no extra config. That includes Claude Code, opencode in Anthropic mode, the official `openai` / `anthropic` SDKs, `aichat`, and so on.

Resolution:

1. The CLI uses `DIBBLA_AI_GATEWAY_URL` if set.
2. Otherwise it derives from the active Dibbla API URL (`api.X` → `ai.X`).
3. If neither works (e.g. `localhost`), it tells you to set `DIBBLA_AI_GATEWAY_URL` explicitly.

If the user hasn't logged in yet, point them at `dibbla login` first — the env block needs a token to populate `ANTHROPIC_API_KEY` / `OPENAI_API_KEY`.

## Per-tool setup

The exact key names vary, so here's the cheatsheet. In every case, the **API key is the user's Dibbla API token** (mint one at `https://app.dibbla.com/api-keys` or use `dibbla login`).

> Whenever an example uses `https://ai.dibbla.net`, substitute `$(dibbla ai url)` so it stays correct across dev/staging/prod.

### Claude Code (Anthropic CLI)

Two env vars, persistable globally:

```bash
export ANTHROPIC_BASE_URL=$(dibbla ai url)/anthropic
export ANTHROPIC_API_KEY=<your dibbla token>
```

Persistent (so it survives a new shell):

```bash
claude config set -g env.ANTHROPIC_BASE_URL "$(dibbla ai url)/anthropic"
claude config set -g env.ANTHROPIC_API_KEY  "$DIBBLA_API_TOKEN"
```

Note: `ANTHROPIC_BASE_URL` does **not** include the `/v1` suffix. The Anthropic SDK appends `/v1/messages` itself.

### opencode

`opencode` supports two routes:

**Anthropic mode** (simplest for Claude models):

```bash
export ANTHROPIC_BASE_URL=$(dibbla ai url)/anthropic
export ANTHROPIC_API_KEY=<your dibbla token>
```

**OpenAI-compatible custom provider** (works for both Claude- and GPT-style routing through the gateway). Add to `~/.config/opencode/opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "dibbla": {
      "npm": "@ai-sdk/openai-compatible",
      "options": {
        "baseURL": "https://ai.dibbla.net/openai/v1",
        "apiKey": "{env:DIBBLA_API_TOKEN}"
      },
      "models": {
        "gpt-4o-mini": { "name": "GPT-4o mini (via Dibbla)" },
        "gpt-4o":      { "name": "GPT-4o (via Dibbla)" }
      }
    }
  }
}
```

### Cursor

Settings → **Models** → **Override OpenAI Base URL**:

- URL: `https://ai.dibbla.net/openai/v1`  *(must include `/v1`)*
- OpenAI API Key: your Dibbla token
- Click **Verify** — Cursor pings the URL.

Caveat: as of 2026 Cursor does **not** expose an Anthropic base URL override. Cursor + Dibbla gateway = OpenAI-compatible models only. Anthropic models still go straight to Anthropic. If the user wants every Cursor call audited, they need a tool that supports Anthropic base URLs (Claude Code, opencode, Zed).

### Cline (VS Code extension)

Sidebar → ⚙ → **API Provider** → **OpenAI Compatible**:

- Base URL: `https://ai.dibbla.net/openai/v1`
- API Key: your Dibbla token
- Model ID: `gpt-4o-mini` (or whichever)

Or in `settings.json`:

```json
"cline.openAiCompatible.baseUrl": "https://ai.dibbla.net/openai/v1",
"cline.openAiCompatible.apiKey":  "<your dibbla token>"
```

### Windsurf

Settings → **AI / Models** → **Custom Model Provider**:

- Endpoint URL: `https://ai.dibbla.net/openai/v1`
- API Key: your Dibbla token
- Model name: e.g. `gpt-4o-mini`

OpenAI-compatible only, same caveat as Cursor for Anthropic models.

### Zed

`~/.config/zed/settings.json`:

```json
{
  "language_models": {
    "openai": {
      "api_url": "https://ai.dibbla.net/openai/v1",
      "available_models": [
        { "name": "gpt-4o-mini", "max_tokens": 128000 }
      ]
    },
    "anthropic": {
      "api_url": "https://ai.dibbla.net/anthropic",
      "available_models": [
        { "name": "claude-sonnet-4-6", "max_tokens": 200000 }
      ]
    }
  }
}
```

API keys are pulled from `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` in Zed's environment — so `eval $(dibbla ai env)` before launching Zed sets both.

### Continue, Aider, Roo, …

Most other assistants accept an OpenAI-compatible base URL plus key. The pattern is always:

- base URL → `$(dibbla ai url)/openai/v1`
- API key → your Dibbla token

If unsure, check the tool's docs for "OpenAI-compatible" or "custom provider"; if it has neither, it can't route through the gateway. (As of 2026, every major code assistant has at least an OpenAI-compatible mode.)

## Direct API calls (curl, scripts, custom clients)

The gateway speaks both API shapes natively. Two important things every client should know:

1. **Auth header is the standard one.** OpenAI shape uses `Authorization: Bearer …`; Anthropic shape uses `x-api-key: …`. Both carry the user's Dibbla token; the gateway swaps it for the platform key on the way out.
2. **`X-Dibbla-App` is optional, opt-in attribution.** When a Dibbla-deployed app sends `X-Dibbla-App: <DIBBLA_ALIAS>`, the gateway cross-checks the alias against the user's org in deploy-api and tags the row with `source=app, app_alias=<alias>`. **Setting this header from a laptop with an arbitrary string does not do anything malicious — the gateway either matches it or quietly records `source=external`. It is best-effort attribution, not authentication.**

### OpenAI-shape curl

```bash
curl https://ai.dibbla.net/openai/v1/chat/completions \
  -H "Authorization: Bearer $DIBBLA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "say hi"}]
  }'
```

With per-app attribution (when called from a Dibbla-deployed app):

```bash
curl https://ai.dibbla.net/openai/v1/chat/completions \
  -H "Authorization: Bearer $DIBBLA_API_TOKEN" \
  -H "X-Dibbla-App: $DIBBLA_ALIAS" \
  -H "Content-Type: application/json" \
  -d '{ "model": "gpt-4o-mini", "messages": [{"role":"user","content":"hi"}] }'
```

### Anthropic-shape curl

```bash
curl https://ai.dibbla.net/anthropic/v1/messages \
  -H "x-api-key: $DIBBLA_API_TOKEN" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-6",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "say hi"}]
  }'
```

Streaming works the same way — add `"stream": true` and read the SSE response. The gateway forwards SSE byte-for-byte; it also tees a copy into the parser so the captured record carries full content blocks.

## Token attribution: one nuance to flag

The gateway is **per-user-token**. If Erik runs Claude Code with his own Dibbla token, every call shows up in the console as `erik@dibbla.com`. If two teammates share a token (don't), the console can't tell their calls apart.

**Recommendation in plain words:** treat the Dibbla API token like an SSH key — one per person, never paste a teammate's token into your IDE. If you need a service account, mint a separate Dibbla token for that account.

## Verifying the setup

After configuring a tool, the fastest sanity check is:

```bash
dibbla ai test          # hits /health + validates the token
```

Then make one call from the tool (any prompt) and refresh `https://ai.dibbla.net/console`. If the call shows up under your user, the wiring is correct. If it doesn't, the tool is still talking to the upstream provider directly — check that the base URL is set and that the API key field is your Dibbla token.

## When NOT to use the gateway

Two cases where pointing at the gateway is wrong:

- **Models the gateway doesn't proxy** (Gemini, Mistral, local LM Studio, …). The gateway only fronts OpenAI and Anthropic. Other providers must go direct.
- **Latency-critical uses where every millisecond matters and audit doesn't.** The gateway adds a hop; fine for IDE assistance, possibly noticeable for very tight loops. Direct provider calls are faster but unaudited.

## Reference

- Helper command: `dibbla ai url | env | test` — see `dibbla ai --help`.
- Console: `https://<gateway>/console/` — same login as `app.dibbla.com` / `app.dibbla.net`.
- Health: `https://<gateway>/health` — unauthenticated 200, useful for monitoring.
- Per-org ledger: filter by user, by app alias, or org-wide; live-updates as new calls land.
- Deployed-app context (env injection, `X-Dibbla-App` from inside a pod): see the `dibbla` skill's `ai-gateway.md`.
