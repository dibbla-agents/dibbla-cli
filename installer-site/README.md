# `installer-site` — the origin behind `install.dibbla.com`

A Caddy static app that serves the CLI bootstrap scripts **and** the release
archives, so installing the CLI touches no host but Dibbla's own.

## Why this exists

`install.dibbla.com` used to be GitHub Pages serving two scripts, and those
scripts then fetched the version list from `api.github.com` and the binary from
`github.com/.../releases/download/...`. Inside Anthropic-hosted agent sandboxes
(Claude Cowork, Claude Code on the web) the script downloads fine while **both
GitHub calls return 403** — so `curl … | sh` dies at
`Failed to fetch latest version.` and the CLI cannot be installed.

That 403 is Anthropic's own repository-scoped message, not a domain allowlist, so
it cannot be fixed by allowlisting `install.dibbla.com`: the blocked host is
GitHub's. Dibbla's own domains are already reachable from those sandboxes with no
configuration. Hence this app. See proposal **P-0020**.

GitHub Releases keeps publishing exactly as before — this mirror is additive, and
remains the archive of record for `.deb`/`.rpm` and for version-pinned
downloads.

## What it serves

```
/                     → install.sh   (the bare origin has always answered with the script)
/install.sh
/install.ps1
/latest.json          → { tag_name, assets: [{ name, browser_download_url, size, digest }] }
/checksums.txt        → verbatim from the GitHub release
/latest/dibbla_<ver>_<os>_<arch>.tar.gz   (+ .zip for windows)
```

**Latest version only.** All 12 release assets are 44.7 MB against the
platform's 50 MB `MAX_ARCHIVE_SIZE_MB`, which one new architecture would break;
the six archives plus `checksums.txt` are ~27 MB. More than one version does not
fit. This is a deliberate scope limit, not an oversight — see P-0020's
*Alternatives* for the reverse-proxy design that removes it.

## Publishing

`public/` is **generated, never committed**. Both callers go through one script:

```bash
./stage.sh v1.2.54                          # for install.dibbla.com
./stage.sh v1.2.54 http://localhost:8080    # for a local test origin
```

The second argument is baked into `latest.json`'s `browser_download_url` values,
because `internal/update` downloads whatever URL that field names — it must be
the origin the client will actually reach.

In CI this is the `mirror` job in `.github/workflows/release.yml`. It is a
**sibling of `homebrew`, both `needs: checksums`** — that ordering is
load-bearing: `checksums` regenerates `checksums.txt` from the re-signed macOS
and Windows archives, so a mirror built off `release` instead would publish
pre-signing binaries with post-signing checksums. Do not reorder.

## Locally

```bash
./stage.sh v1.2.53 http://localhost:8080
docker build -t installer-site .
docker run --rm -p 8080:80 installer-site

DIBBLA_INSTALL_BASE_URL=http://localhost:8080 sh public/install.sh
```

The `docker build` fails if `public/` was not staged — better a loud build error
than a deployed site that 404s every install.

## Verifying a deploy

`dibbla deploy --update` is a rolling replace: it returns when the platform has
accepted the new revision, not when the old pod has stopped answering. Asserting
immediately after it returns races the restart and gets a 502 from the edge —
which is exactly what failed `mirror-redeploy.yml`'s first run while the deploy
itself was fine.

So both workflows gate on `./wait-ready.sh <base-url> <tag>` before asserting
anything. It waits for **three consecutive** reads of `latest.json` serving the
expected tag, cache-busted, because a single 200 can come from a pod that is
about to be replaced. Use it by hand too:

```bash
./wait-ready.sh https://install.dibbla.com v1.2.54
```

## Deploying by hand

Normally CI does this. For a cutover or a fix:

```bash
./stage.sh <tag>
dibbla deploy . --alias install --update -m "…"
```

`--update`, never `--force`: `--force` tears the app down, and this app is on the
critical path for every new install — including `dibbla-docs`' own deploy job,
which installs the CLI from `install.dibbla.com/install.sh`.

## Notes for whoever changes this next

- **Do not rename `public/` to `dist/`.** The deploy extractor silently strips
  `dist/` (along with `node_modules/`, `vendor/`, `.next/`, …), so the app would
  deploy empty with no error anywhere.
- **Nothing may redirect off-origin.** Release assets on `github.com` 302 to
  `release-assets.githubusercontent.com`, and that cross-origin hop is one of the
  things that breaks in a sandbox. The `mirror` job asserts `num_redirects == 0`
  on an archive download for exactly this reason.
- **Archives are not `encode`d.** Re-compressing an already-compressed archive
  would switch the response to chunked transfer and drop `Content-Length`.
- **`latest.json` is short-TTL, archives are immutable.** Archive URLs carry the
  version, so their bytes never change; `latest.json` must not be cached hard or
  a stale copy pins users to a version `/latest/` no longer holds.
- **The hostname is claimed in Terraform**, not by the manifest's `domain:`
  field. `PublicHostname` (deploy-api `render.go`) returns *only* the custom
  domain, so `domain:` would destroy the `install-*.dibbla.app` alias URL — the
  URL used to verify a deploy before DNS moves onto it, and the rollback target.
  `download.dibbla.com` and `docs.dibbla.com` are both done the same way, in
  `terra-dibbla/environments/prod/main.tf`.
