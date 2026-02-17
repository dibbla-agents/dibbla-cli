#!/bin/sh
# Dibbla CLI installer for macOS and Linux
# Usage: curl -fsSL https://raw.githubusercontent.com/dibbla-agents/dibbla-cli/main/install.sh | sh

set -e

REPO="dibbla-agents/dibbla-cli"
BINARY="dibbla"
INSTALL_DIR="/usr/local/bin"

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

# Check required dependencies
check_deps() {
    for dep in curl tar; do
        if ! command -v "$dep" >/dev/null 2>&1; then
            error "Missing required dependency: $dep"
            exit 1
        fi
    done
}

# Detect OS
detect_os() {
    OS="$(uname -s)"
    case "$OS" in
        Linux*)  OS="linux" ;;
        Darwin*) OS="darwin" ;;
        *)       error "Unsupported OS: $OS"; exit 1 ;;
    esac
}

# Detect architecture
detect_arch() {
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64|amd64)  ARCH="amd64" ;;
        arm64|aarch64) ARCH="arm64" ;;
        *)             error "Unsupported architecture: $ARCH"; exit 1 ;;
    esac
}

# Get latest release version from GitHub API
get_latest_version() {
    VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "$VERSION" ]; then
        error "Failed to fetch latest version."
        exit 1
    fi
    # Strip leading 'v' for the download URL
    VERSION_NUM="${VERSION#v}"
}

# Get version from installed binary
get_installed_version() {
    VERSION=$($INSTALL_DIR/$BINARY --version)
}

# Install with brew
install_with_brew() {
    info "Homebrew detected. Installing with brew..."
    brew tap dibbla-agents/tap
    brew install dibbla
    get_installed_version
}

# Download and install manually
install_manually() {
    get_latest_version
    ARCHIVE_NAME="${BINARY}_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE_NAME}"

    TMP_INSTALL_DIR=$(mktemp -d)
    trap 'rm -rf "$TMP_INSTALL_DIR"' EXIT

    info "Downloading ${BINARY} ${VERSION} for ${OS}/${ARCH}..."
    curl -fsSL "$DOWNLOAD_URL" -o "${TMP_INSTALL_DIR}/${ARCHIVE_NAME}"

    info "Extracting..."
    tar -xzf "${TMP_INSTALL_DIR}/${ARCHIVE_NAME}" -C "$TMP_INSTALL_DIR"

    # Install binary
    if [ -w "$INSTALL_DIR" ]; then
        mv "${TMP_INSTALL_DIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    else
        info "Installing to ${INSTALL_DIR} (requires sudo)..."
        sudo mv "${TMP_INSTALL_DIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    fi

    chmod +x "${INSTALL_DIR}/${BINARY}"
}

# Verify installation
verify() {
    if command -v "$BINARY" >/dev/null 2>&1; then
        ok ""
        ok "  dibbla ${VERSION} installed successfully!"
        ok ""
        info "  Get started:"
        printf "    dibbla create go-worker my-project\n"
        printf "\n"
    else
        warn ""
        warn "  Installed to ${INSTALL_DIR}/${BINARY}, but it's not in your PATH."
        warn ""
        warn "  Add this to your shell profile (.bashrc, .zshrc, etc.):"
        printf "    export PATH=\"%s:\$PATH\"\n" "$INSTALL_DIR"
        printf "\n"
    fi
}

main() {
    printf "\n"
    info "  Dibbla CLI Installer"
    info "  --------------------"
    printf "\n"

    check_deps
    detect_os

    if [ "$OS" = "darwin" ] && command -v brew >/dev/null 2>&1; then
        install_with_brew
    else
        detect_arch
        install_manually
    fi

    verify
}

main
