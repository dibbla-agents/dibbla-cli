#!/usr/bin/env bash
# Stage installer-site/public/ for one release tag.
#
# One script, two callers: the `mirror` job in .github/workflows/release.yml and
# a human doing a cutover or a local test. Keeping the staging logic here rather
# than inline in the workflow is what makes "reproduce what CI published" a
# single command instead of a reading exercise.
#
# Usage:
#   ./stage.sh v1.2.53                      # stage for install.dibbla.com
#   ./stage.sh v1.2.53 http://localhost:8080  # stage for a local/staging origin
#
# The second argument is baked into latest.json's browser_download_url values,
# because `internal/update` downloads whatever URL that field names. It must be
# the origin the client will actually reach.
#
# Requires: gh (authenticated), jq, sha256sum.
set -euo pipefail

TAG="${1:?usage: stage.sh <tag> [base-url]}"
BASE_URL="${2:-https://install.dibbla.com}"
BASE_URL="${BASE_URL%/}"
REPO="${GITHUB_REPOSITORY:-dibbla-agents/dibbla-cli}"

HERE="$(cd "$(dirname "$0")" && pwd)"
PUBLIC="$HERE/public"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# GNU stat is not portable and this script is documented as runnable locally,
# including on macOS. One helper rather than two call sites getting it wrong.
filesize() {
    wc -c < "$1" | tr -d ' '
}

for tool in gh jq sha256sum; do
    command -v "$tool" >/dev/null 2>&1 || { echo "stage.sh: $tool is required" >&2; exit 1; }
done

echo "==> Downloading $TAG assets from $REPO"
mkdir -p "$WORK/assets"
gh release download "$TAG" -D "$WORK/assets" --clobber -R "$REPO"

# Keep only what an installer needs. The .deb/.rpm packages are deliberately
# excluded: all 12 assets are 44.7 MB against the platform's 50 MB
# MAX_ARCHIVE_SIZE_MB, which one new architecture would break, while the six
# archives plus checksums.txt are ~27 MB. Package-manager users are not the
# GitHub-blocked population, and GitHub Releases stays the archive of record for
# them and for version-pinned downloads.
echo "==> Selecting archives"
rm -rf "$PUBLIC"
mkdir -p "$PUBLIC/latest"

shopt -s nullglob
archives=("$WORK"/assets/dibbla_*.tar.gz "$WORK"/assets/dibbla_*.zip)
shopt -u nullglob
if [ "${#archives[@]}" -eq 0 ]; then
    echo "stage.sh: $TAG published no tar.gz/zip archives" >&2
    exit 1
fi
for a in "${archives[@]}"; do
    cp "$a" "$PUBLIC/latest/"
done

# checksums.txt is copied verbatim from the release rather than recomputed, so
# the mirror's digests are byte-identical to the ones GitHub publishes. The
# release's own `checksums` job already regenerated it from the *signed* assets;
# recomputing here would only create a second source of truth that can drift.
[ -s "$WORK/assets/checksums.txt" ] || { echo "stage.sh: $TAG has no checksums.txt" >&2; exit 1; }
cp "$WORK/assets/checksums.txt" "$PUBLIC/checksums.txt"

echo "==> Staging the bootstrap scripts"
cp "$HERE/../docs/install.sh" "$PUBLIC/install.sh"
cp "$HERE/../docs/install.ps1" "$PUBLIC/install.ps1"

# latest.json carries exactly the subset internal/update parses (tag_name, and
# per-asset name/browser_download_url/size), so the Go structs, ChecksumAsset()
# and FindAsset() need no change. `digest` is additive and follows P-0016's
# convention ("sha256:" + 64 lowercase hex), letting a client verify without a
# second fetch.
#
# Built with jq rather than printf so it cannot emit invalid JSON, and so
# tag_name lands on its own line — install.sh resolves the version by grepping
# for it, which is the one formatting assumption the shell installer makes.
echo "==> Generating latest.json"
{
    for a in "$PUBLIC/latest/"*; do
        name="$(basename "$a")"
        jq -n --arg name "$name" \
              --arg url "$BASE_URL/latest/$name" \
              --argjson size "$(filesize "$a")" \
              --arg digest "sha256:$(sha256sum "$a" | cut -d' ' -f1)" \
              '{name: $name, browser_download_url: $url, size: $size, digest: $digest}'
    done
    jq -n --arg url "$BASE_URL/checksums.txt" \
          --argjson size "$(filesize "$PUBLIC/checksums.txt")" \
          --arg digest "sha256:$(sha256sum "$PUBLIC/checksums.txt" | cut -d' ' -f1)" \
          '{name: "checksums.txt", browser_download_url: $url, size: $size, digest: $digest}'
} | jq -s --arg tag "$TAG" '{tag_name: $tag, assets: .}' > "$PUBLIC/latest.json"

# Fail here rather than shipping a latest.json no client can parse, and hold the
# grep-able-tag_name assumption install.sh depends on.
jq -e '.tag_name and (.assets | length > 6)' "$PUBLIC/latest.json" >/dev/null
# Anchored to the start of a line: a bare grep for the key passes on compact
# single-line JSON too, which is exactly the shape install.sh cannot parse.
grep -q '^[[:space:]]*"tag_name"' "$PUBLIC/latest.json"

# The mirror's archive names must match AssetName() in
# internal/cmd/update/selfreplace.go, which carries a "MUST stay in sync with
# .goreleaser.yml" comment. Assert the digests agree with checksums.txt, so a
# future rename or a partial download is caught here and not by a user.
echo "==> Verifying staged digests against checksums.txt"
for a in "$PUBLIC/latest/"*; do
    name="$(basename "$a")"
    want="$(awk -v n="$name" '{ sub(/^\*/, "", $2); if ($2 == n) { print $1; exit } }' "$PUBLIC/checksums.txt")"
    [ -n "$want" ] || { echo "stage.sh: checksums.txt has no entry for $name" >&2; exit 1; }
    got="$(sha256sum "$a" | cut -d' ' -f1)"
    [ "$want" = "$got" ] || { echo "stage.sh: digest mismatch for $name" >&2; exit 1; }
    echo "      ok $name"
done

# The six archive names AssetName() in internal/cmd/update/selfreplace.go
# constructs. Checked by identity, not by count: `length > 6` passes with
# darwin_arm64 missing and two unrelated tar.gz present, which would ship a
# mirror that 404s for real users on one platform while every other check
# stayed green.
echo "==> Verifying the archive set matches AssetName()"
VERSION_NUM="${TAG#v}"
for want in \
    "dibbla_${VERSION_NUM}_darwin_amd64.tar.gz" \
    "dibbla_${VERSION_NUM}_darwin_arm64.tar.gz" \
    "dibbla_${VERSION_NUM}_linux_amd64.tar.gz" \
    "dibbla_${VERSION_NUM}_linux_arm64.tar.gz" \
    "dibbla_${VERSION_NUM}_windows_amd64.zip" \
    "dibbla_${VERSION_NUM}_windows_arm64.zip"; do
    [ -s "$PUBLIC/latest/$want" ] || { echo "stage.sh: missing archive $want" >&2; exit 1; }
done
staged_count="$(ls -1 "$PUBLIC/latest" | wc -l | tr -d ' ')"
[ "$staged_count" -eq 6 ] || {
    echo "stage.sh: /latest holds $staged_count files, expected exactly 6" >&2
    ls -1 "$PUBLIC/latest" >&2
    exit 1
}
echo "      ok 6 archives, names match AssetName()"

# The 50 MB platform upload limit is the entire reason the mirror is
# latest-only, so assert it rather than only naming it in prose. Failing here
# points at the real cause; failing later surfaces as a `dibbla deploy`
# rejection from deploy-api *after* `checksums` has already published the
# release -- an error from the wrong layer, at the worst moment.
#
# 45, not 50: room for the two scripts and tar overhead, and one new
# architecture (~5 MB) trips this instead of the deploy.
total_kb="$(du -sk "$PUBLIC" | cut -f1)"
total_mb=$((total_kb / 1024))
BUDGET_MB="${DIBBLA_MIRROR_BUDGET_MB:-45}"
if [ "$total_mb" -gt "$BUDGET_MB" ]; then
    echo "stage.sh: staged tree is ${total_mb} MB, over the ${BUDGET_MB} MB budget." >&2
    echo "          The platform rejects uploads above MAX_ARCHIVE_SIZE_MB (50)." >&2
    echo "          If a new architecture pushed it over, the mirror no longer fits" >&2
    echo "          latest-only -- see P-0020's Alternatives for the proxy design." >&2
    exit 1
fi

echo "==> Staged $TAG into $PUBLIC (${total_mb} MB of a ${BUDGET_MB} MB budget, base URL $BASE_URL)"
