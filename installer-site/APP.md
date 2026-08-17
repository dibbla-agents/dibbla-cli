---
subtitle: Install the Dibbla command-line tool on your computer.
---

# Installing the Dibbla CLI

This is the download service behind **install.dibbla.com**. It hands your
computer the `dibbla` command-line tool — the thing you use to put an app
online, look at its logs, and manage its data.

You don't browse this service. You point one command at it, and it does the rest.

## Install it

**macOS or Linux** — paste this into a terminal:

```
curl https://install.dibbla.com -fsS | sh
```

**Windows** — paste this into PowerShell:

```
irm https://install.dibbla.com/install.ps1 | iex
```

Then check that it worked:

```
dibbla --version
```

You should see a version number. If the command isn't found, open a new terminal
window and try again — the installer adds `dibbla` to your path, and existing
windows don't pick that up until they're restarted.

## Keeping it up to date

Run:

```
dibbla update
```

Re-running the install command above does the same thing, so either is fine.

## If you use an AI assistant

If you work with an AI coding assistant — Claude Cowork, Claude Code, Cursor and
so on — you can simply ask it to install the Dibbla CLI, and the command above is
the one it will use. It works from inside those assistants' sandboxes, which is
the reason this service exists: assistants often can't reach GitHub, so the
download comes from Dibbla directly instead.

## Is the download safe?

Yes, and you can check it yourself rather than taking our word for it. The
installer works out the checksum of the file it downloaded and compares it
against the published one before putting anything on your computer. If the two
don't match — a corrupted download, or something tampering in between — the
install stops and nothing is written.

## Something went wrong

**"Failed to fetch latest version"** — this service couldn't be reached. Check
your internet connection, then try again in a minute.

**"Checksum mismatch"** — the download arrived damaged. Nothing was installed.
Run the install command again; it almost always succeeds on the second try.

**"Neither sha256sum nor shasum is available"** — your system is missing the tool
needed to check the download. On macOS, install Homebrew and run
`brew tap dibbla-agents/tap && brew install dibbla` instead — the same command the
error message itself prints.

**Anything else** — copy the exact error message and send it to
support@dibbla.com. The message text is the useful part; a screenshot of the
whole window usually isn't.

## Older versions

This service always offers the newest release. If you specifically need an older
one, or a `.deb`/`.rpm` package for Linux, those live at
<https://github.com/dibbla-agents/dibbla-cli/releases>.
