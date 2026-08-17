#!/usr/bin/env bash
# End-to-end test for P-0020: install the CLI with GitHub unreachable.
#
# This reproduces the exact failure observed in a Claude Cowork session on
# 2026-08-17 — the installer script downloads fine, then dies at "Failed to
# fetch latest version." because api.github.com and github.com both return 403.
# A passing run proves no code path in the installer reaches GitHub.
#
# GitHub is blackholed in /etc/hosts inside a clean container rather than
# simulated, so the test cannot pass by accident: if any step still needs
# github.com, it fails here.
#
# Modelled on the shell E2E convention in
# ../../../app-hosting-service/deploy-api/scripts/e2e/ (doc header, required vs
# optional env with :? guards, numbered steps, ✓ assertions, trap cleanup)
# rather than inventing a different one. dibbla-cli had no scripts/ directory
# before this.
#
# Optional env:
#   DIBBLA_INSTALL_BASE_URL — origin under test. Defaults to the real
#                             https://install.dibbla.com. Point it at an
#                             install-*.dibbla.app alias to verify a deploy
#                             BEFORE the DNS cutover.
#   E2E_PLATFORMS           — space-separated docker platforms.
#                             Default: "linux/amd64 linux/arm64".
#   E2E_OLD_VERSION         — tag used for the `dibbla update` leg (test 4).
#   SKIP_ARM64=1            — skip the arm64 leg when qemu is unavailable.
#
# Usage:
#   ./install-no-github.sh
#   DIBBLA_INSTALL_BASE_URL=https://install-xxxx.dibbla.app ./install-no-github.sh
set -euo pipefail

BASE_URL="${DIBBLA_INSTALL_BASE_URL:-https://install.dibbla.com}"
BASE_URL="${BASE_URL%/}"
PLATFORMS="${E2E_PLATFORMS:-linux/amd64 linux/arm64}"
OLD_VERSION="${E2E_OLD_VERSION:-}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }

if [ "${SKIP_ARM64:-0}" = "1" ]; then
    PLATFORMS="$(echo "$PLATFORMS" | tr ' ' '\n' | grep -v arm64 | tr '\n' ' ')"
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

pass() { printf '      \033[0;32m✓\033[0m %s\n' "$1"; }
fail() { printf '      \033[0;31m✗\033[0m %s\n' "$1" >&2; exit 1; }

# Every host the installer must NOT need. release-assets.githubusercontent.com is
# in here because release assets on github.com 302 to it — a mirror that
# redirected off-origin would inherit the very failure this change removes.
BLACKHOLE_HOSTS="api.github.com github.com www.github.com codeload.github.com objects.githubusercontent.com raw.githubusercontent.com release-assets.githubusercontent.com"

# 127.0.0.1 rather than 0.0.0.0: it fails fast with connection-refused instead of
# hanging until a timeout, so a regression shows up as a clear error.
blackhole_snippet() {
    for h in $BLACKHOLE_HOSTS; do
        echo "127.0.0.1 $h"
    done
}
blackhole_snippet > "$WORK/blackhole"

echo "==> P-0020 E2E: install with GitHub blackholed"
echo "    origin under test : $BASE_URL"
echo "    platforms         : $PLATFORMS"
echo

# ---------------------------------------------------------------------------
# 0. Pre-flight: the origin serves what the installer needs.
# ---------------------------------------------------------------------------
echo "[0/5] Origin contract"
curl -fsSL "$BASE_URL/latest.json" -o "$WORK/latest.json" \
    || fail "GET $BASE_URL/latest.json failed"
TAG="$(grep '"tag_name"' "$WORK/latest.json" | sed -E 's/.*"([^"]+)".*/\1/')"
[ -n "$TAG" ] || fail "latest.json has no tag_name"
pass "latest.json serves tag_name=$TAG"

curl -fsSL "$BASE_URL/" -o "$WORK/root" || fail "GET $BASE_URL/ failed"
head -n1 "$WORK/root" | grep -q '^#!/bin/sh' \
    || fail "GET / did not return the shell script (it must, as GitHub Pages did)"
pass "GET / returns the shell script"

curl -fsSL "$BASE_URL/checksums.txt" -o "$WORK/checksums.txt" \
    || fail "GET $BASE_URL/checksums.txt failed"
pass "checksums.txt is served from the same origin as the archives"

ARCHIVE="dibbla_${TAG#v}_linux_amd64.tar.gz"
REDIRECTS="$(curl -sS -o /dev/null -w '%{num_redirects}' -L "$BASE_URL/latest/$ARCHIVE")"
[ "$REDIRECTS" = "0" ] \
    || fail "archive download followed $REDIRECTS redirect(s); it must terminate on our own origin"
pass "archive download does not redirect off-origin"
echo

# ---------------------------------------------------------------------------
# 1. The motivating flow: curl | sh in a clean container, GitHub blackholed.
# ---------------------------------------------------------------------------
echo "[1/5] curl | sh with GitHub unreachable"
for platform in $PLATFORMS; do
    echo "  → $platform"
    docker run --rm --platform "$platform" \
        -v "$WORK/blackhole:/blackhole:ro" \
        -e "DIBBLA_INSTALL_BASE_URL=$BASE_URL" \
        debian:stable-slim bash -euo pipefail -c '
            apt-get update -qq >/dev/null
            apt-get install -y -qq --no-install-recommends curl ca-certificates >/dev/null
            cat /blackhole >> /etc/hosts

            # Prove the blackhole is real before trusting the result below.
            if curl -fsS --max-time 5 https://api.github.com/ >/dev/null 2>&1; then
                echo "blackhole is not in effect — api.github.com is still reachable" >&2
                exit 1
            fi

            curl -fsSL "$DIBBLA_INSTALL_BASE_URL/install.sh" | sh

            export PATH="$HOME/.local/bin:$PATH"
            dibbla --version
        ' > "$WORK/out-$(echo "$platform" | tr / -).txt" 2>&1 \
        || { cat "$WORK/out-$(echo "$platform" | tr / -).txt" >&2; fail "install failed on $platform"; }

    out="$WORK/out-$(echo "$platform" | tr / -).txt"
    grep -q "dibbla version ${TAG#v}" "$out" \
        || { cat "$out" >&2; fail "$platform: dibbla --version did not print ${TAG#v}"; }
    grep -q "Verified SHA-256" "$out" \
        || { cat "$out" >&2; fail "$platform: the installer did not verify the checksum"; }
    pass "$platform installed $TAG and verified its SHA-256"
done
echo

# ---------------------------------------------------------------------------
# 2. Checksum verification actually fires.
#
# A corrupted archive with a VALID checksums.txt — i.e. the bytes changed in
# transit, which is the case verification exists for. Asserting the abort is
# non-zero is not enough; a half-installed binary would be worse than a clean
# failure, so the empty install dir is the real assertion.
# ---------------------------------------------------------------------------
echo "[2/5] Corrupted archive is rejected"
FIXTURE="$WORK/fixture"
mkdir -p "$FIXTURE/latest"
cp "$WORK/checksums.txt" "$FIXTURE/checksums.txt"
grep '"tag_name"' "$WORK/latest.json" > /dev/null
printf '{\n  "tag_name": "%s",\n  "assets": []\n}\n' "$TAG" > "$FIXTURE/latest.json"
cp "$ROOT/docs/install.sh" "$FIXTURE/install.sh"
# Same name the installer will ask for, deliberately wrong bytes.
head -c 4096 /dev/urandom > "$FIXTURE/latest/$ARCHIVE"

docker run --rm --platform linux/amd64 \
    -v "$FIXTURE:/fixture:ro" \
    debian:stable-slim bash -euo pipefail -c '
        apt-get update -qq >/dev/null
        apt-get install -y -qq --no-install-recommends curl ca-certificates python3 >/dev/null

        ( cd /fixture && python3 -m http.server 8099 >/dev/null 2>&1 & )
        sleep 2

        set +e
        DIBBLA_INSTALL_BASE_URL=http://127.0.0.1:8099 sh /fixture/install.sh
        rc=$?
        set -e

        if [ "$rc" -eq 0 ]; then
            echo "installer exited 0 on a corrupted archive" >&2
            exit 1
        fi
        if [ -e "$HOME/.local/bin/dibbla" ]; then
            echo "installer left a binary behind after a checksum failure" >&2
            exit 1
        fi
        echo "CHECKSUM-ABORT-OK rc=$rc"
    ' > "$WORK/out-corrupt.txt" 2>&1 \
    || { cat "$WORK/out-corrupt.txt" >&2; fail "corrupted-archive leg did not behave"; }

grep -q "CHECKSUM-ABORT-OK" "$WORK/out-corrupt.txt" \
    || { cat "$WORK/out-corrupt.txt" >&2; fail "expected a non-zero abort with no binary installed"; }
grep -qi "Checksum mismatch" "$WORK/out-corrupt.txt" \
    || { cat "$WORK/out-corrupt.txt" >&2; fail "abort message did not name the checksum mismatch"; }
pass "aborts non-zero, names the mismatch, leaves no binary"
echo

# ---------------------------------------------------------------------------
# 3. A missing checksums entry must also refuse (fail-closed, not fail-open).
# ---------------------------------------------------------------------------
echo "[3/5] Missing checksums entry is refused"
FIXTURE2="$WORK/fixture-nosums"
mkdir -p "$FIXTURE2/latest"
: > "$FIXTURE2/checksums.txt"
printf '{\n  "tag_name": "%s",\n  "assets": []\n}\n' "$TAG" > "$FIXTURE2/latest.json"
cp "$ROOT/docs/install.sh" "$FIXTURE2/install.sh"
cp "$WORK/checksums.txt" "$FIXTURE2/real-checksums.txt"
curl -fsSL "$BASE_URL/latest/$ARCHIVE" -o "$FIXTURE2/latest/$ARCHIVE"

docker run --rm --platform linux/amd64 \
    -v "$FIXTURE2:/fixture:ro" \
    debian:stable-slim bash -euo pipefail -c '
        apt-get update -qq >/dev/null
        apt-get install -y -qq --no-install-recommends curl ca-certificates python3 >/dev/null
        ( cd /fixture && python3 -m http.server 8099 >/dev/null 2>&1 & )
        sleep 2

        set +e
        DIBBLA_INSTALL_BASE_URL=http://127.0.0.1:8099 sh /fixture/install.sh
        rc=$?
        set -e
        [ "$rc" -ne 0 ] || { echo "installer accepted an archive with no checksum entry" >&2; exit 1; }
        [ ! -e "$HOME/.local/bin/dibbla" ] || { echo "binary installed despite no checksum entry" >&2; exit 1; }
        echo "NO-ENTRY-REFUSED-OK rc=$rc"
    ' > "$WORK/out-nosums.txt" 2>&1 \
    || { cat "$WORK/out-nosums.txt" >&2; fail "missing-entry leg did not behave"; }

grep -q "NO-ENTRY-REFUSED-OK" "$WORK/out-nosums.txt" \
    || { cat "$WORK/out-nosums.txt" >&2; fail "a genuine archive with no checksums entry was accepted"; }
pass "refuses rather than installing unverified"
echo

# ---------------------------------------------------------------------------
# 4. `dibbla update` without GitHub, and the pinned-version failure mode.
#
# Needs a released build that already carries the P-0020 update path, so it is
# skipped until such a tag exists. Skipping loudly beats a green run that
# silently tested nothing.
# ---------------------------------------------------------------------------
echo "[4/5] dibbla update with GitHub unreachable"
if [ -z "$OLD_VERSION" ]; then
    echo "      - skipped: set E2E_OLD_VERSION to a tag that ships the P-0020 update path"
    echo "        (a pre-P-0020 binary resolves updates against GitHub by design, so it"
    echo "         cannot pass here — that is the bug, not a test failure)"
else
    docker run --rm --platform linux/amd64 \
        -v "$WORK/blackhole:/blackhole:ro" \
        -e "DIBBLA_INSTALL_BASE_URL=$BASE_URL" \
        -e "OLD_VERSION=$OLD_VERSION" \
        -e "EXPECTED_TAG=$TAG" \
        debian:stable-slim bash -euo pipefail -c '
            apt-get update -qq >/dev/null
            apt-get install -y -qq --no-install-recommends curl ca-certificates >/dev/null

            # Install the older version BEFORE blackholing, since only the
            # latest is mirrored — the point of the leg is the update, not the
            # install.
            curl -fsSL "https://github.com/dibbla-agents/dibbla-cli/releases/download/${OLD_VERSION}/dibbla_${OLD_VERSION#v}_linux_amd64.tar.gz" \
                -o /tmp/old.tar.gz
            mkdir -p "$HOME/.local/bin"
            tar -xzf /tmp/old.tar.gz -C /tmp
            install -m 0755 /tmp/dibbla "$HOME/.local/bin/dibbla"
            export PATH="$HOME/.local/bin:$PATH"

            cat /blackhole >> /etc/hosts

            dibbla update --yes
            got="$(dibbla --version)"
            echo "$got" | grep -q "${EXPECTED_TAG#v}" || {
                echo "after update, version is \"$got\", expected ${EXPECTED_TAG#v}" >&2
                exit 1
            }
            echo "UPDATE-OK $got"

            # A pinned --version has no mirror equivalent. It must fail with an
            # explanation that names the way out, not a bare network error.
            set +e
            out="$(dibbla update --version "$OLD_VERSION" --yes 2>&1)"
            rc=$?
            set -e
            [ "$rc" -ne 0 ] || { echo "pinned update unexpectedly succeeded with GitHub blocked" >&2; exit 1; }
            echo "$out" | grep -q "without --version" || {
                echo "pinned-update error does not name the way out: $out" >&2
                exit 1
            }
            echo "PINNED-ERROR-OK"
        ' > "$WORK/out-update.txt" 2>&1 \
        || { cat "$WORK/out-update.txt" >&2; fail "update leg failed"; }

    grep -q "UPDATE-OK" "$WORK/out-update.txt" || fail "dibbla update did not reach the latest tag"
    pass "dibbla update reached $TAG with GitHub blackholed"
    grep -q "PINNED-ERROR-OK" "$WORK/out-update.txt" || fail "pinned --version did not fail explicitly"
    pass "dibbla update --version fails with an actionable message"
fi
echo

# ---------------------------------------------------------------------------
# 5. Windows. install.ps1 is exercised by the release pipeline's Windows runner
#    (see .github/workflows/release.yml sign-windows), which this harness cannot
#    reach from Linux. Named rather than silently omitted.
# ---------------------------------------------------------------------------
echo "[5/5] Windows (install.ps1)"
echo "      - not run here: needs a Windows runner. See release.yml."
echo

echo "==> All Linux legs passed against $BASE_URL ($TAG)"
