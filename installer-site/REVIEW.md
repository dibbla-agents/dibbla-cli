# Pre-deploy review — `install` app

Review-status: Ok
Reviewed: 2026-08-17
Proposal: P-0020
Reviewer: cc session on pine (implementing IMPL-0015)

## What this app is

A static-file origin for `install.dibbla.com`: two bootstrap scripts, the six
release archives for the latest tag, `checksums.txt`, and a `latest.json`
version endpoint. No application logic, no user input, no database, no secrets,
no outbound calls.

## Public exposure — intended, and reviewed as such

The service is **public with no auth**, which is correct: it is an installer
endpoint that must answer anonymous requests from anywhere, including CI runners
and agent sandboxes. There is no admin UI, debug console, mail catcher or
internal dashboard behind it — the usual reason a public service is a mistake
does not apply.

What was checked because it *is* public:

- **No directory listing.** Caddy's `file_server` has `browse` off by default and
  it is not enabled, so `/latest/` returns 404 rather than an index.
- **No off-origin redirects.** Deliberate, and asserted in CI
  (`num_redirects == 0`): a cross-origin hop to
  `release-assets.githubusercontent.com` is one of the things that breaks in the
  sandboxes this app exists to serve.
- **Nothing writable.** The image is a read-only static tree; there is no upload
  path, no form handler and no proxying.
- **No secrets in the image.** `public/` contains only published release
  artefacts — bytes that are already public on GitHub Releases — plus the two
  scripts from `docs/`.
- **Integrity is verifiable by the client.** `latest.json` carries a `sha256:`
  digest per asset and `checksums.txt` is served verbatim from the GitHub
  release, so a client can detect tampered bytes. The installers now do exactly
  that, fail-closed.

## Container

`caddy:2-alpine`, listening on `:80`, matching the `dibbla-docs` app on this
platform. TLS terminates at the Cloudflare edge and the tunnel forwards cleartext
to Traefik, so no in-cluster certificate is needed. Caddy binds :80 as root, the
same as the other Caddy app here; changing that would need
`CAP_NET_BIND_SERVICE` and is not worth diverging from the platform precedent for
a read-only static tree.

`caddy validate` runs during the image build, and the build fails outright if
`public/` was not staged — so a misconfiguration or an empty mirror cannot reach
production silently.

## Risks accepted

- **This app is on the critical path for every new CLI install**, including the
  `dibbla-docs` deploy job. Mitigated by verifying at the `install-*.dibbla.app`
  alias before DNS moves onto it, by keeping the DNS rollback a two-line
  Terraform revert of a record that already exists, and by the CI verification
  step that re-hashes the published bytes after every deploy.
- **Latest-version-only.** A user asking for a historical version is redirected
  (in documentation, not HTTP) to GitHub Releases. Deliberate scope limit on the
  50 MB upload budget.
