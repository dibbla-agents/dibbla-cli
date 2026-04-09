# Dibbla CLI — Examples

Copy-paste examples for common workflows. For full usage and flags see [reference.md](reference.md).

---

## Feedback

```bash
dibbla feedback "The deploy took too long"
dibbla feedback Everything is on fire
dibbla feedback "Why does the database keep disappearing?"

# List all feedback
dibbla feedback list

# Delete feedback
dibbla feedback delete 550e8400-e29b-41d4-a716-446655440000
dibbla feedback delete 550e8400-e29b-41d4-a716-446655440000 --yes
```

---

## Deploy

```bash
dibbla deploy
dibbla deploy ./my-app
dibbla deploy --alias my-api       # Custom alias instead of directory name
dibbla deploy --force
dibbla deploy --update             # Rolling update (zero downtime)
dibbla deploy -e NODE_ENV=production -e LOG_LEVEL=info
dibbla deploy --cpu 500m --memory 512Mi --port 3000
dibbla deploy --favicon https://example.com/favicon.ico
dibbla deploy ./ --cpu 500m --memory 512Mi -e NODE_ENV=production
```

---

## Apps

```bash
dibbla apps list
dibbla apps update myapp -e NODE_ENV=production
dibbla apps update myapp -e NODE_ENV=production -e LOG_LEVEL=info
dibbla apps update myapp --replicas 3
dibbla apps update myapp --cpu 500m --memory 512Mi --port 3000
dibbla apps update myapp --replicas 2 --cpu 1 --memory 512Mi -e NODE_ENV=production
dibbla apps update myapp --favicon https://example.com/favicon.ico
dibbla apps update myapp --favicon ""   # Clear favicon
dibbla apps delete my-old-app
dibbla apps delete my-old-app -y
```

---

## Db

```bash
dibbla db list
dibbla db list -q
dibbla db create my-new-db
dibbla db create --name my-new-db
dibbla db create mydb --deployment myapp   # Scope DB + DATABASE_URL secret to a deployment
dibbla db delete my-old-db
dibbla db delete my-old-db --yes
dibbla db delete my-old-db --yes -q
dibbla db dump my-production-db
dibbla db dump my-production-db -o backup.dump
dibbla db restore my-staging-db --file backup.dump
dibbla db restore my-staging-db -f /tmp/backup.dump
dibbla db connect myapp                    # Print connection string with tips
dibbla db connect myapp -q                 # Connection string only (scripting)
psql $(dibbla db connect myapp -q)         # Quick connect
export DATABASE_URL=$(dibbla db connect myapp -q)  # Export as env var
```

---

## Secrets

**Global (omit `-d`):**

```bash
dibbla secrets list
dibbla secrets set API_KEY "my-secret-value"
echo "my-secret-value" | dibbla secrets set API_KEY
dibbla secrets get API_KEY
dibbla secrets delete API_KEY --yes
```

**Per-deployment (`-d` or `--deployment`):**

```bash
dibbla secrets list -d myapp
dibbla secrets set API_KEY "x" -d myapp
dibbla secrets set DATABASE_URL "postgres://..." --deployment myapp
cat private.key | dibbla secrets set SSL_KEY -d myapp
dibbla secrets get API_KEY -d myapp
dibbla secrets delete API_KEY -d myapp -y
```

---

## Workflows

```bash
# List and inspect
dibbla workflows list
dibbla workflows list -o json
dibbla wf list                        # alias

# Get a workflow definition
dibbla workflows get my-workflow
dibbla workflows get my-workflow -o json
dibbla workflows get my-workflow --revision abc123

# Create, update, validate
dibbla workflows create -f workflow.yaml
dibbla workflows update my-workflow -f workflow.yaml
dibbla workflows validate -f workflow.yaml

# Delete
dibbla workflows delete my-workflow
dibbla workflows delete my-workflow --yes

# Execute
dibbla workflows execute my-workflow
dibbla workflows execute my-workflow --data '{"query": "hello"}'
dibbla workflows execute my-workflow -f input.json
dibbla workflows execute my-workflow --node api_node_1

# URL and API docs
dibbla workflows url my-workflow
dibbla workflows api-docs my-workflow
dibbla workflows api-docs my-workflow -o json
```

---

## Nodes

```bash
# Add a node from a file
dibbla nodes add my-workflow -f node.yaml

# Add a node inline
dibbla nodes add my-workflow --inline '{"id":"transform","type":"code","code":"return input"}'

# Remove a node
dibbla nodes remove my-workflow transform
dibbla nodes remove my-workflow transform --yes
```

---

## Edges

```bash
# Add an edge between nodes
dibbla edges add my-workflow "input.output -> transform.input"

# Remove an edge
dibbla edges remove my-workflow "input.output -> transform.input"

# List all edges
dibbla edges list my-workflow
dibbla edges list my-workflow -o json
```

---

## Inputs

```bash
# Set a node input value
dibbla inputs set my-workflow agent1 system_prompt "You are a helpful assistant"
dibbla inputs set my-workflow agent1 temperature 0.7

# Set to null
dibbla inputs set my-workflow agent1 max_tokens ignored --null
```

---

## Tools

```bash
# Add a tool to an agent node
dibbla tools add my-workflow agent1 web_search
dibbla tools add my-workflow agent1 calculator

# Remove a tool
dibbla tools remove my-workflow agent1 web_search
```

---

## Revisions

```bash
# List revisions
dibbla revisions list my-workflow
dibbla rev list my-workflow           # alias
dibbla revisions list my-workflow -o json

# Create a snapshot
dibbla revisions create my-workflow
dibbla revisions create my-workflow -q   # prints only the revision ID

# Restore
dibbla revisions restore my-workflow abc123
```

---

## Functions

```bash
# List available functions
dibbla functions list
dibbla fn list                        # alias
dibbla functions list --server my-server
dibbla functions list --tag search
dibbla functions list -o json

# Get function details
dibbla functions get my-server web_search
dibbla functions get my-server web_search -o json
```

---

## Scripting tips

- Use `-y` / `--yes` to skip confirmations: `apps delete`, `db delete`, `secrets delete`, `workflows delete`, `nodes remove`.
- Use `-q` / `--quiet` on `db list`, `db delete`, `db connect`, and workflow commands for minimal output.
- Use `-o json` on workflow commands for machine-readable output.
- Pipe `secrets get` into env or other commands; use `db list -q` for name-only loops.
- `revisions create -q` prints only the revision ID for scripting.

```bash
# Save a revision and capture the ID
REV=$(dibbla revisions create my-workflow -q)
echo "Created revision: $REV"

# Export a secret
export API_KEY=$(dibbla secrets get API_KEY -d myapp)

# Loop over databases
for db in $(dibbla db list -q); do echo "$db"; done

# Validate before deploying a workflow
dibbla workflows validate -f workflow.yaml && dibbla workflows update my-workflow -f workflow.yaml
```

---

## Agent workflows

Step-by-step patterns for AI agents deploying and managing apps non-interactively.

### Pre-deploy guardrails workflow

```bash
# 1. Code is ready — run guardrails checks by reviewing the source code
#    Check for: security issues, database anti-patterns, unsafe API calls, external write safety
#    See guardrails.md for the full checklist

# 2. Present the guardrails report to the user (example):
#
#    ## Pre-deploy guardrails report
#    - [x] Security (OWASP Top 10): OK
#    - [x] Database usage: OK
#    - [x] REST/API calls: 1 warning
#      - WARNING: No retry logic on payment API call in src/services/payment.js:23
#    - [x] External writes: OK
#    **Result: PASSED with 1 warning**
#
#    Ask: "There is 1 warning. Should I fix this before deploying, or proceed as-is?"

# 3. Wait for user confirmation before deploying or fixing anything

# 4. Deploy only after explicit user approval
dibbla deploy . --alias my-app --update
```

### Deploy a new app (first time)

```bash
# 1. Check if the app already exists
dibbla apps list

# 2. If NOT listed, deploy with all required env vars in one command
dibbla deploy . --alias my-app \
  -e DATABASE_URL="postgres://user:pass@host:5432/db" \
  -e API_KEY="sk-xxx" \
  -e NODE_ENV=production \
  -e PORT=3000

# 3. Verify it's running
dibbla apps list
```

### Update an existing app (zero downtime)

```bash
# Rolling update — re-deploys the code, keeps existing env vars
dibbla deploy . --alias my-app --update

# To change env vars without redeploying code
dibbla apps update my-app -e LOG_LEVEL=debug -e NEW_VAR=value
```

### Deploy-or-update pattern

```bash
# Check if app exists, then deploy or update accordingly
if dibbla apps list 2>/dev/null | grep -q "my-app"; then
  dibbla deploy . --alias my-app --update
else
  dibbla deploy . --alias my-app \
    -e DATABASE_URL="postgres://..." \
    -e NODE_ENV=production
fi
```

### Full setup: app + database + secrets

```bash
# 1. Create the database (scoped to the deployment — auto-creates DATABASE_URL secret)
dibbla db create my-app-db --deployment my-app

# 2. Set additional secrets
dibbla secrets set API_KEY "sk-xxx"

# 3. Deploy with env vars
dibbla deploy . --alias my-app \
  -e API_KEY="sk-xxx" \
  -e NODE_ENV=production

# 4. Verify
dibbla apps list
dibbla secrets list -d my-app

# Alternative: get the connection string directly
export DATABASE_URL=$(dibbla db connect my-app-db -q)
```

### Tear down an app

```bash
# Always use --yes to avoid interactive prompt
dibbla apps delete my-app --yes
dibbla db delete my-app-db --yes
dibbla secrets delete API_KEY --yes
```
