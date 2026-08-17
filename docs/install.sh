#!/bin/sh
# Dibbla CLI installer for macOS and Linux
# Usage: curl https://install.dibbla.com -fsS | sh
#
# Installs to ~/.local/bin by default. Override with DIBBLA_INSTALL_DIR.
# Shell-config auto-edit can be skipped with DIBBLA_SKIP_PATH_SETUP=1.
# Prefer Homebrew instead? Run: brew tap dibbla-agents/tap && brew install dibbla

set -e

BINARY="dibbla"
INSTALL_DIR="${DIBBLA_INSTALL_DIR:-$HOME/.local/bin}"

# Every request this script makes goes to BASE_URL and nowhere else.
#
# It used to resolve the version from api.github.com and download from
# github.com. Both return 403 inside Anthropic-hosted agent sandboxes (Claude
# Cowork, Claude Code on the web) — the block is repository-scoped by Anthropic,
# not something we or a customer can allowlist away, and release assets on
# github.com additionally 302 to release-assets.githubusercontent.com, a third
# host. Dibbla's own domains are reachable from those sandboxes with no
# configuration, so the artefacts are mirrored here instead. See P-0020.
#
# DIBBLA_INSTALL_BASE_URL points the installer at a staging deploy or a test
# fixture; it is what makes the mirror verifiable before DNS moves onto it.
BASE_URL="${DIBBLA_INSTALL_BASE_URL:-https://install.dibbla.com}"
BASE_URL="${BASE_URL%/}"

# State shared between configure_path() and verify()
PATH_UPDATED_FILE=""
PATH_ALREADY_PATCHED=0

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

info()  { printf "${CYAN}%s${NC}\n" "$1"; }
ok()    { printf "${GREEN}%s${NC}\n" "$1"; }
warn()  { printf "${YELLOW}%s${NC}\n" "$1"; }
error() { printf "${RED}%s${NC}\n" "$1"; }

check_deps() {
    for dep in curl tar; do
        if ! command -v "$dep" >/dev/null 2>&1; then
            error "Missing required dependency: $dep"
            exit 1
        fi
    done
}

detect_os() {
    OS="$(uname -s)"
    case "$OS" in
        Linux*)  OS="linux" ;;
        Darwin*) OS="darwin" ;;
        *)       error "Unsupported OS: $OS"; exit 1 ;;
    esac
}

detect_arch() {
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64|amd64)  ARCH="amd64" ;;
        arm64|aarch64) ARCH="arm64" ;;
        *)             error "Unsupported architecture: $ARCH"; exit 1 ;;
    esac
}

get_latest_version() {
    VERSION=$(curl -fsSL "${BASE_URL}/latest.json" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "$VERSION" ]; then
        error "Failed to fetch latest version from ${BASE_URL}/latest.json"
        exit 1
    fi
    # Strip leading 'v' for the download URL
    VERSION_NUM="${VERSION#v}"
}

# Verify the downloaded archive against the mirror's checksums.txt.
#
# This is new, and it closes a real gap: until now a first install downloaded
# and extracted a binary with no verification at all. The delegation comment
# further down used to say that was unavoidable — "both things this bootstrap
# script can't do on its own" — and it was, because checksums.txt was one more
# GitHub asset behind the same 403. Now that it is served from the same origin
# as the archive, verification costs a handful of lines.
#
# Deliberately fail-closed with no opt-out: on a mismatch, on a missing entry,
# and on a machine with neither sha256sum nor shasum. A silent skip is how a
# security control turns into decoration. The package-manager installs
# (brew/apt/rpm) remain the answer for anything too minimal for either tool.
verify_checksum() {
    _archive_path="$1"
    _archive_name="$2"
    _sums="${TMP_INSTALL_DIR}/checksums.txt"

    if ! curl -fsSL "${BASE_URL}/checksums.txt" -o "$_sums"; then
        error "Failed to fetch ${BASE_URL}/checksums.txt — refusing to install unverified."
        exit 1
    fi

    # goreleaser writes "<sha256>  <name>"; the name is occasionally prefixed
    # with "*" for binary mode.
    _expected=$(awk -v n="$_archive_name" '{ sub(/^\*/, "", $2); if ($2 == n) { print $1; exit } }' "$_sums")
    if [ -z "$_expected" ]; then
        error "checksums.txt has no entry for ${_archive_name} — refusing to install unverified."
        exit 1
    fi

    if command -v sha256sum >/dev/null 2>&1; then
        _actual=$(sha256sum "$_archive_path" | cut -d' ' -f1)
    elif command -v shasum >/dev/null 2>&1; then
        _actual=$(shasum -a 256 "$_archive_path" | cut -d' ' -f1)
    else
        error "Neither sha256sum nor shasum is available — cannot verify the download."
        error "Install one, or use a package manager: brew tap dibbla-agents/tap && brew install dibbla"
        exit 1
    fi

    if [ "$_expected" != "$_actual" ]; then
        error "Checksum mismatch for ${_archive_name}!"
        error "  expected ${_expected}"
        error "  actual   ${_actual}"
        error "Refusing to install. Nothing was written to ${INSTALL_DIR}."
        exit 1
    fi

    ok "  Verified SHA-256."
}

install_dibbla() {
    get_latest_version
    ARCHIVE_NAME="${BINARY}_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
    DOWNLOAD_URL="${BASE_URL}/latest/${ARCHIVE_NAME}"

    TMP_INSTALL_DIR=$(mktemp -d)
    trap 'rm -rf "$TMP_INSTALL_DIR"' EXIT

    info "Downloading ${BINARY} ${VERSION} for ${OS}/${ARCH}..."
    curl -fsSL "$DOWNLOAD_URL" -o "${TMP_INSTALL_DIR}/${ARCHIVE_NAME}"

    info "Verifying..."
    verify_checksum "${TMP_INSTALL_DIR}/${ARCHIVE_NAME}" "$ARCHIVE_NAME"

    info "Extracting..."
    tar -xzf "${TMP_INSTALL_DIR}/${ARCHIVE_NAME}" -C "$TMP_INSTALL_DIR"

    # Make executable in the temp dir so the mv delivers a ready-to-run binary.
    chmod +x "${TMP_INSTALL_DIR}/${BINARY}"

    mkdir -p "$INSTALL_DIR"
    mv "${TMP_INSTALL_DIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
}

# Ensure INSTALL_DIR is on PATH for future shells by appending a marked block
# to the user's shell rc file. Idempotent across re-runs (e.g. the update-dibbla
# task step). Opt out with DIBBLA_SKIP_PATH_SETUP=1.
configure_path() {
    # Already on the parent shell's PATH — no edit needed. Typical on Linux
    # desktop distros that ship ~/.local/bin in ~/.profile by default.
    case ":$PATH:" in
        *":${INSTALL_DIR}:"*) return 0 ;;
    esac

    if [ "${DIBBLA_SKIP_PATH_SETUP:-}" = "1" ]; then
        return 0
    fi

    shell_name=$(basename "${SHELL:-/bin/sh}")
    case "$shell_name" in
        zsh)
            rc_file="$HOME/.zshrc"
            path_line="export PATH=\"$INSTALL_DIR:\$PATH\""
            ;;
        bash)
            # macOS Terminal launches bash as a login shell → .bash_profile.
            # On Linux, write to .profile: login shells source it (SSH, desktop
            # session managers, `bash -l`), and most distros' default .bashrc
            # starts with `[ -z "$PS1" ] && return`, which makes an appended
            # PATH line unreachable from non-interactive contexts.
            if [ "$OS" = "darwin" ]; then
                rc_file="$HOME/.bash_profile"
            else
                rc_file="$HOME/.profile"
            fi
            path_line="export PATH=\"$INSTALL_DIR:\$PATH\""
            ;;
        fish)
            rc_file="$HOME/.config/fish/config.fish"
            path_line="fish_add_path $INSTALL_DIR"
            ;;
        *)
            rc_file="$HOME/.profile"
            path_line="export PATH=\"$INSTALL_DIR:\$PATH\""
            ;;
    esac

    mkdir -p "$(dirname "$rc_file")"
    touch "$rc_file"

    if grep -Fq ">>> dibbla installer >>>" "$rc_file" 2>/dev/null; then
        PATH_UPDATED_FILE="$rc_file"
        PATH_ALREADY_PATCHED=1
        return 0
    fi

    {
        printf '\n'
        printf '# >>> dibbla installer >>>\n'
        printf '# Added by the dibbla CLI installer. Remove this block to opt out.\n'
        printf '%s\n' "$path_line"
        printf '# <<< dibbla installer <<<\n'
    } >> "$rc_file"

    PATH_UPDATED_FILE="$rc_file"
}

verify() {
    # Prepend INSTALL_DIR to this script's PATH so our own check below is
    # trustworthy even when the parent shell doesn't have it yet.
    case ":$PATH:" in
        *":${INSTALL_DIR}:"*) ;;
        *) PATH="$INSTALL_DIR:$PATH" ;;
    esac

    if ! command -v "$BINARY" >/dev/null 2>&1; then
        error ""
        error "  dibbla installed to ${INSTALL_DIR}/${BINARY} but is not executable."
        error "  Check file permissions and try running it directly."
        exit 1
    fi

    ok ""
    ok "  dibbla ${VERSION} installed successfully!"
    ok ""

    if [ "${DIBBLA_SKIP_PATH_SETUP:-}" = "1" ]; then
        warn "  Skipped updating shell config (DIBBLA_SKIP_PATH_SETUP=1)."
        warn "  Make sure ${INSTALL_DIR} is on your PATH to use dibbla."
        printf "\n"
        return
    fi

    if [ -z "$PATH_UPDATED_FILE" ]; then
        # Already on PATH in the parent shell — nothing was edited.
        info "  Get started:"
        printf "    dibbla init\n"
        printf "\n"
        info "  Tip: teach AI coding agents about the CLI by running in your project:"
        printf "    dibbla skills install dibbla\n"
        printf "\n"
        return
    fi

    if [ "$PATH_ALREADY_PATCHED" = "1" ]; then
        info "  ${INSTALL_DIR} is already configured in ${PATH_UPDATED_FILE}."
    else
        info "  Added ${INSTALL_DIR} to PATH in ${PATH_UPDATED_FILE}."
    fi
    printf "\n"
    info "  To use dibbla in this terminal right now, run:"
    printf "    export PATH=\"%s:\$PATH\"\n" "$INSTALL_DIR"
    printf "\n"
    info "  New terminal windows will work automatically."
    printf "\n"
    info "  Tip: teach AI coding agents about the CLI by running in your project:"
    printf "    dibbla skills install dibbla\n"
    printf "\n"
}

# First tag that shipped `dibbla update` (internal/cmd/update/update.go).
# Only delegate to the existing binary's self-update when it's at least
# this version; anything older predates the command.
MIN_UPDATE_VERSION="1.2.10"

# If a working dibbla is already on PATH and is new enough to know the
# `update` subcommand (v1.2.10+), delegate to it. That path goes through
# install-method detection, so it refuses to clobber a brew/apt install —
# still the thing this bootstrap script cannot do on its own. (SHA-256
# verification used to be on that list too; since P-0020 mirrored
# checksums.txt onto the same origin as the archive, verify_checksum()
# above does it here as well.) We gate on the parsed `--version` number
# (kept in lockstep with install.ps1, which needs the version gate to
# avoid an stderr-on-Stop trap); a dev or unparseable build falls through
# to a fresh install, which is always safe.
#
# Set DIBBLA_INSTALLER_FORCE=1 to skip delegation when:
#   - the existing dibbla update is broken and you need to reinstall fresh
#   - you want to install into a different DIBBLA_INSTALL_DIR
maybe_delegate_to_update() {
    if [ "${DIBBLA_INSTALLER_FORCE:-}" = "1" ]; then
        return 0
    fi

    existing=$(command -v dibbla 2>/dev/null || true)
    if [ -z "$existing" ]; then
        return 0
    fi

    # `dibbla --version` prints "dibbla version X.Y.Z" to stdout; pull the
    # first X.Y.Z triple. Empty for a dev/unparseable build.
    ver=$("$existing" --version 2>/dev/null | sed -n 's/.*[^0-9]\([0-9]*\.[0-9]*\.[0-9]*\).*/\1/p' | head -n1)
    if [ -n "$ver" ] && [ "$(printf '%s\n%s\n' "$MIN_UPDATE_VERSION" "$ver" | sort -V | head -n1)" = "$MIN_UPDATE_VERSION" ]; then
        printf "\n"
        info "  Found existing dibbla $ver at $existing — delegating to 'dibbla update'."
        info "  (set DIBBLA_INSTALLER_FORCE=1 to reinstall from scratch instead.)"
        printf "\n"
        exec "$existing" update --yes
    fi
}

main() {
    printf "\n"
    info "  Dibbla CLI Installer"
    info "  --------------------"
    printf "\n"

    maybe_delegate_to_update

    check_deps
    detect_os
    detect_arch
    install_dibbla
    configure_path
    verify
}

main
