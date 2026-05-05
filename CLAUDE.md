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

## Releases are tag-driven

Pushing a `v*` tag triggers two workflows in parallel:

- `release.yml` — goreleaser builds cross-platform binaries, publishes a
  GitHub Release with `checksums.txt`, updates the Homebrew tap.
- `publish-skill.yml` — mirrors the skill to `dibbla-agents/skills` and
  tags it.

Don't push tags casually — they're public events with consumer-facing
side effects. Confirm with the user before tagging.

## Self-update flow

`dibbla update` is the user-facing self-upgrader (added in v1.2.10). It
detects the install method (Homebrew/apt/rpm/scoop/choco/script) and
either prints the right package-manager command or self-replaces the
binary with checksum verification. The install script at
`docs/install.sh` delegates to `dibbla update` when an existing binary
on `PATH` recognizes the `update` subcommand. See `internal/cmd/update/`
for the implementation.

## `dibbla init` first-run wizard

`dibbla init` (added in v1.2.11) orchestrates `update` → `login` →
`skills install dibbla` as **subprocesses** of the running binary, not
in-process calls. The reason: when `update` self-replaces, the next
subprocess automatically picks up the new binary on disk. See
`internal/cmd/initcmd/`.
