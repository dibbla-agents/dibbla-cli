#!/usr/bin/env bash
# Wait for the install app to be serving a given tag before asserting anything.
#
# `dibbla deploy --update` is a rolling replace: it returns when the platform has
# accepted the new revision, not when the old pod has stopped answering. Verifying
# immediately after it returns raced the restart and got a 502 from the edge — a
# false negative that failed the pipeline while the deploy itself was fine
# (observed on mirror-redeploy.yml's first run, 2026-08-17).
#
# There is a second, subtler reason to gate on the TAG and not just on a 200:
# /latest/ holds exactly one version, so mid-rollout a client can read the new
# latest.json from a new pod and then request the archive from an old one. Waiting
# until the tag is consistently served keeps the verification from tripping over
# that window too.
#
# Usage: ./wait-ready.sh <base-url> <expected-tag> [timeout-seconds]
set -euo pipefail

BASE="${1:?usage: wait-ready.sh <base-url> <expected-tag> [timeout]}"
BASE="${BASE%/}"
WANT="${2:?expected tag required}"
TIMEOUT="${3:-180}"

# Three consecutive good reads, not one: a single 200 can come from a pod that is
# about to be replaced, which is the race this script exists to close.
NEED_STREAK=3

deadline=$(( $(date +%s) + TIMEOUT ))
streak=0
last="(no response yet)"

while [ "$(date +%s)" -lt "$deadline" ]; do
    # Cache-bust: latest.json is served with max-age=60, so the edge could hand
    # back the pre-deploy copy and make this pass against stale bytes.
    if body="$(curl -fsSL -H 'Cache-Control: no-cache' "${BASE}/latest.json?_=$(date +%s)-${streak}" 2>/dev/null)"; then
        got="$(printf '%s' "$body" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
        if [ "$got" = "$WANT" ]; then
            streak=$(( streak + 1 ))
            if [ "$streak" -ge "$NEED_STREAK" ]; then
                echo "wait-ready: $BASE is serving $WANT (${NEED_STREAK} consecutive reads)"
                exit 0
            fi
        else
            streak=0
            last="serving tag '$got', want '$WANT'"
        fi
    else
        streak=0
        last="request failed (app restarting, or edge 502)"
    fi
    sleep 3
done

echo "wait-ready: $BASE did not settle on $WANT within ${TIMEOUT}s — $last" >&2
exit 1
