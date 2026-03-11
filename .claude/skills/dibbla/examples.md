# Dibbla CLI — Examples

Copy-paste examples for common workflows. For full usage and flags see [reference.md](reference.md).

---

## Deploy

```bash
dibbla deploy
dibbla deploy ./my-app
dibbla deploy --alias my-api       # Custom alias instead of directory name
dibbla deploy --force
dibbla deploy -e NODE_ENV=production -e LOG_LEVEL=info
dibbla deploy --cpu 500m --memory 512Mi --port 3000
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
dibbla db delete my-old-db
dibbla db delete my-old-db --yes
dibbla db delete my-old-db --yes -q
dibbla db dump my-production-db
dibbla db dump my-production-db -o backup.dump
dibbla db restore my-staging-db --file backup.dump
dibbla db restore my-staging-db -f /tmp/backup.dump
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

**Scripting:**

```bash
export API_KEY=$(dibbla secrets get API_KEY -d myapp)
for db in $(dibbla db list -q); do echo "$db"; done
```

---

## Scripting tips

- Use `-y` / `--yes` to skip confirmations: `apps delete`, `db delete`, `secrets delete`.
- Use `-q` / `--quiet` on `db list` and `db delete` for minimal output.
- Pipe `secrets get` into env or other commands; use `db list -q` for name-only loops.
