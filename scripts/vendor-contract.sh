#!/usr/bin/env bash
# Re-vendor the platform capability contract from the architecture repository.
#
#   scripts/vendor-contract.sh <path-to-architecture>/docs/contract
#
# Copies v1/{scopes,errors,capabilities}.json and contract.lock into
# internal/platformcontract/ and runs the gate. The lock travels WITH the files:
# a copy without its lock, or a lock without its copy, is exactly the drift the
# gate exists to catch, so this script never copies one without the other.
set -euo pipefail

src="${1:?usage: scripts/vendor-contract.sh <architecture>/docs/contract}"
here="$(cd "$(dirname "$0")/.." && pwd)"
dst="$here/internal/platformcontract"

for f in v1/scopes.json v1/errors.json v1/capabilities.json contract.lock; do
  [[ -f "$src/$f" ]] || { echo "missing $src/$f" >&2; exit 1; }
done

cp "$src/v1/scopes.json" "$src/v1/errors.json" "$src/v1/capabilities.json" "$dst/v1/"
cp "$src/contract.lock" "$dst/contract.lock"

cd "$here"
go test ./internal/platformcontract/ >/dev/null
echo "vendored contract $(grep -o '"contract_digest": *"[^"]*"' "$dst/contract.lock" | cut -d'"' -f4)"
echo "If TestCanonicalDigestMatchesTheCheckedInConstants now fails, update its constants from contract.lock."
