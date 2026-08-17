# Notes for Claude Code (and other agents)

This file captures non-obvious project conventions an agent should know
before editing code here. Code-level patterns live in the code itself —
this is for cross-cutting rules.

## Skill files: two copies, one source of truth

The dibbla skill markdown is duplicated by design:

- `.claude/skills/dibbla/*.md` — the **source** that humans edit. Also
  the location that other Claude Code instances read directly when this
  repo is opened.
- `internal/cmd/skills/assets/dibbla/*.md` — a **generated copy** that
  `go:embed` bakes into the released binary. This is what `dibbla skills
  install dibbla` writes into a user's project.

The two are kept in lockstep by `go generate`. The directives live in
`internal/cmd/skills/assets.go` and `internal/cmd/root.go`.

**Rule:** after editing anything under `.claude/skills/dibbla/` or the
root `SKILL.md`, run:

```bash
go generate ./...
```

and commit the regenerated files alongside the source edits. The
`Skill sync check` GitHub workflow (`.github/workflows/skill-sync-check.yml`)
runs on every PR that touches skill files and fails the build if `go
generate` would change anything — meaning someone forgot this step.

## Skill publishing contract

`.github/workflows/publish-skill.yml` mirrors the embedded skill
(`internal/cmd/skills/assets/dibbla/`, **not** the editable source dir)
to the public `dibbla-agents/skills` repo. Trigger: **only on `v*` tag
push**. The mirror gets a tag with the same name as the CLI release.

The contract this gives consumers: for every `vX.Y.Z` tag in
`dibbla-cli`, there's a `vX.Y.Z` tag in `dibbla-skills` whose contents
match exactly the skill files embedded in the CLI binary at that
version. `dibbla skills install dibbla` from `dibbla-cli@vX.Y.Z` produces
files identical to checking out `dibbla-skills@vX.Y.Z`.

In-flight skill changes on `main` (between releases) do **not** reach
the public repo until the next CLI tag is cut. Don't rely on the public
repo as a development mirror.

### Skill content is public-facing — write it that way

`dibbla-agents/skills` is a **public** GitHub repo, and P-0016 additionally
serves those same bytes from `https://dibbla.com/.well-known/agent-skills/`.
Anything you put under `.claude/skills/dibbla/` or in the root `SKILL.md` is
therefore published to the open internet at the next `v*` tag.

Concretely, when editing skill files:

- **Use `dibbla.com` hosts in examples, never `dibbla.net`.** `*.dibbla.net` is
  staging. Publishing it maps internal infrastructure, and it is a support
  problem on its own — readers paste the example and hit staging. Note the
  portal is `app.dibbla.com` in prod (there is no `auth.dibbla.com`).
- **Never name a customer, partner, or internal-only host.** Use a placeholder
  (`api.your-domain.com`, `workflow-server.<internal>`).
- **Don't document internal transport details** — DIB-188 removed a line stating
  that an internal DB proxy runs with `sslmode=disable`.
- **No personal email addresses.**

DIB-188 scrubbed all of the above; keep it clean rather than re-running a scrub.

## Releases are tag-driven

Pushing a `v*` tag triggers two workflows in parallel:

- `release.yml` — builds, signs and publishes the release (see below).
- `publish-skill.yml` — mirrors the skill to `dibbla-agents/skills` and
  tags it.

Don't push tags casually — they're public events with consumer-facing
side effects. Confirm with the user before tagging.

### `release.yml` job order is load-bearing

```
release ──┬── sign-macos ──┬── checksums ──┬── mirror ───┐
          └── sign-windows ┘               └── homebrew ─┼── notify
```

Three rules hold this together; breaking any of them has bitten us before:

1. **Nothing touches the Homebrew tap until after signing.** GoReleaser
   used to push the tap itself, at the end of the `release` job. When
   `HOMEBREW_TAP_GITHUB_TOKEN` expired, that failed the `release` job and
   *skipped both signing jobs* — v1.2.47 through v1.2.51 all shipped
   unsigned macOS and Windows binaries, with no skill archive attached and
   the tap frozen at v1.2.46. The tap is now the last thing to run and
   cannot take anything else down with it.

2. **`checksums.txt` has exactly one writer.** Both signing jobs used to
   download, regex-patch and re-upload the shared file, which is why
   sign-windows had to wait on sign-macos. The `checksums` job now
   regenerates it wholesale from the published assets once signing is
   done, so the signing jobs run in parallel.

3. **Anything that republishes artifacts hangs off `checksums`, never off
   `release`.** `mirror` and `homebrew` are siblings for that reason: both
   copy the final, signed bytes together with the checksums that match them.
   A mirror built from `release` would serve pre-signing darwin/windows
   archives with post-signing checksums — the exact bug the comment in
   `.goreleaser.yml` records for the old Homebrew step.

The Homebrew formula is rendered from `.github/homebrew/dibbla.rb.tmpl`,
**not** by GoReleaser — its `brews` feature is deprecated upstream, and it
could only ever see the pre-signing checksums. Edit the template here; the
copy in the tap is generated and carries a DO-NOT-EDIT header.

### The `mirror` job publishes install.dibbla.com

`install.dibbla.com` is a Dibbla-hosted app, not GitHub Pages. It serves the
two bootstrap scripts **and** the release archives, `checksums.txt` and a
`latest.json` version endpoint, so installing the CLI touches no host but our
own — which is what makes the CLI installable inside AI coding-agent sandboxes
(Claude Cowork, Claude Code on the web), where GitHub returns 403. The app
source is `installer-site/`; see its README. Proposal P-0020.

Three things about it that are easy to get wrong:

- **The mirror serves the latest release only**, on the platform's 50 MB upload
  limit. `.deb`/`.rpm` and every historical version stay on GitHub Releases,
  which remains the archive of record. This is why a pinned
  `dibbla update --version <tag>` still resolves against the GitHub API while
  an unpinned one does not.
- **`installer-site/public/` is generated, never committed.** Both CI and a
  hand-run cutover go through `installer-site/stage.sh <tag> [base-url]`, and
  the Dockerfile fails the build if it was not staged.
- **A prerelease publishes nothing here**, same `!contains(github.ref_name, '-')`
  guard as `homebrew`: `latest.json` and `/latest/` must move together or not
  at all, and moving `latest` to an rc would hand every new install a release
  candidate.

Also note the circularity, which is fine but surprising: the `mirror` job
installs the CLI *from the mirror it is about to republish*. GitHub Actions
runners can reach GitHub, so even a broken mirror leaves that step working via
the previous release — and it means the job exercises the installer it ships.

### Go version is pinned in go.mod, not in the workflows

No workflow hardcodes a Go version — all three use `setup-go` with
`go-version-file: go.mod`. The single `go` directive therefore does two jobs:
it is the **compatibility floor** for `go install .../cmd/dibbla@latest` *and*
the version CI and the release build install. To move CI, bump it — never add
a version to a workflow file.

**Do not add a `toolchain` directive.** It is the natural way to separate
"floor" from "what we build with", and it does work with `setup-go`, which
reads it in preference to `go`. But Snyk's go.mod parser rejects it: the PR
security check errors with `Failed to process go.mod` and reports
`state: error` with no vulnerability detail, which reads like a real security
finding and cost a long detour to diagnose. This was tried and reverted —
don't rediscover it.

The cost is that dev machines may run a newer Go than CI. That is fine as long
as the format gate agrees across both; gofmt 1.25.13 and 1.26.6 were verified
to agree on this tree. Re-check if that gap widens.

The floor is not a free choice — it is pinned by the dependency graph.
`golang.org/x/crypto` carried 13 CVEs whose fixes all land in versions
declaring `go 1.25.0`, so 1.24 could not be held without shipping them.
Expect the same pressure next time: check what the patched `x/*` modules
require before assuming the floor can stay put. `govulncheck ./...` is the
tool to confirm reachability — Snyk's PR check reports on presence alone, so
the two will disagree and govulncheck is the one that reflects real exposure.

One trap: **`gofmt` is a standalone binary and ignores `GOTOOLCHAIN`**, so a
system `gofmt` of a different vintage can disagree with the CI gate. Use
`go fmt ./...` locally, which routes through the `go` command and therefore
the module-resolved toolchain. gofmt's doc-comment normalisation is the part that
actually differs between releases — it rewrites `''` and double-backticks in
doc comments into typographic quotes, which once silently corrupted a SQL
snippet in `internal/secrets/secrets.go`.

### Secrets the release needs

`HOMEBREW_TAP_GITHUB_TOKEN` (PAT with write access to
`dibbla-agents/homebrew-tap`) is the one with a history of silently
expiring. The `homebrew` job now preflights it and fails with an explicit
rotate-the-secret message instead of burying a 401 in GoReleaser output.

`SLACK_BOT_URL` and `SLACK_CLUSTER_DNS` hold the notification endpoint and
the DNS name pinned via `curl --resolve`. They're secrets rather than
inline literals because this repo is public — same reasoning as the skill
content rule above. If either is unset the notify step skips cleanly.

## Self-update flow

`dibbla update` is the user-facing self-upgrader (added in v1.2.10). It
detects the install method (Homebrew/apt/rpm/scoop/choco/script) and
either prints the right package-manager command or self-replaces the
binary with checksum verification. The install script at
`docs/install.sh` delegates to `dibbla update` when an existing binary
on `PATH` recognizes the `update` subcommand. See `internal/cmd/update/`
for the implementation.

Since P-0020 the bootstrap scripts verify SHA-256 themselves, so the
delegation is no longer about verification — it is about install-method
detection, i.e. refusing to clobber a brew/apt install. The comments in
both installers used to say verification was something a bootstrap script
"can't do on its own"; that was true only while `checksums.txt` lived
behind the same GitHub 403 as the archive. Don't restore the old claim.

Resolution is asymmetric on purpose: an unpinned update reads
`install.dibbla.com/latest.json`, a pinned `--version <tag>` still reads
the GitHub API, because the mirror is latest-only. `internal/update`
holds two overridable base URLs (`mirrorBaseURL`, `githubAPIBaseURL`) for
exactly this reason.

## `dibbla init` first-run wizard

`dibbla init` (added in v1.2.11) orchestrates `update` → `login` →
`skills install dibbla` as **subprocesses** of the running binary, not
in-process calls. The reason: when `update` self-replaces, the next
subprocess automatically picks up the new binary on disk. See
`internal/cmd/initcmd/`.
