# Dibbla CLI — Workflows

The mental model and authoring guide for Dibbla workflows. Use this when the user wants to design, validate, deploy, iterate, or debug anything reachable from `dibbla workflows`/`wf`/`nodes`/`edges`/`inputs`/`tools`/`revisions`/`functions`. Cross-links: [reference.md](reference.md) for the full command surface, [examples.md](examples.md) for end-to-end transcripts.

---

## 1. Scope check — is this a workflow task?

Apply this file when the user asks anything like:

- "Build / design / wire a workflow that …" (most often: an LLM agent that calls some tools).
- "Add / remove / connect a node / edge / tool to workflow X."
- "Validate this YAML against the workflow server."
- "Snapshot a revision before I edit X."
- "Roll back workflow X to revision Y."
- "What functions / tools are available?" → answered by the registry, not the YAML.
- "Why won't my workflow run?" → 95% a validator error from §10 below.

If the user is asking about something else (apps, deploy, db, secrets, runtime logs) this file isn't relevant — fall back to `SKILL.md` and the right sibling doc.

---

## 2. Mental model

A workflow is a **directed acyclic graph of typed function calls**.

- **Nodes are function calls.** Each non-trivial node names a `function` from the registry (e.g. `handlebars_template`, `reasoning_agent_function`, `get_weather_function`); when invoked, the workflow server calls that function with input values and stores the outputs.
- **Edges carry typed data, port-to-port.** An edge says "feed this output of node A into that input of node B." Edges have **no conditions** — data always flows when it's available.
- **Activation is push-based.** A node fires the moment all its non-optional inputs have a value (either from an edge, a hardcoded value, or an API request body). There is no scheduler, no orchestrator. If an input has no provider, the node never fires — that's the most common silent failure.
- **No cycles.** The validator rejects cycles outright; the runtime would hang on them anyway.
- **HTTP shape: API in, API out.** A workflow is exposed as an HTTP endpoint by including an `api` node (the request body) and an `api_response` node linked back to it (the response body).
- **Versioned snapshots.** Every workflow has a mutable `HEAD` revision plus zero or more immutable named revisions. Patches and updates target HEAD; production callers can pin to a named revision.

---

## 3. The slim YAML format

This is what `dibbla wf create -f file.yaml`, `wf update <name> -f file.yaml`, `wf get <name> -o yaml`, and `wf validate -f file.yaml` consume. It's a friendly façade over the verbose React-Flow JSON the editor uses; you should **always author in slim YAML** and let the server compile it.

The complete shape (every keyword the format supports) — annotated reference example for an agent + tool + handlebars-template:

```yaml
name: weather_assistant         # required; ^[a-zA-Z][a-zA-Z0-9_-]*$
label: "Weather Assistant"      # optional UI label
description: "Asks an LLM agent the weather, with a tool"   # optional

nodes:
  # ── HTTP entry point ──────────────────────────────────────
  - id: api_input               # required; unique within the workflow
    type: api                   # one of: api | api_response | function
    inputs: [question]          # api inputs is a LIST of names (becomes request body keys)
    outputs: [question]         # api outputs typically mirror inputs

  # ── Static system prompt via Handlebars ───────────────────
  - id: system_prompt
    type: function
    function: handlebars_template       # function name from the registry
    server: function-server         # which function server hosts it
    inputs:                             # function inputs is a MAP of name → value
      script: |                         # the template literal
        You are a helpful assistant.
        Answer the user's question using the tools available.
    outputs: [error, output]            # explicit outputs (registry's are merged in)

  # ── The agent: an LLM with one tool wired in ──────────────
  - id: agent
    type: function
    function: reasoning_agent_function  # tagged accepts_tools in the registry
    server: function-server
    inputs:
      model: "claude-sonnet-4-5-20250514"   # hardcoded constant
      prompt_message: ~                     # ~ = null → must be supplied by an edge
      system_message: ~
    tools:
      - weather_tool                        # node IDs that act as this agent's tools
    outputs: [response]

  # ── The same agent, using every optional capability ───────
  # Only valid on a function that DECLARES the capability — check with
  # `dibbla fn get <server> <name>`, which lists them under `capabilities:`.
  # `reasoning_agent_with_toolbox` declares all of the ones below.
  - id: rich_agent
    type: function
    function: reasoning_agent_with_toolbox
    server: function-server
    inputs:
      model: "claude-sonnet-4-5-20250514"
      prompt_message: ~
      system_message: ~
    tools: [weather_tool]           # capability "tools" — tool NODES, wired as edges
    toolbox_tools: [generate_image] # capability "tools" — tools resolved by registry NAME
    mcp_servers:                    # capability "mcp"
      - name: acme                  # also the lookup key into the run's credentials
        url: https://mcp.acme.test
        allowed_tools: [lookup]     # optional allowlist; keeps the tool list small
    data_sources: [ds_abc]          # capability "data_sources"; or full objects (below)
    memory:                         # capability "memory"
      history_policy: last-N        # tiered | full | text-only | last-N | custom
      history_policy_n: 7
    tool_search:                    # capability "tool_search"
      allow: true
      persist: true
      scope:                        # empty = the full non-agent pool
        - { type: tag,  value: search }
        - { type: tool, value: dangerous_thing, exclude: true }
    outputs: [response]

  # ── A tool: ordinary function node, referenced from agent.tools ──
  - id: weather_tool
    type: function
    function: get_weather_function
    server: function-server
    inputs:
      query: ~                              # filled at runtime by the agent, NOT by an edge
    outputs: [result]

  # ── HTTP exit point, linked to the entry ──────────────────
  - id: api_response
    type: api_response
    linked_to: api_input         # required; must reference an `api` node by id
    inputs: [response]           # api_response inputs is a LIST (becomes response body keys)

edges:
  # Format: "<srcNodeID>.<srcPort> -> <tgtNodeID>.<tgtPort>"
  # Note the spaces around the arrow — required by the parser.
  - api_input.question -> agent.prompt_message
  - system_prompt.output -> agent.system_message
  - agent.response -> api_response.response
  # Tool-connection edges are auto-generated from the agent's `tools:` list — do not author them manually.
```

That's all there is. Three top-level keys (`nodes`, `edges`, plus metadata); edges are plain strings.

### Every node key

| Key | Applies to | Shape |
|---|---|---|
| `id` | all | Unique within the workflow. **This is the name the workflow keeps** — it is what `edges:`, `tools:` and every patch command address. |
| `type` | all | `api` \| `api_response` \| `function` |
| `label` | all | Display name on the canvas. Cosmetic; `id` is the identity. |
| `function`, `server` | `function` | Registry coordinates; both required |
| `linked_to` | `api_response` | Id of the `api` node this responds for |
| `inputs`, `outputs` | all | See below |
| `tools` | agent `function` | Node ids to wire in as tools |
| `toolbox_tools` | agent `function` | Registry function **names**, resolved at run time |
| `mcp_servers` | agent `function` | `{name, url, allowed_tools?}` entries |
| `data_sources` | agent `function` | Bare source id, or `{source_id, access?, allowed_operations?, include_linked_tools?, allowed_linked_tools?}` |
| `memory` | agent `function` | `{history_policy?, history_policy_n?}` |
| `tool_search` | agent `function` | `{allow?, persist?, scope?}` |
| `capability_providers` | agent `function` | Capability seat → provider name |

The agent keys are only accepted on a function that declares the matching capability; otherwise you get `CAPABILITY_NOT_SUPPORTED` (or `TOOLS_NOT_SUPPORTED` for `tools:`) rather than a setting that is saved and then ignored.

### Inputs is polymorphic by node type

| Node type | `inputs:` shape | Example |
|---|---|---|
| `api` | List of names | `inputs: [question, locale]` |
| `api_response` | List of names | `inputs: [response]` |
| `function` | Map of name → value (use `~` for null) | `inputs: { model: "claude-sonnet-4-5", prompt: ~ }` |

`outputs:` is always a list of names. For `function` nodes you only need to list outputs when you want to override or augment what the registry declares.

**You only have to write the inputs that are required.** `dibbla fn get` reports which those are under `required_inputs`; everything else is optional and can be omitted entirely. Optional inputs are still settable — a value or an edge works fine — they just don't have to be. Engine-injected inputs (`_capability_providers`, `has_credentials`, `_original_system_message`) and the agent's internal `tools` array are not authorable at all and never appear in `wf get` output.

### `wf get` output is re-appliable as-is

`dibbla wf get <name> -o yaml` returns exactly what `wf create`/`wf update` accept, and applying it back is a no-op — same ids, same api trigger urls, byte-identical on a second `wf get`. That makes the download → edit → upload loop safe, and makes `wf get` a good way to crib the shape of an existing workflow.

---

## 4. The three node types — and the four roles they play

Slim YAML has **three** type values: `api`, `api_response`, `function`. The user-facing UI shows a richer taxonomy (`agent`, `tool`, `script`, `codexBase`, `flow_tool`) — but those are just the editor's *presentation* of the same `function` type, inferred from which function name you picked and how the node is wired. Authoring just uses `function`; the role emerges from the wiring.

| Slim type | Role | When to use | Required fields | Common pitfalls |
|---|---|---|---|---|
| `api` | HTTP entry — request body | Every callable workflow needs one | `inputs:` list of input names | An `api` node with no edges leaving it ⇒ nothing downstream ever runs |
| `api_response` | HTTP exit — response body | Every callable workflow needs one, paired to an `api` | `linked_to:` (must point at an `api` node) | Forgetting `linked_to` is the #1 validator hit |
| `function` (as agent) | LLM agent that may call tools | Any "ask an LLM and let it decide" step | `function:` is one with the `accepts_tools` tag (e.g. `reasoning_agent_function`); `tools:` is the list of tool node IDs | The agent function must have `accepts_tools` in its registry tags or the tool wiring is silently dropped — verify with `dibbla fn get <server> <name>` |
| `function` (as tool) | Function the agent may invoke | Anything you want the agent to be able to *choose* to do | Just an ordinary `function` node referenced in some agent's `tools:` list | Tool inputs are filled at runtime by the agent — **any hardcoded `inputs.value` on a tool is overwritten and ignored**; use `~` |
| `function` (as script) | Pure transform / template | Compose prompts from upstream values, format JSON, etc. | Convention: `function: handlebars_template`; the `script:` input holds the template (`{{var}}` references) | Hardcoded `script:` is fine; other inputs typically come from edges. Use `outputs: [error, output]` |
| `function` (as codexBase) | Plain function call (data fetch, today's date, custom logic) | Everything else: `todays_date`, `static_output`, custom registry functions, sub-workflow embedding | Just a `function` + `server` reference; inputs from edges or hardcoded | Don't forget the `server` — it's required even though there's usually only one |

The one extra slim-only path is **sub-workflow embedding**: a `function` node whose `function:` is the name of another workflow registered as a function. You'll see this surface as `flow_tool` in the editor; for authoring, treat it as an ordinary function node.

---

## 5. Edges and data flow

Edges are strings shaped `"<srcNodeID>.<srcPort> -> <tgtNodeID>.<tgtPort>"`. The arrow is `space dash greater-than space` — `parts := strings.SplitN(s, " -> ", 2)` (`types/slim_workflow.go` `ParseEdgeString`). Mis-spaced arrows fail with `INVALID_EDGE_FORMAT`.

Rules the validator enforces:

- **Both nodes must exist** (`UNKNOWN_NODE`).
- **Both ports must exist on their nodes** (`UNKNOWN_PORT`). Port = a name from the node's `inputs`/`outputs`. For `function` nodes, the registry's declared inputs/outputs count too.
- **Each input port can only have one incoming edge** (`DUPLICATE_INPUT_EDGE`). One output may fan out to many inputs — that's fine.
- **No cycles** (`CYCLE_DETECTED`).

You **do not author tool-connection edges**. When you put a tool node ID in an agent's `tools:` list, the server materializes the underlying tool-connection edges (with the verbose handle prefix `tool-connection:…`) automatically. Authoring them manually in the slim YAML's `edges:` is unsupported.

---

## 6. Tools and the agent pattern

The most-used pattern in production: one `api` input, one or more `function`-as-script nodes that compose a prompt, one `function`-as-agent that calls tools, one `api_response`. To wire a tool to an agent:

1. Define the tool as an ordinary `function` node (give it a meaningful `id`).
2. List that node's `id` in the agent's `tools: [...]` array.
3. Don't add edges to the tool's inputs — the agent fills them at runtime when it decides to invoke the tool.

```yaml
- id: agent
  type: function
  function: reasoning_agent_function
  server: function-server
  inputs: { model: "claude-sonnet-4-5-20250514", prompt_message: ~, system_message: ~ }
  tools: [weather_tool, time_tool]   # ← node IDs

- id: weather_tool
  type: function
  function: get_weather_function
  server: function-server
  inputs: { query: ~ }     # ← runtime-filled; do not hardcode
  outputs: [result]

- id: time_tool
  type: function
  function: todays_date
  server: function-server
  outputs: [date]
```

Mid-flight: `dibbla tools add <workflow> <agent_id> <tool_id>` and `dibbla tools remove <workflow> <agent_id> <tool_id>` patch HEAD without rewriting the whole YAML. (Note the namespace: these are top-level commands, not `dibbla wf tools`.) Both fail loudly rather than no-op — adding a tool the agent already has, or removing one it doesn't, is an error with the current tool list in the message.

### Three ways to give an agent tools

They compose; use whichever fits.

| | How | When |
|---|---|---|
| `tools:` | Node ids, wired as graph edges | The tool is a node you want to see and configure on the canvas |
| `toolbox_tools:` | Registry function **names**, resolved at run time | You want many tools without a node each; the tool needs no per-node config |
| `mcp_servers:` | External MCP server, its tools exposed to the agent | The capability lives outside the registry |

`tools:` needs a node per tool and shows up as wiring; `toolbox_tools:` is just a list of names. For ten tools the second is far less YAML. Use `allowed_tools:` on an MCP server to keep the agent's tool list short — every exposed tool costs context on every turn.

### Picking an agent function — what works, what to avoid

Several agent-shaped functions exist in the registry. Always confirm against `dibbla fn get` — the list below is a starting point, not the source of truth.

- **`reasoning_agent_with_toolbox`** (preferred when you need more than plain tool calls) — the full-capability agent: `tools`, `toolbox_tools`, `mcp_servers`, `data_sources`, `memory`, `tool_search`, `structured_output`. Required inputs are just `system_message`, `prompt_message`, `model`; every capability is opt-in and can be omitted. Use `*_no_cache` while developing (see below).
- **`reasoning_agent_function`** — the smaller workhorse. `accepts_tools`, and nothing else: `system_message`, `prompt_message`, `model`, `tools`. Fine when wired tool nodes are all you need.
- **`reasoning_agent_with_thread`** — adds thread-id-based history. **Has been observed to silently return `{response: ""}` with claude-haiku-4-5 and claude-sonnet-4-5** even with no tools and no thread_id, because most failure paths in the function populate the `error` output instead of `response`. Don't use it for new workflows until you've verified it works in your deployment with the model you want; if you need history, manage it client-side and prepend it to the `prompt_message`.
- **`reasoning_with_messages`** — takes `chat_messages`, `model`, `tools` only. **No `system_message` input** — you can't combine a system prompt and conversation history without smuggling the system instructions into the first message. Use sparingly.

**Do not wire the agent's `error` output into the `api_response` node.** It looks like the right way to surface failures, and it hangs the workflow:

```yaml
# ⚠️ This shape never returns.
- id: api_response
  type: api_response
  linked_to: api_input
  inputs: [response, error]
edges:
  - agent.response -> api_response.response
  - agent.error    -> api_response.error   # ← api_response now waits for this too
```

A wired input on an `api_response` gates it. On a successful run the agent produces `response` and never produces `error`, so the response node waits for a value nothing will send — the run stalls until the stuck-run watchdog logs `run is not making progress`, and the caller blocks to its own timeout. Verified on dev 2026-08-16: the identical workflow without the `error` edge returns in seconds.

Surface agent failures through the run instead, which costs nothing and works on both paths:

```bash
dibbla wf execute <name> --data '{…}' --follow   # errors appear in the log tail
dibbla wf logs <runId>                           # after the fact
```

Agents that fail silently do still look identical to agents that succeed with an empty answer — that problem is real, the `api_response` wiring just isn't the fix for it. If you need the error in the HTTP body, put a `handlebars_template` node between the agent and the response that merges `response` and `error` into one field, and wire only that node's output into `api_response`.

### Cache TTL — vary your input or fail your tests

`reasoning_agent_function` has a **1-hour result cache** keyed by inputs. Re-running `wf execute` with the same JSON body returns the cached result, including a cached *empty* result from a transient failure — easy to mistake for a persistent bug. During development, either vary the input each iteration (append a timestamp), or pick a `*_no_cache` function variant if the registry exposes one. Production callers normally want the cache.

### One node, one role — never both tool AND data input

A node can be **either** a data input feeding the agent's system prompt **or** a tool the agent calls — never both. If you list `node_x` in some agent's `tools:` array AND wire `node_x.output -> agent.system_message` (or anything that transitively flows back into the agent), the auto-generated tool-connection edge plus your data edge close a cycle. Pre-flight catches this and `wf execute` returns `422 CYCLE_DETECTED` instead of launching the run. Pick one role per node — the canonical shape for "inject this fact into the system prompt" is `data_source -> handlebars_template -> agent.system_message`, with the data source NOT in `tools:`. See the worked example in [examples.md](examples.md) → "Today's date in the system prompt".

---

## 7. Inputs come from three places

For any input to be satisfied (and the node to fire), it needs a value from one of:

1. **Hardcoded `value:` in the YAML.** Static system prompts, model names, fixed limits. Only valid for `function`-node inputs (`inputs:` map). Use `~` (YAML null) to declare an input is intentionally empty and must come from an edge.
2. **Edge-driven from another node's output.** The standard graph wiring.
3. **API request body.** Inputs of an `api` node arrive in the JSON body of `POST .../execute`.

**Optional vs collects_values.** Two registry tags change input behavior:
- `accepts_tools`: function may have `tools:`; the converter injects synthetic `tools[].*` inputs that are auto-populated — don't try to satisfy them yourself.
- `collects_values`: function accepts dynamic, unregistered inputs (e.g. `handlebars_template` collects whatever variable names the script references). The validator skips `UNSATISFIED_INPUT` checks for these functions.

You can introspect a function's tags with `dibbla fn get <server> <name>`.

**Match YAML types to the function's declared types.** Tool inputs are decoded into Go structs via reflection; sending `triggered: "true"` (string) into a `bool` input fails at runtime with `cannot unmarshal string into Go struct field Inputs.x of type bool`. Send native YAML types: `true` not `"true"`, `42` not `"42"`. **`dibbla fn get <server> <name>` is the ground truth** — it reports the actual Go-reflected types (`boolean` / `integer` / `float` / `string`). When in doubt, regenerate the input shape from `fn get`, or read the function source at `go-toolserver/functions/<name>/function.go`.

**Array-typed inputs are spelled with a `[]` suffix in the schema** (`files[]`, `mcp_servers[]`, `toolbox_tools[]`). Edges accept either spelling — `agent.files` and `agent.files[]` resolve to the same port — but `wf get` emits the schema spelling.

---

## 8. The functions registry — discover before you author

The registry, not the YAML, is the source of truth for what functions exist, what their inputs/outputs are called and typed, and which ones have `accepts_tools` / `collects_values`. Always start a workflow task by querying it:

```bash
dibbla fn list                          # all functions, all servers
dibbla fn list --tag accepts_tools      # only agent-eligible functions
dibbla fn list --server function-server
dibbla fn get function-server reasoning_agent_function   # full schema for one
```

`fn get` answers from what the function itself publishes, so it works for every registered function whether or not any workflow uses it yet. The fields that matter when authoring:

| Field | Use |
|---|---|
| `required_inputs` | The only inputs you must satisfy. Write these; omit the rest. |
| `inputs.<name>.required` | Same thing per-input, alongside `type` and `allowed_values` |
| `inputs.<name>.capability` | Which optional capability an input belongs to |
| `capabilities` | The agent keys this function accepts (`tools`, `mcp`, `memory`, …) |
| `accepts_tools` | Whether a `tools:` list is legal at all |
| `collects_values` | Whether arbitrary extra inputs are allowed (handlebars-style) |

A reasonable warmup before authoring anything non-trivial:

```bash
dibbla fn list --tag accepts_tools
dibbla fn get function-server reasoning_agent_with_toolbox -o json | jq '{required_inputs, capabilities}'
dibbla wf get <some_existing_workflow> -o yaml > /tmp/template.yaml   # crib the shape
```

---

## 9. The three idiomatic authoring loops

### (a) Author from scratch

Use when the existing workflows aren't a fit and you need a new one.

```bash
# 1. Discover what's available
dibbla fn list --tag accepts_tools

# 2. Write a YAML file
cat > /tmp/wf.yaml <<'EOF'
name: my_new_workflow
…
EOF

# 3. Validate before sending — safe, never persists
dibbla wf validate -f /tmp/wf.yaml

# 4. Create
dibbla wf create -f /tmp/wf.yaml

# 5. Smoke-test
dibbla wf execute my_new_workflow --data '{"question":"hi"}'
```

### (b) Iterate by patch

Use when you have a working workflow and want a small change. Each command applies one operation to HEAD.

```bash
# Snapshot first — patches are not auto-revisioned
dibbla revisions create my_new_workflow

# Add a node from an inline JSON spec (or a file)
dibbla nodes add my_new_workflow --inline '{"id":"date_tool","type":"function","function":"todays_date","server":"function-server","outputs":["date"]}'

# Wire it up
dibbla edges add my_new_workflow "date_tool.date -> agent.system_message"

# Set a hardcoded input value
dibbla inputs set my_new_workflow agent model "claude-sonnet-4-5-20250514"

# Attach a tool to an agent
dibbla tools add my_new_workflow agent date_tool

# Remove things by name / spec
dibbla edges remove my_new_workflow "date_tool.date -> agent.system_message"
dibbla nodes remove my_new_workflow date_tool
```

### (c) Download → edit → upload

Use when the change is large enough that patches would be tedious.

```bash
dibbla wf get my_new_workflow -o yaml > /tmp/wf.yaml
# … edit the file …
dibbla wf validate -f /tmp/wf.yaml
dibbla revisions create my_new_workflow         # snapshot before overwriting HEAD
dibbla wf update my_new_workflow -f /tmp/wf.yaml
```

`update` is a full replacement of HEAD — it is not a merge.

**Decision rule:** if the change touches one or two nodes/edges/inputs, patch (b). If it changes the shape (adding a stage, restructuring a pipeline, refactoring), download/edit/upload (c). Always snapshot before either.

---

## 10. Validator errors and how to fix them

`dibbla wf validate -f file.yaml` (or any create/update) returns a list of these. Every rule the server enforces, with the fix:

| Rule | Meaning | Fix |
|---|---|---|
| `INVALID_NAME` | Workflow name empty or contains characters outside `[a-zA-Z0-9_-]`, or doesn't start with a letter | Rename to a valid identifier |
| `DUPLICATE_NODE_ID` | Two nodes share an `id` | Pick unique ids |
| `MISSING_REQUIRED_FIELD` | A node missed a required field — usually `type` on any node, `function`/`server` on a `function` node, `linked_to` on `api_response` | Add the missing field. |
| `UNKNOWN_FUNCTION` | `function`/`server` pair isn't in the registry | `dibbla fn list` to see the canonical names; check spelling and that the function server is online |
| `INVALID_ENUM_VALUE` | An input value is constrained by an `enum:` tag (e.g. valid models) and the value isn't in the allowed list | `dibbla fn get <server> <name>` — the allowed values are listed under each input's `allowed_values` |
| `UNKNOWN_TOOL_NODE` | An agent's `tools: [foo]` references a node id that doesn't exist | Add the tool node, or fix the id reference |
| `INVALID_LINK` | `api_response.linked_to` points at a missing node, or at a node that isn't `type: api` | Point it at the corresponding `api` node |
| `UNSATISFIED_INPUT` | A **required** input has no edge feeding it AND no hardcoded value | Add an edge into that input, or set a value in the node's `inputs:` map. Only inputs in the function's `required_inputs` are checked — optional and capability inputs are not. Tool-node inputs are exempt (filled by the agent at runtime); `collects_values` functions are exempt (handlebars templates) |
| `TOOLS_NOT_SUPPORTED` | `tools:` on a function that isn't tagged `accepts_tools` | Pick an agent function — `dibbla fn list --tag accepts_tools`. (Previously this was accepted and the tools were silently ignored at runtime.) |
| `CAPABILITY_NOT_SUPPORTED` | `toolbox_tools:` / `mcp_servers:` / `data_sources:` / `memory:` / `tool_search:` on a function that doesn't declare that capability | `dibbla fn get <server> <name>` lists the capabilities the function has |
| `INVALID_MCP_SERVER` | An `mcp_servers:` entry is missing `name` or `url`, or uses the reserved `_` name prefix | Supply both; rename off the `_` prefix |
| `SELF_REFERENTIAL_TOOL` | A node lists its own id in `tools:` | Remove it — that's an immediate cycle |
| `INVALID_VALUE` | A capability setting is out of range (e.g. negative `history_policy_n`) | Correct the value |
| `INVALID_EDGE_FORMAT` | Edge string isn't `"src.port -> tgt.port"` (note the spaces) | Fix the syntax |
| `UNKNOWN_NODE` | Edge references a node id that doesn't exist | Fix the id |
| `UNKNOWN_PORT` | Edge port name isn't in the node's declared inputs/outputs (registry-declared inputs/outputs count too) | Use `dibbla fn get` to confirm the right names |
| `DUPLICATE_INPUT_EDGE` | Two edges target the same input port | Remove one — an input only takes one feed |
| `CYCLE_DETECTED` | The graph contains a cycle | Restructure; the runtime won't execute cycles. If you need iteration, model it as a sub-workflow called repeatedly |

### Exit codes — script against these, not against stderr

Every `wf` / `nodes` / `edges` / `inputs` / `tools` / `revisions` / `functions` command exits with a code that identifies the failure class:

| Code | Meaning |
|---|---|
| 0 | Success. For `wf validate`, the workflow is valid. |
| 1 | Everything else — network failure, unreadable file, bad flags |
| 3 | Not authorised (401/403) — `dibbla login` |
| 4 | Not found (404) |
| 5 | Validation or patch failure (422). **`wf validate` exits 5 on an invalid workflow**, so it works as a CI gate |
| 6 | Already exists (409) — use `update` instead of `create` |
| 7 | Timeout (408) |

```bash
dibbla wf validate -f workflow.yaml || exit 1   # fails the build on an invalid workflow
```

---

## 11. Execution & invocation

A workflow with at least one `api` node is callable over HTTP. Two ways to invoke:

```bash
# 1. From the CLI
dibbla wf execute <name> --data '{"question":"What's the weather in Berlin?"}'
# Use --node <api_node_id> only if the workflow has multiple `api` nodes.

# 2. The endpoint (for code calling from the outside)
dibbla wf api-docs <name>          # prints the URL + curl examples
dibbla wf url <name>               # just the URL
```

Request body shape: a JSON object **keyed by the input names declared on the `api` node**.
Response shape: a JSON object **keyed by the input names declared on the `api_response` node** (those are filled by the edges flowing into it).

Both ends pin to the workflow's HEAD revision unless you pass `--revision <id>`. The server returns a `runID` that addresses the run for the rest of its lifecycle.

**Async execution and live tailing.** `wf execute` is **synchronous by default** — it blocks until the workflow's `api_response` node fires (or the server's 30-minute hard timeout, whichever comes first). Two non-blocking variants:

- `wf execute <name> --async` returns immediately with `{"response_metadata":{"run","node","workflow"}}`. Use this when dispatching many runs or when the run is expected to take longer than the agent's terminal patience.
- `wf execute <name> --follow` (`-f`) is `--async` plus an automatic log-tail. Live operational logs stream to stdout as the run progresses; on the server-emitted `run_completed` sentinel the CLI prints the api_response payload and exits 0. Best for interactive debugging.

Once you have a `runID`, three commands address it:

- `wf logs <runId>` — operational logs (workflow-server orchestration + go-toolserver function/agent calls), tagged with `run` / `workflow` / `node` / `level` / `src`. `--follow` for live; default is historic backfill from the persisted store. Persistence policy: WARN/ERROR + a single `run_completed` sentinel are persisted; INFO/DEBUG are live-only. A quiet finished run will tail to essentially just `run completed`.
- `wf runs output <runId>` — the api_response payload, identical shape to what a sync execute would return.
- `wf runs list [-w <name>] [-n <N>]` — find recent run ids without leaving the terminal. Server caps the page size at 500.

```bash
# Typical async loop
dibbla wf execute weather --data '{"question":"…"}' --async
# → response_metadata.run = "020b1341-…"
dibbla wf logs 020b1341-… --follow              # watch it run
dibbla wf runs output 020b1341-…                # fetch the final payload
```

For runs that error before the api_response node fires (or workflows with no api_response at all), `wf runs output` returns 404 — the operational `wf logs` view is the meaningful artefact in those cases.

### Use `--follow` for the first execution after any workflow change

Silent function failures used to surface only after the 30-min server timeout. The stuck-run watchdog (default 30s, env `STUCK_RUN_WARN_SECONDS`) now bounds that wait: a stalled run emits a WARN-level log line `run is not making progress` with `pending_nodes` count and per-input `diagnosis` (e.g. *"input X has no upstream link and no default value"*, or *"input X wired but upstream did not produce a value"*). The first firing carries `rule: RUN_STUCK`; subsequent heartbeats use `RUN_STILL_STUCK`.

Treat `--follow` as the recommended default for first-run smoke tests, not as a debugging escape hatch:

```bash
# First exec after authoring or modifying a workflow — see watchdog WARN within 30s
dibbla wf execute my_workflow --data '{"…":"…"}' --follow
```

Without `--follow`, `wf execute` (sync) just sits on the open HTTP request for the whole timeout window — the watchdog's WARN exists in the run logs but you're not looking at them, and the cause of the silence stays invisible.

### Diagnosing a hanging `wf execute`

Walk this in order — each step is cheap and rules out one class of cause:

1. **Was a run created at all?** `dibbla wf runs list -w <name> -n 3`. If no row exists, the request never reached the workflow runtime — URL routing problem (auth, gateway, stale URL id), not workflow logic. Stop and check the URL.
2. **Did pre-flight reject it?** A `422` response with `rule: CYCLE_DETECTED` means a cycle (often the tool+data-edge pattern in §6) — fix the topology. Pre-flight also returns `UNSATISFIABLE_INPUT`, `UNKNOWN_NODE`, `UNKNOWN_PORT`, `INVALID_HANDLE`, `EMPTY_WORKFLOW` for other static issues.
3. **Did the watchdog fire?** `dibbla wf logs <runId>`. Look for a WARN `run is not making progress`. The attached `diagnosis` field tells you which input on which node is unsatisfied — fix the missing edge or default and retry.
4. **`function execution failed`?** Function-side error. The most common cause is an input-type mismatch (string into bool, missing required field). The CLI message is the same regardless of panic, returned error, or input deserialization failure — the actual Go error is only in the go-toolserver pod's stdout (`kubectl logs <go-toolserver-pod>` in a Dibbla deployment, or the local console if running locally). Re-check the function's `dibbla fn get` schema and your YAML types.
5. **None of the above?** Last resort: `wf delete --yes && wf create -f workflow.yaml` to refresh the URL id and clear any orphaned response-channel state. Update production callers' baked-in URL afterward (see §15 footgun).

### Calling a workflow from production code (HTTP)

There are **two execute paths**, and they accept different auth. This catches every team once.

| Path | Used by | Auth it accepts | Stable? |
|---|---|---|---|
| `POST /api/wf/slim/workflows/<name>/execute` | The CLI's `wf execute` | User session JWT (browser/CLI login). **Not** `ak_<workflow-api-key>` Bearer tokens through the gateway. | Yes — auto-resolves the api node by name |
| `POST /api/wf/execute/<name>/<urlid>` | Production callers using a workflow API key | `Authorization: Bearer ak_<workflow-api-key>` | The `<urlid>` can go stale (see footgun) — recreate the workflow if calls start hanging |

For a Dibbla-deployed app calling its own workflow, use the second path with `ak_` Bearer auth. The first path won't accept that token through the public gateway.

**Two URLs, one of which is wrong for production.** `dibbla wf api-docs <name>` shows the workflow-server's *direct* URL — `https://workflow-server.<internal>/api/execute/<name>/<urlid>` — which requires platform-internal auth and returns "Missing authentication headers" when called with an `ak_` token. To call from a deployed app, **rewrite the host to the public gateway**:

| | URL |
|---|---|
| Shown by `wf api-docs` (internal — don't paste this into production code) | `https://workflow-server.<internal>/api/execute/<name>/<urlid>` |
| What production code should use | `https://api.dibbla.com/api/wf/execute/<name>/<urlid>` |

Same path tail, different host, plus the gateway's `/api/wf` prefix.

**Always use a short timeout in callers.** Workflow calls behave like any external HTTP — they can hang. Node's undici defaults to a 5-minute headers timeout, and a stale `<urlid>` can hang silently for the full 5 minutes before throwing `UND_ERR_HEADERS_TIMEOUT`. Wrap every workflow fetch in an `AbortController` with 30–60s, log before and after, and surface failures fast:

```javascript
const ctrl = new AbortController();
const t = setTimeout(() => ctrl.abort(), 60_000);
console.log("[wf] calling", name);
try {
  const r = await fetch(`https://api.dibbla.com/api/wf/execute/${name}/${urlId}`, {
    method: "POST",
    headers: { Authorization: `Bearer ${process.env.WORKFLOW_API_KEY}`, "Content-Type": "application/json" },
    body: JSON.stringify({ question }),
    signal: ctrl.signal,
  });
  console.log("[wf] returned", r.status);
  if (!r.ok) throw new Error(`workflow ${name} returned ${r.status}`);
  return await r.json();
} finally {
  clearTimeout(t);
}
```

If a production call to the gateway path silently hangs while `dibbla wf execute` against the same workflow works, **the `<urlid>` has gone stale** — see the footgun below; the fix is `wf delete --yes` + `wf create`. Don't chase auth/path/header issues first.

---

## 12. Revisions

A workflow's name (`my_workflow`) is stable. Underneath it lives:

- **`HEAD`** — the mutable working revision. Every `wf update`, `nodes add`, `edges add`, `inputs set`, `tools add` writes here.
- **Named revisions** — immutable snapshots. Created by `dibbla revisions create <workflow>` (returns the new id, e.g. `1td9`).

```bash
dibbla revisions list <workflow>           # shows id, timestamp, label
dibbla revisions create <workflow>         # snapshot HEAD as a new immutable revision
dibbla revisions restore <workflow> <id>   # makes <id> become the new HEAD (overwrites the current HEAD)
```

`restore` is **not** a checkout — it's an update. Once HEAD has been overwritten, it's overwritten. To "go back" to where you were before the restore, you'd need a snapshot you took before doing it. **Always snapshot before patching, always snapshot before restoring** — the cost is one HTTP call.

**Concurrent edits are detected, not silently lost.** The slim `PUT /api/wf/slim/workflows/:name` requires an `If-Match` header containing the current ETag; the CLI's `wf update` sends it automatically. If you and a teammate (or you and the browser editor) both edit the same workflow and both push, the second writer gets `412 Precondition Failed` with a JSON body containing `current_etag` and `received_etag`. Pull the latest with `wf get`, merge your changes, and retry — or pass `--force` to override. Use `--force` only for known-overwrite cases (re-applying a stable definition from CI); avoid it as a "make the error go away" reflex, since it overwrites whatever the other writer just shipped.

`dibbla wf delete <name>` removes the workflow **and all of its revisions** — there is no per-revision delete in the CLI. Use `--yes` for non-interactive.

---

## 13. Three canonical workflow shapes

The wild has many variations on three core shapes. Recognize which one the user wants before you start typing.

### (a) Pure transform — API → script → API

No LLM, just data shaping. Useful for format conversion, templating, light arithmetic.

```text
api_input  ──message──▶  greeting (handlebars_template)  ──output──▶  api_response
```

### (b) Agent + tools — the most common shape

One LLM call with N tools available. The agent decides which tools to invoke.

```text
api_input ──question──▶ agent (reasoning_agent_function)
                        │  tools: [weather_tool, time_tool, search_tool]
system_prompt (handlebars) ──output──▶ agent.system_message
                        │
                        agent ──response──▶ api_response
```

### (c) Multi-stage pipeline — the complex shape

Chained agents with intermediate parsing/templating. Use when you need to (e.g.) parse messy input first, then run a tool-equipped solver, then format the result.

```text
api_input
   │
   ├──▶ data_fetch (codexBase: e.g. fetch from external API)
   │
   ▼
parse_prompt (handlebars) ──▶ parser_agent (generic_agent_function) ──▶ parsed
                                                                          │
                          ┌───────────────────────────────────────────────┘
                          ▼
solver_prompt (handlebars) ──▶ solver_agent (reasoning_agent_function with N tools)
                          │
                          ▼
                       api_response
```

Production examples that follow shape (c): SVN-augmented crash-analysis flows (parse crash → fetch repo data → solve with SVN tools), localization pipelines (extract terms → translate → format).

---

## 14. Pre-flight checklist

Before `wf create` or `wf update`, walk this:

- [ ] Workflow `name` matches `^[a-zA-Z][a-zA-Z0-9_-]*$`.
- [ ] Every node has a unique `id`.
- [ ] Every node has a `type` of `api`, `api_response`, or `function`.
- [ ] Every `function` node has both `function:` and `server:`.
- [ ] Every `api_response` has `linked_to:` pointing at an `api` node.
- [ ] Every edge is shaped `"src.port -> tgt.port"` with single spaces around the arrow.
- [ ] Every edge port name exists on its node (or in the registry's declared ports for that function).
- [ ] No input port has more than one incoming edge.
- [ ] Every non-tool, non-`collects_values` `function` node input is satisfied by an edge OR a value.
- [ ] No cycles.
- [ ] `dibbla wf validate -f file.yaml` returns clean.

---

## 15. Footguns

Things that compile clean but bite at runtime, or that look right but aren't:

- **Hardcoded tool inputs are silently overwritten.** If you write `inputs: { query: "Berlin" }` on a tool node, the agent fills `query` from its own decision and your value is gone. Use `~` to make this visible to readers.
- **Cycles fail validation, but missing satisfaction is silent at runtime.** A node with one unsatisfied input never fires; downstream nodes never get their inputs; the request hangs until timeout. Trust `UNSATISFIED_INPUT` from the validator and fix all of them before running.
- **`revisions restore` overwrites HEAD; it does not check out.** If you restore to recover from a bad change, then edit, then realize you wanted the *previous* HEAD back, you've already lost it unless you snapshotted.
- **`wf delete` removes ALL revisions.** There's no soft delete and no per-revision delete.
- **Patches don't snapshot.** `nodes add` / `edges add` / `inputs set` / `tools add` modify HEAD with no automatic revision. Wrap risky patch sequences in `revisions create` before, `revisions create` after.
- **Patches are strict, and that's deliberate.** Adding a tool the agent already has, removing one it doesn't, setting an input the function doesn't declare, removing a node that isn't there, or removing an edge whose spacing doesn't match exactly — each is an error (exit 5) naming what's actually there, not a silent success.
- **The editor and the CLI are safe to use on the same workflow.** A slim write preserves everything the YAML doesn't describe: canvas positions and sizes, and node settings with no slim key. Agent capability config (`toolbox_tools`, `mcp_servers`, `data_sources`, `memory`, `tool_search`) *is* described by the YAML, so it round-trips — but that also means deleting one of those keys from your file and running `wf update` clears the setting. Concurrent writes are still caught by the ETag check (see §12).
- **The registry can change underneath you.** A function that exists today on `function-server` may not next week. Workflows referencing a removed function fail at execution with `UNKNOWN_FUNCTION`. Pinning a revision pins the YAML, not the registry — there is no function-version pinning at the workflow level beyond the function's own `version` field.
- **Edge spaces are load-bearing.** `"a.x->b.y"` is `INVALID_EDGE_FORMAT`. Always `"a.x -> b.y"`.
- **Tool-connection edges are auto-generated.** Don't hand-author entries like `agent.tool-connection:foo -> tool.tool-connection:bar` in `edges:`; the slim YAML has no syntax for this and the converter fills it in from `tools: […]`.
- **`<urlid>` goes stale if you rename an api node.** Node identity is the `id:` you wrote, so a node keeps its UUID — and its api node keeps its `<urlid>` — across updates as long as the id doesn't change. **Changing an api node's `id:` is a rename**, and a rename gets a fresh UUID, invalidating any `<urlid>` baked into production callers. Labels, inputs and function swaps no longer affect it. After renaming an api node, run `dibbla wf api-docs <name>` and update the caller (or rebuild + redeploy if the url id is baked at build time). Symptom of a missed rename: gateway-path calls hang for the full caller-side timeout (typically the undici 5-min default) while `dibbla wf execute` keeps working — the CLI's slim path resolves the api node dynamically and isn't affected.
- **Result cache is 1 hour, including failed runs.** `reasoning_agent_function` caches by input — re-running `wf execute` with the same body returns the cached prior result. If a prior run silently failed and returned an empty response (see §6), the empty answer is cached for an hour and looks like a persistent bug. Vary the input or use a `*_no_cache` variant during development.
- **Silent-empty agents.** A runtime failure shows up as a successful response with an empty `response` field. The obvious fix — routing `agent.error -> api_response.error` — makes it worse: it hangs every successful run (§6). Watch the run instead (`wf execute --follow`, `wf logs <runId>`), or merge `response` and `error` in a `handlebars_template` node and wire only that node's output into the response.
- **File-emitting functions need `API_TOKEN` on go-toolserver.** `generate_image`, `transcribe_audio`, and anything that uploads an artefact via `/api/files/init` authenticate with the toolserver's `API_TOKEN` env var. Missing it → the model API call succeeds but the upload 401s, and the agent's reply reads as a generic "authentication error" (the storage layer, not the model API). Platform-operator concern, not workflow-author — see [platform.md](platform.md) § 10.5.
