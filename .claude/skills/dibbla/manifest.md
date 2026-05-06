# Dibbla CLI — Multi-service manifest (`dibbla.yaml`)

A `dibbla.yaml` lets one deploy bundle multiple services — e.g. a web frontend + a worker + a redis sidecar — under a single alias. The whole graph is built and applied atomically; rollback is automatic on any failure. Use this file as the schema reference when authoring or reviewing a manifest. Cross-links: [reference.md](reference.md) for the CLI flags around manifests, [examples.md](examples.md) for runnable transcripts, [platform.md § 8.5](platform.md) for the runtime contract under multi-service, [guardrails.md](guardrails.md) for pre-deploy multi-service safety checks.

---

## 1. When to use a manifest

Add a `dibbla.yaml` when **any** of these is true:

- The app needs more than one container — e.g. `web + worker`, `api + cache`, `web + redis + worker`.
- The app needs a per-environment shape — e.g. `replicas: 1` in dev, `replicas: 3` in prod, sourced from one file.
- The app needs init steps that run before the main container — e.g. db migrations.
- The app needs cron jobs declared alongside the deployment.
- The app needs a per-service secret (a token only the worker should see) or a build-time secret (private npm token used during `docker build`).

Keep the legacy single-`Dockerfile` path when:

- The app is one container and stays that way.
- All env vars are happy living on `dibbla deploy --env` / `dibbla apps update --env`.
- You don't need cross-service discovery (`DIBBLA_SVC_*` env vars).

The two paths are mutually exclusive at deploy time: detection is `dibbla.yaml` present at the root of the archive ⇒ multi-service; absent ⇒ single-Dockerfile. The CLI fails fast if `dibbla.yaml` AND `dibbla.yml` are both present (`MANIFEST_AMBIGUOUS`) so you don't have to guess which one ships.

---

## 2. Mental model

Think of the manifest as a typed declaration of a small Kubernetes graph. Each `services:` entry becomes a Deployment + Service (and optionally PVCs + NetworkPolicy + Ingress). Each `jobs:` entry becomes a CronJob. The deploy-api:

1. Parses + validates the manifest.
2. Resolves env-aware fields against the active env (`prod` by default).
3. Filters services by active `profiles:`.
4. Runs a quota check on the resolved set.
5. Builds every `build:`-typed service in parallel via BuildKit.
6. Renders the final K8s objects with stable names and labels.
7. Applies them with rollback-on-failure (a journal of pre-state means a partial failure rolls every object back to where it started).
8. Marks orphans (objects from the previous deploy that no longer exist) for sweep.
9. Sets up the public route to the one service that has `public: true`.

Local validation (`dibbla manifest validate`) runs steps 1 only. Server preview (`dibbla preview`) runs 1–4. A real deploy runs 1–9.

---

## 3. Top-level shape

```yaml
version: 1                       # required; only 1 is supported today
services:                        # required; at least one entry
  web:
    build: ./web
    port: 3000
    public: true
  worker:
    build: ./worker
  redis:
    image: redis:7
    port: 6379
jobs:                            # optional; cron-style scheduled jobs
  nightly-cleanup:
    schedule: "0 2 * * *"
    image: alpine:3.20
    command: [sh, -c, "echo cleanup"]
```

Reserved top-level keys (rejected today, kept for future versions): `volumes:`, `networks:`, `secrets:`, `cron:`, `init:`. Use the per-service equivalents instead.

---

## 4. Per-service fields

| Field | Type | Required | Env-aware | Default | Notes |
|---|---|---|---|---|---|
| `build` | string \| object | one of build/image | no | — | Path or build spec; see § 5 |
| `image` | string \| env-map | one of build/image | yes | — | Pulled image; must include a tag |
| `port` | int | when `public: true` | no | — | 1–65535; container port the service listens on |
| `public` | bool \| env-map | no | yes | `false` | Routes the public URL to this service; **at most one** in v1 (see § 13) |
| `replicas` | int \| env-map | no | yes | `1` | Number of pods; capped by org quota (§ 18) |
| `cpu` | string \| env-map | no | yes | platform default | K8s CPU spec (`500m`, `1`, `2`); resource quota applies |
| `memory` | string \| env-map | no | yes | platform default | K8s memory spec (`256Mi`, `1Gi`) |
| `environment` | string-map \| env-map-of-string-maps | no | yes | empty | Env vars; supports `${VAR}` substitution (§ 8) |
| `command` | list-of-strings | no | no | image default | Overrides the container `CMD` |
| `entrypoint` | list-of-strings | no | no | image default | Overrides the container `ENTRYPOINT` |
| `volumes` | list of `{path, size, access?}` | no | no | empty | Per-service PVCs; see § 10 |
| `profiles` | list-of-strings | no | no | empty | Activation gates; see § 7 |
| `depends_on` | list-of-strings | no | no | empty | Boot ordering hint; rolls out in topological order |
| `expose_to` | list-of-strings | no | no | open | NetworkPolicy whitelist; see § 9 |
| `init` | list of init containers | no | no | empty | Run-once containers before main; see § 11 |
| `healthcheck` | object | no | no | platform default | Liveness/readiness/startup probes; see § 12 |
| `domain` | string | no | no | `<alias>.dibbla.com` | Custom hostname for the public service; see § 14 |

Service names must match `^[a-z][a-z0-9-]{0,29}$`. Reserved names: `proxy`, `auth`, `system`, `dibbla`, `kube-*`. Service names appear in DNS, in env-var names (`DIBBLA_SVC_<NAME>_HOST` becomes upper-case-with-`_`), and in K8s object names — keep them short and DNS-safe.

---

## 5. Build vs image

Every service carries either `build:` (Dockerfile-based) or `image:` (pulled image), never both. The validator emits `MANIFEST_INVALID` if both or neither are set.

### `build:` shorthand (string)

```yaml
services:
  web:
    build: ./web        # context dir; defaults Dockerfile to ./web/Dockerfile
```

### `build:` object form

```yaml
services:
  web:
    build:
      context: ./web
      dockerfile: Dockerfile.prod      # defaults to "Dockerfile"
      target: runtime                  # optional multi-stage target
      args:                            # docker build --build-arg
        NODE_VERSION: "20"
        PUBLIC_API_URL: "https://api.example.com"
      secrets:                         # build-time secrets; see § 16
        - id: npm_token
          source: NPM_TOKEN_SECRET
```

Every `build.context` must exist inside the archive. Missing context fails the deploy with `BUILD_CONTEXT_MISSING`.

### `image:` (pulled)

```yaml
services:
  redis:
    image: redis:7
    port: 6379
```

Image refs **must include a tag**. `image: redis` is rejected (`MANIFEST_INVALID`); use `redis:7` or `redis:latest` (and don't use `latest` in prod — pin a digest).

The platform allows pulls from a configured registry allowlist (Docker Hub, GHCR, GCR by default). Private images require a build-time secret or pre-pushed image to the org registry; ask the platform operator if you need a new registry whitelisted.

---

## 6. Env-aware fields

Every field marked **Env-aware** in § 4 accepts either a scalar (uniform across environments) or a per-environment map.

### Scalar form

```yaml
services:
  web:
    replicas: 2          # always 2, regardless of --target-env
```

### Per-environment form

```yaml
services:
  web:
    replicas:
      default: 1         # used when no env-specific key matches
      staging: 2
      prod: 5
```

Resolution order at deploy time:

1. If the field is a scalar, use it.
2. Otherwise, look up `--target-env` in the map.
3. If absent, fall back to `default:`.
4. If `default:` is also absent, fall back to the field's documented default (§ 4).

The active env name is whatever `--target-env` is set to; **the server defaults to `prod`** when the flag is omitted. Resolution happens server-side so the local `dibbla manifest validate` does NOT exercise this — use `dibbla preview --target-env <env>` for that.

The `environment` field uses a richer form: per-env values are themselves maps of `KEY: value`, and `default:` overlays under the env-specific entries.

```yaml
services:
  web:
    environment:
      default:
        LOG_LEVEL: info
        FEATURE_FLAG_X: "false"
      prod:
        FEATURE_FLAG_X: "true"        # overlays default
        SENTRY_DSN: "https://..."     # only in prod
```

Resolved environment for `--target-env prod`:
```
LOG_LEVEL=info               (from default)
FEATURE_FLAG_X=true          (prod overrides default)
SENTRY_DSN=https://...       (prod-only)
```

You can mix flat and per-env forms across fields, but **not within one field**. The validator rejects `environment:` with mixed scalar and mapping values to keep resolution unambiguous.

---

## 7. Profiles

Profiles activate or skip whole services without changing the YAML. Use them when a service exists in some environments and not others (e.g. `mailcatcher` in dev, omitted in prod).

```yaml
services:
  web:
    build: ./web
    port: 3000
    public: true
  mailcatcher:
    image: mailhog/mailhog:v1.0.1
    port: 8025
    profiles: [dev]                  # only deployed when --profile dev is passed
  metrics-shipper:
    image: prom/prometheus:v2.50
    port: 9090
    profiles: [observability]
```

Activation is additive: `dibbla deploy --profile dev --profile observability` activates both. A service with no `profiles:` is always active. Skipped services appear in `PreviewResponse.skipped_services` with the reason.

**Profiles vs env-aware:** profiles toggle whether a service exists at all; env-aware fields shape an existing service. If you want one less service in prod, use a profile. If you want the same service with `replicas: 1` in dev and `replicas: 5` in prod, use env-aware `replicas:`.

---

## 8. Service discovery contract

When the deploy lands, every container in the deployment receives a fixed set of env vars so services can reach each other and identify themselves.

| Variable | Set on every service | Value |
|---|---|---|
| `DIBBLA_DEPLOYMENT_ID` | yes | Stable id across versions of this alias (`dep_<random>`) |
| `DIBBLA_ALIAS` | yes | Deployment alias (`myapp`) |
| `DIBBLA_ENV` | yes | Active env name (`prod`, `staging`, `dev`) |
| `DIBBLA_SERVICE_NAME` | yes | This container's own service name |
| `DIBBLA_SVC_<NAME>_HOST` | one per service in the deploy | Cluster DNS hostname |
| `DIBBLA_SVC_<NAME>_PORT` | one per service that declares `port:` | Container port |
| `DIBBLA_SVC_<NAME>_URL` | one per service that declares `port:` | `http://<host>:<port>` |

`<NAME>` is the service name uppercased with `-` → `_`. So a service `my-worker` becomes `DIBBLA_SVC_MY_WORKER_HOST` etc.

```yaml
services:
  web:
    build: ./web
    port: 3000
    public: true
    environment:
      REDIS_URL: ${DIBBLA_SVC_REDIS_URL}        # substituted at deploy time
      WORKER_HOST: ${DIBBLA_SVC_WORKER_HOST}
  worker:
    build: ./worker
  redis:
    image: redis:7
    port: 6379
```

Inside `web`, `REDIS_URL` resolves to e.g. `http://myapp-redis:6379`. The substitution happens server-side during render — the value is literal in the env after that. **Hard-coding cluster DNS bypasses the contract** and breaks across alias renames; use `${DIBBLA_SVC_*}` instead.

A service with no `port:` gets a Deployment but no Service object — it's reachable only via in-pod loopback (and probably shouldn't be reached at all). `DIBBLA_SVC_<NAME>_HOST` is still set so dependents can know it's part of the graph; `_PORT` and `_URL` are absent.

---

## 9. `expose_to:` and NetworkPolicy

By default services in the same deploy are mutually reachable on the cluster network. `expose_to:` switches the service to **deny by default** and allows traffic only from the listed services.

```yaml
services:
  web:
    build: ./web
    port: 3000
    public: true
  worker:
    build: ./worker
    port: 9090
    expose_to: [web]                  # only web can reach worker:9090
  redis:
    image: redis:7
    port: 6379
    expose_to: [web, worker]          # both can talk to redis
```

This translates to a Kubernetes NetworkPolicy per service. Effects:

- A service with `expose_to:` set generates a NetworkPolicy that whitelists the listed peers.
- A service WITHOUT `expose_to:` retains the default open-within-deploy posture.
- A service in `expose_to:` must exist in the manifest; references to non-existent services error with `MANIFEST_INVALID`.
- The public service (the one with `public: true`) does NOT need `expose_to:` for its public traffic — that path comes through the platform proxy/ingress, not pod-to-pod.

NetworkPolicy enforcement requires a CNI that honors it (Calico, Cilium, etc.). The platform's clusters use such a CNI; outside that you'll get the policies as plain YAML with no enforcement.

---

## 10. Volumes

Volumes attach a per-service PersistentVolumeClaim. Mounts are inside the container; the PVC lifecycle follows the deploy.

```yaml
services:
  redis:
    image: redis:7
    port: 6379
    volumes:
      - path: /data
        size: 1Gi
        access: rwo                   # default; omit for ReadWriteOnce
  shared-tools:
    image: alpine:3.20
    volumes:
      - path: /shared
        size: 5Gi
        access: rwx                   # ReadWriteMany — multi-pod write
```

Field rules:

- `path` is required, must start with `/`.
- `size` is required, K8s storage shape (`1Gi`, `500Mi`, `10Gi`). Per-service quota: 10Gi default; per-deploy total: 50Gi default.
- `access` defaults to `rwo` (`ReadWriteOnce`). Use `rwx` only when you genuinely need multi-pod write — most clusters charge more for that storage class.
- PVC name: `<resource>-<service>-<index>` (e.g. `myapp-redis-0`).
- Lifecycle: created on first deploy, retained across redeploys, **deleted with the deployment**. There is no "keep PVC across delete" knob in v1 — back up before `dibbla apps delete`.

Reserved top-level `volumes:` (for shared/named PVCs) is parsed but rejected today (`MANIFEST_UNSUPPORTED`). Per-service volumes are the only supported form.

---

## 11. Init containers

Init containers run sequentially before the main container starts. They're for migrations, schema sync, asset hydration — anything that must finish before the main process is healthy.

```yaml
services:
  api:
    build: ./api
    port: 8080
    public: true
    init:
      - name: migrate
        image: registry.example.com/migrate:v1
        command: [/usr/bin/migrate, "up"]
        environment:
          DATABASE_URL: ${DIBBLA_SVC_REDIS_URL}     # service-discovery substitution works
      - name: seed
        image: registry.example.com/seeder:v1
        command: [seeder, "--once"]
```

Rules:

- Init entries are an ordered list — they run in order, all must succeed before the main container starts.
- v1 supports `image:` only — no `build:` for init containers. The container has to be a pre-built pulled image. Use a build step in your CI to produce one if you need code from this repo.
- Each init container must **exit cleanly**. An init that runs forever blocks the rollout and the deploy will time out.
- `environment:` here is a flat map (no per-env form); use a single map of literals or `${VAR}` substitutions.
- `name:` must be unique within the service and DNS-safe (matches `^[a-z][a-z0-9-]{0,29}$`).

Init containers count against the deploy's pod resource budget — the cluster needs to schedule them too. They share the pod's PVCs (so an init can write fixtures into a `/data` mount the main container then reads).

---

## 12. Healthchecks

Healthchecks are per-service liveness/readiness/startup probes. Maps to K8s `*corev1.Probe` 1:1.

```yaml
services:
  api:
    build: ./api
    port: 8080
    public: true
    healthcheck:
      liveness:
        http_get: { path: /healthz, port: 8080 }
        initial_delay_seconds: 10
        period_seconds: 5
        timeout_seconds: 2
        failure_threshold: 3
      readiness:
        http_get: { path: /ready }      # port defaults to service.port
        period_seconds: 5
      startup:
        tcp_socket: { port: 8080 }
        failure_threshold: 30           # 30 * period_seconds before liveness kicks in
```

Probe variants:

- `http_get: { path, port? }` — HTTP GET that must return 2xx/3xx. `port` defaults to `service.port`.
- `tcp_socket: { port }` — port must be open.
- `exec: ["cmd", "arg1", ...]` — exec inside the container; exit 0 = healthy.

Exactly one of `http_get` / `tcp_socket` / `exec` per probe (validator enforces). The three probe slots (`liveness`, `readiness`, `startup`) are independent — set what you need.

Common rules:

- `initial_delay_seconds` defaults to platform-sane values; bump for slow-starting JVMs.
- `failure_threshold` ≥ 3 in production keeps a single transient failure from killing the pod.
- `startup` is the right home for slow boots: it disables liveness until the startup probe passes, so a 60-second cold start doesn't get killed at second 10.

Skip the field entirely to use the platform default health check, which is the same one the legacy single-Dockerfile path uses.

---

## 13. Multiple public services

V1 ships with one `public: true` service per deploy, exposed at `https://<alias>.dibbla.com`. **F14 widens this** to multiple public services, each at its own subdomain.

```yaml
services:
  web:
    build: ./web
    port: 3000
    public: true                      # https://web.myapp.dibbla.com
  api:
    build: ./api
    port: 8080
    public: true                      # https://api.myapp.dibbla.com
```

When more than one service is public, each gets `https://<service>.<alias>.dibbla.com` and the alias-only URL `https://<alias>.dibbla.com` 301-redirects to the alphabetically-first public service. If you want a specific service to own the bare alias, use a custom domain (§ 14).

Backwards compat: a single-public deploy still serves at `https://<alias>.dibbla.com` directly — no per-service prefix.

---

## 14. Custom domains

A service can claim a custom hostname via `domain:`:

```yaml
services:
  web:
    build: ./web
    port: 3000
    public: true
    domain: api.example.com
```

The Ingress is rendered with that hostname and the platform's TLS issuer takes care of cert provisioning. **DNS is your job**:

- Create a `CNAME` from `api.example.com` → the platform's ingress hostname (the platform operator publishes the target — usually `<region>.ingress.dibbla.com`).
- Once DNS is live, the cert issuer issues a Let's Encrypt cert (HTTP-01 by default; DNS-01 if the operator has configured a DNS provider).
- `https://<alias>.dibbla.com` is preserved in addition to the custom domain.

Multiple custom domains for the same service aren't supported in v1 (one `domain:` per service). For wildcard or apex domains, talk to the platform operator about the DNS-01 path — apex `example.com` needs a DNS-01 challenge because most registrars don't support `ALIAS`/`ANAME` at the apex.

---

## 15. Cron jobs (`jobs:`)

Top-level `jobs:` declares scheduled jobs. They render to K8s CronJob objects.

```yaml
jobs:
  nightly-cleanup:
    schedule: "0 2 * * *"             # standard cron, UTC
    image: alpine:3.20
    command: [sh, -c, "/usr/local/bin/cleanup.sh"]
    environment:
      RETENTION_DAYS: "30"
    cpu: 200m
    memory: 256Mi
  hourly-warm-cache:
    schedule: "0 * * * *"
    build: ./warmer                   # build a job image from the archive
    successful_jobs_history_limit: 1  # default 3
    failed_jobs_history_limit: 3      # default 1
```

Rules:

- `schedule` is a standard cron expression in UTC; quote it to keep YAML happy with the `*`s.
- One of `image` or `build` (same rules as services).
- `command:` overrides the image's `CMD`.
- `environment:` is flat (no per-env form).
- Job names follow service-name regex (`^[a-z][a-z0-9-]{0,29}$`) and share namespace with services — a job and a service can't have the same name.
- History limits default to K8s defaults (`3` successful, `1` failed). Override per-job for noisy crons.
- A cron-only deploy (no `services:`) is allowed if `--no-public` is passed; otherwise the validator rejects it (`PUBLIC_SERVICE_MISSING`).

---

## 16. Build-time secrets

Some `docker build`s need a secret — a private npm token, a Codeartifact creds file, a registry pull token. These must NOT land in the image layer (anyone with the image can extract them).

```yaml
services:
  web:
    build:
      context: ./web
      dockerfile: Dockerfile
      secrets:
        - id: npm_token
          source: NPM_TOKEN_SECRET    # name of the secret in dibbla secrets
```

In your Dockerfile, use BuildKit's `--mount=type=secret`:

```dockerfile
RUN --mount=type=secret,id=npm_token \
    NPM_TOKEN=$(cat /run/secrets/npm_token) npm ci
```

The platform mounts the secret value into the BuildKit Solve via the named id; the value never lands in the image layer. You provide the value via `dibbla secrets set NPM_TOKEN_SECRET <value>` (deployment-wide, since builds happen before per-service routing).

- `id` is the BuildKit identifier referenced in the Dockerfile (`--mount=…,id=<id>`).
- `source` is the name of the secret in the dibbla secrets store. Per-service build secrets are not supported in v1 — the build is one operation per service, and the secret is scoped to that build.
- The secret must exist before the deploy; otherwise `BUILD_FAILED` with a message naming the missing secret.

---

## 17. Schema validation

Two layers, two purposes:

| Tool | What it checks | When to use |
|---|---|---|
| `dibbla manifest validate` | Local parse + schema (version, service names, build/image XOR, image-must-have-tag, port range) | CI, pre-commit hooks, editor integrations — fast, no network |
| `dibbla preview` | Everything `manifest validate` does + env-aware resolution + profile filter + quota check + (some) cross-service references | Before a real deploy when you want a server-authoritative answer |

Error codes (subset — full set in `reference.md`):

| Code | Meaning |
|---|---|
| `MANIFEST_AMBIGUOUS` | Both `dibbla.yaml` and `dibbla.yml` exist; remove one |
| `MANIFEST_INVALID` | Schema violation (parse error, version, build+image both set, image without tag, …) |
| `MANIFEST_UNSUPPORTED` | Reserved key the schema knows about but doesn't accept yet |
| `SERVICE_NAME_INVALID` | Service name fails regex or is reserved |
| `BUILD_CONTEXT_MISSING` | `build.context` doesn't exist in the archive |
| `DOCKERFILE_MISSING` | `build.dockerfile` doesn't exist at the resolved path |
| `PUBLIC_SERVICE_MISSING` | No `public: true` and `--no-public` not set |
| `PUBLIC_MISSING_PORT` | A `public: true` service has no `port:` |
| `QUOTA_EXCEEDED` | Resolved set exceeds an org quota (services, replicas, CPU, memory, PVC size) |
| `BUILD_FAILED` | A build step failed (missing build secret, Dockerfile error, …) |
| `DEPLOY_IN_PROGRESS` | Another deploy is in-flight for this alias; wait or cancel |
| `PATCH_AMBIGUOUS` | `dibbla apps update --replicas N` against a multi-service deploy |

Local validation cannot detect everything: env-aware errors (e.g. `replicas: { prod: -1 }`) and quota violations are server-side. A manifest that passes `dibbla manifest validate` may still fail `dibbla preview`.

---

## 18. Quotas

Default org quotas (the platform operator can override):

| Quota | Default | Field |
|---|---|---|
| Max services per deploy | 8 | service count after profile filter |
| Max replicas per service | 10 | per-service `replicas` |
| Max replicas total | 20 | sum of `replicas` across services |
| Max CPU total | 8 | sum of `cpu` across services (cores) |
| Max memory total | 16Gi | sum of `memory` across services |
| Max PVC size per service | 10Gi | sum of `volumes[].size` per service |
| Max PVC size total | 50Gi | sum across services |
| Max manifest size | 64KB | the `dibbla.yaml` file itself |
| Max archive size | 50MB | the deploy upload (CLI cap) |

`QUOTA_EXCEEDED` errors carry a `path` like `services.worker.replicas` and a `detail` like `replicas 12 exceeds per-service max 10`. Override at the org level by talking to the platform operator.

---

## 19. Reserved keys

The schema accepts these top-level keys at parse time but rejects them at validate time with `MANIFEST_UNSUPPORTED`. They're held for future versions to avoid users squatting on names that will mean something specific.

- `volumes:` — top-level shared/named PVCs.
- `networks:` — explicit named networks (today every deploy has one default).
- `secrets:` — top-level secret declarations (today secrets live in the secrets store).
- `cron:` — superseded by top-level `jobs:`.
- `init:` — top-level init containers (use per-service `init:` instead).

If you set one of these the validator emits `MANIFEST_UNSUPPORTED at <key>` and the deploy fails. The fix is the per-service equivalent.

---

## 20. Worked example: web + worker + redis

Annotated, end-to-end. This is the canonical multi-service shape — copy and adapt.

```yaml
version: 1

services:
  web:
    build:
      context: ./web
      dockerfile: Dockerfile
      secrets:
        - id: npm_token
          source: NPM_TOKEN_SECRET
    port: 3000
    public: true
    domain: api.example.com           # optional custom domain
    replicas:
      default: 1
      prod: 3
    cpu:
      default: 250m
      prod: 1
    memory:
      default: 256Mi
      prod: 1Gi
    environment:
      default:
        LOG_LEVEL: info
        NODE_ENV: production
      prod:
        SENTRY_DSN: ${SENTRY_DSN}     # comes from a runtime secret of the same name
      staging:
        LOG_LEVEL: debug
    init:
      - name: migrate
        image: registry.example.com/migrate:v1
        command: [migrate, up]
        environment:
          DATABASE_URL: ${DATABASE_URL}
    healthcheck:
      liveness:
        http_get: { path: /healthz }
        period_seconds: 10
        failure_threshold: 3
      readiness:
        http_get: { path: /ready }
        period_seconds: 5
    depends_on: [redis]
    expose_to: []                     # public; pod-to-pod default open

  worker:
    build: ./worker
    replicas:
      default: 1
      prod: 2
    environment:
      LOG_LEVEL: info
      REDIS_URL: ${DIBBLA_SVC_REDIS_URL}
    depends_on: [redis]
    expose_to: [web]                  # only the web service can reach worker

  redis:
    image: redis:7
    port: 6379
    volumes:
      - path: /data
        size: 2Gi
    expose_to: [web, worker]

  mailcatcher:
    image: mailhog/mailhog:v1.0.1
    port: 8025
    profiles: [dev]                   # only deployed when --profile dev is passed

jobs:
  nightly-cleanup:
    schedule: "0 2 * * *"
    image: alpine:3.20
    command: [sh, -c, "/cleanup.sh"]
    environment:
      RETENTION_DAYS: "30"
```

Validate it locally:

```bash
dibbla manifest validate              # ./dibbla.yaml
```

Preview against staging:

```bash
dibbla preview --target-env staging
```

Preview against prod with mailcatcher activated:

```bash
dibbla preview --target-env prod --profile dev
```

Deploy to prod (no profiles → `mailcatcher` is skipped):

```bash
dibbla deploy --alias myapp --target-env prod -m "feat: ship multi-service"
```

Operate per-service afterwards:

```bash
dibbla logs myapp --service worker -f
dibbla apps restart myapp --service worker
dibbla secrets set NPM_TOKEN_SECRET <token> -d myapp           # build-time secret
dibbla secrets set SENTRY_DSN https://... -d myapp --service web
```

---

## Cross-references

- [reference.md](reference.md) — exhaustive flag tables for `dibbla manifest validate`, `dibbla preview`, the new `--target-env` / `--profile` / `--no-public` flags on `deploy`, and the per-service `--service` flag on `apps restart` / `logs` / `secrets`.
- [examples.md](examples.md) — runnable bash transcripts for each multi-service pattern (init container, healthcheck, custom domain, cron, build secret, etc.).
- [platform.md § 8.5](platform.md) — runtime contract under multi-service: discovery env vars, NetworkPolicy, public URL shape, what does and doesn't work compared to the single-Dockerfile path.
- [guardrails.md](guardrails.md) Check 6 — pre-deploy multi-service safety (quota fit, no `depends_on:` cycles, init exit, healthcheck thresholds, build-secret existence, multi-public confirmation).
