#!/usr/bin/env bash
# End-to-end test for P-0011: named contexts against two API servers at once.
#
# The claim under test is the headline one — you can be logged in to several
# Dibbla API servers at the same time, switch between them with one command, and
# every command reaches the server you selected. That claim needs TWO
# distinguishable endpoints, each with its own token. It does not need
# production, and this script deliberately does not use it.
#
# The two endpoints are:
#
#   1. A REAL Dibbla instance (dev by default). This is what makes it an end-to-
#      end test rather than a unit test: a genuine `dibbla login`, a genuine
#      token, a genuine `dibbla apps list` over the network.
#
#   2. A local RECORDING STUB, started by this script. It answers the handful of
#      endpoints the CLI touches and writes every request it receives to a file.
#
# The stub is not a compromise; for this claim it is the better instrument.
# P-0011's Part C acceptance asks for the org header to be "asserted at the HTTP
# layer against a recording server, not inferred from the UI", and only a server
# whose inbound requests we can read can answer three questions that matter:
#
#   * did the ACTIVE context's token arrive, and never the other context's?
#   * did the ACTIVE context's X-Org-ID arrive, and never the previous one's?
#     (that is the wrong-org-to-the-wrong-server case Part C exists to prevent)
#   * does an UNPINNED context send NO X-Org-ID header at all? "Unpinned" has no
#     other observable, and a 200 from a real server cannot distinguish it.
#
# What this does NOT prove, stated rather than left to be assumed: the second
# endpoint is not a real Dibbla deployment, so this is not evidence that the CLI
# works against two real installations. It is evidence that the CLI keeps two
# credentials apart and routes each command to the right one — which is the
# property P-0011 introduces and the only one at risk here.
#
# The whole run is confined to a throwaway XDG_CONFIG_HOME, so it can never read
# or destroy the credentials of whoever runs it. That is not politeness: on a
# machine with a real login, a test of a credential store that used the real
# store would be one bug away from logging its author out.
#
# Required env:
#   DIBBLA_E2E_LIVE_TOKEN — an API token for the live instance (an ak_ token;
#                           mint one against dev, and revoke it afterwards).
#
# Optional env:
#   DIBBLA_E2E_LIVE_URL   — the live instance. Default https://api.dibbla.net
#                           (dev), which resolves to a cluster-local ClusterIP
#                           and is reachable ONLY from pine.
#   DIBBLA_E2E_BIN        — the dibbla binary to exercise. Default: built from
#                           this checkout, because the released binary on PATH
#                           predates this feature.
#   DIBBLA_E2E_MARKER_APP — the name of an app that exists on the LIVE instance
#                           and nowhere else. Used to prove the two inventories
#                           really are the two servers' own. When unset, that one
#                           assertion is reported as SKIPPED rather than
#                           silently passing.
#
# Usage (from pine):
#   DIBBLA_E2E_LIVE_TOKEN=ak_... ./scripts/e2e/contexts-smoke.sh
#
# This is hand-run, like every other E2E in this repo. It is not a CI gate: it
# needs a credential for a live instance, which CI does not have.
set -euo pipefail

LIVE_URL="${DIBBLA_E2E_LIVE_URL:-https://api.dibbla.net}"
LIVE_URL="${LIVE_URL%/}"
LIVE_TOKEN="${DIBBLA_E2E_LIVE_TOKEN:?DIBBLA_E2E_LIVE_TOKEN is required (an ak_ token for $LIVE_URL)}"
MARKER_APP="${DIBBLA_E2E_MARKER_APP:-}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

WORK="$(mktemp -d)"
STUB_PID=""
cleanup() {
    [ -n "$STUB_PID" ] && kill "$STUB_PID" 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

pass() { printf '      \033[0;32m✓\033[0m %s\n' "$1"; }
skip() { printf '      \033[0;33m-\033[0m SKIPPED: %s\n' "$1"; }
fail() { printf '      \033[0;31m✗\033[0m %s\n' "$1" >&2; exit 1; }
step() { printf '\n\033[1m[%s/%s]\033[0m %s\n' "$1" "$TOTAL_STEPS" "$2"; }
TOTAL_STEPS=8

# Every dibbla invocation in this script runs against a throwaway config dir and
# with the ambient DIBBLA_* environment cleared. Leaving DIBBLA_API_TOKEN set in
# the caller's shell would short-circuit the entire context layer and every
# assertion below would pass without testing anything.
export XDG_CONFIG_HOME="$WORK/config"
mkdir -p "$XDG_CONFIG_HOME"
unset DIBBLA_API_TOKEN DIBBLA_API_URL DIBBLA_AUTH_SERVICE_URL DIBBLA_ORG_ID DIBBLA_CONTEXT || true

BIN="${DIBBLA_E2E_BIN:-}"
if [ -z "$BIN" ]; then
    BIN="$WORK/dibbla"
    (cd "$ROOT" && go build -o "$BIN" ./cmd/dibbla)
fi
[ -x "$BIN" ] || fail "no dibbla binary at $BIN"

# ---------------------------------------------------------------------------
# The recording stub. Answers token validation and an empty app list, and
# appends one TAB-SEPARATED line per request: METHOD, PATH, AUTHORIZATION,
# X-ORG-ID. Tabs rather than spaces because an Authorization value is "Bearer
# <token>" — it contains a space, so a space-separated log silently shifts every
# later column and the org-header assertions read the wrong field. That is
# exactly how this script's first run reported a failure that was not there.
# ---------------------------------------------------------------------------
REQUESTS="$WORK/requests.log"
: > "$REQUESTS"

cat > "$WORK/stub.py" <<'PY'
import http.server, json, os, sys, threading

LOG = os.environ["E2E_REQUEST_LOG"]
LAB_ONLY_APP = os.environ["E2E_LAB_ONLY_APP"]
lock = threading.Lock()

class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):  # keep the script's own output readable
        pass

    def record(self):
        with lock, open(LOG, "a") as f:
            f.write("%s\t%s\t%s\t%s\n" % (
                self.command,
                self.path.split("?")[0],
                self.headers.get("Authorization", "-"),
                self.headers.get("X-Org-ID", "-"),
            ))

    def respond(self, code, body):
        raw = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def handle_any(self):
        self.record()
        path = self.path.split("?")[0]
        if path.endswith("/tokens/validate"):
            # Enough shape for `dibbla login` and `dibbla status` to accept it.
            self.respond(200, {"valid": True, "user": {"email": "e2e@example.invalid"},
                               "organization": {"id": "org-stub", "name": "Stub Org"}})
        elif path.endswith("/api/deploy/deployments"):
            # A NON-EMPTY inventory, with a name that exists nowhere else. An
            # empty list would make "the marker app is absent here" true for
            # every possible name, so step 8's absence assertion would pass
            # without discriminating anything.
            # The shape internal/apps expects: {"deployments":[...],"total":N}
            # with alias/url/status per entry. Matched against the Go structs
            # rather than guessed, because a stub the CLI silently parses as
            # "no applications" would make the absence half of step 8 pass
            # without proving anything.
            self.respond(200, {"total": 1, "deployments": [{
                "id": "app-lab-fixture",
                "alias": LAB_ONLY_APP,
                "url": "http://127.0.0.1/lab-fixture",
                "status": "running",
                "created_at": "2026-01-01T00:00:00Z",
                "updated_at": "2026-01-01T00:00:00Z",
            }]})
        elif "/organizations" in path or "/orgs" in path:
            self.respond(200, {"organizations": []})
        else:
            # Anything unrecognised gets an empty object. If a future CLI change
            # routes the app list elsewhere, the assertions in step 8 fail loudly
            # rather than reading an empty inventory as a pass -- which is what
            # happened when this stub matched "/apps" while the CLI called
            # /api/deploy/deployments.
            self.respond(200, {})

    do_GET = do_POST = do_PUT = do_DELETE = handle_any

port = int(sys.argv[1])
http.server.HTTPServer(("127.0.0.1", port), Handler).serve_forever()
PY

STUB_PORT="$(python3 - <<'PY'
import socket
s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()
PY
)"
STUB_URL="http://127.0.0.1:$STUB_PORT"
LAB_ONLY_APP="lab-only-fixture-$$"
E2E_REQUEST_LOG="$REQUESTS" E2E_LAB_ONLY_APP="$LAB_ONLY_APP" python3 "$WORK/stub.py" "$STUB_PORT" &
STUB_PID=$!

# Wait for it rather than sleeping a guessed amount: a race here would show up
# as a confusing login failure attributed to the CLI.
for _ in $(seq 1 50); do
    curl -sf "$STUB_URL/health" >/dev/null 2>&1 && break
    sleep 0.1
done
curl -sf "$STUB_URL/health" >/dev/null 2>&1 || fail "the recording stub did not come up on $STUB_URL"

# requests_to <substring> — the recorded lines whose path contains the substring
reqs() { grep -F "$1" "$REQUESTS" || true; }

# ---------------------------------------------------------------------------
step 1 "log in to the live instance ($LIVE_URL)"
# ---------------------------------------------------------------------------
"$BIN" login --context live --api-url "$LIVE_URL" --api-key "$LIVE_TOKEN" >"$WORK/login-live.txt" 2>&1 \
    || { cat "$WORK/login-live.txt"; fail "login to the live instance failed"; }
grep -q "context live" "$WORK/login-live.txt" || fail "login did not report the context name: $(cat "$WORK/login-live.txt")"
pass "logged in to $LIVE_URL as context 'live'"

# ---------------------------------------------------------------------------
step 2 "log in to the second instance — the first must survive"
# ---------------------------------------------------------------------------
"$BIN" login --context lab --api-url "$STUB_URL" --api-key "ak_stub_token" >"$WORK/login-lab.txt" 2>&1 \
    || { cat "$WORK/login-lab.txt"; fail "login to the stub failed"; }

# THE headline assertion. Before P-0011 this is precisely where the first
# credential was destroyed.
"$BIN" --context live status --no-validate >"$WORK/status-live.txt" 2>&1 \
    || { cat "$WORK/status-live.txt"; fail "the first context stopped working after logging in to the second"; }
grep -q "Token:   configured" "$WORK/status-live.txt" \
    || fail "the first context lost its token: $(cat "$WORK/status-live.txt")"
pass "logging in to a second server left the first logged in"

# ---------------------------------------------------------------------------
step 3 "both contexts are listed, and the newest is current"
# ---------------------------------------------------------------------------
"$BIN" context list --json >"$WORK/list.json" 2>&1 || fail "context list --json failed"
python3 - "$WORK/list.json" <<'PY' || exit 1
import json, sys
rows = json.load(open(sys.argv[1]))
names = sorted(r["name"] for r in rows)
if names != ["lab", "live"]:
    print("      contexts = %s, want ['lab','live']" % names); sys.exit(1)
if not all(r["logged_in"] for r in rows):
    print("      a context reports no stored token: %s" % rows); sys.exit(1)
cur = [r["name"] for r in rows if r["current"]]
if cur != ["lab"]:
    print("      current = %s, want ['lab']" % cur); sys.exit(1)
PY
pass "context list --json shows both, marks 'lab' current, both hold tokens"

# ---------------------------------------------------------------------------
step 4 "--context routes each command to the server it names"
# ---------------------------------------------------------------------------
: > "$REQUESTS"
"$BIN" --context lab apps list >/dev/null 2>&1 || true
[ -s "$REQUESTS" ] || fail "--context lab sent nothing to the stub"
if ! reqs "" | grep -q "Bearer ak_stub_token"; then
    fail "the stub did not receive the lab context's token; got:
$(cat "$REQUESTS")"
fi
if reqs "" | grep -q "Bearer $LIVE_TOKEN"; then
    fail "THE LIVE INSTANCE'S TOKEN WAS SENT TO THE OTHER SERVER — credentials are leaking across contexts"
fi
pass "the stub received only the lab context's token"

: > "$REQUESTS"
"$BIN" --context live apps list >/dev/null 2>&1 || true
if [ -s "$REQUESTS" ]; then
    fail "--context live still sent requests to the stub:
$(cat "$REQUESTS")"
fi
pass "--context live sent nothing to the other server"

# ---------------------------------------------------------------------------
step 5 "an UNPINNED context sends no X-Org-ID header at all"
# ---------------------------------------------------------------------------
# This is the only observable meaning of "unpinned", and only a server whose
# inbound headers can be read can check it.
: > "$REQUESTS"
"$BIN" --context lab apps list >/dev/null 2>&1 || true
if awk -F'\t' '$4 != "-" {print}' "$REQUESTS" | grep -q .; then
    fail "an unpinned context sent an X-Org-ID header:
$(cat "$REQUESTS")"
fi
pass "no X-Org-ID sent for an unpinned context"

# ---------------------------------------------------------------------------
step 6 "PART C: an org pinned on one context never reaches the other server"
# ---------------------------------------------------------------------------
# Pin an org on the lab context by editing config.yaml directly. Going through
# `dibbla org use` would need the stub to serve a membership list the CLI
# accepts, which tests the stub rather than the CLI.
# Keyed on the context NAME, not on a URL substring. Matching on the URL looked
# fine and was wrong: with both endpoints on 127.0.0.1 during a self-test it
# pinned the org on BOTH contexts, and the script then reported a leak that did
# not exist. A test that fails for the wrong reason is worth no more than one
# that passes for the wrong reason, and this one cost a real diagnosis.
python3 - "$XDG_CONFIG_HOME/dibbla/config.yaml" <<'PYPIN'
import sys
path = sys.argv[1]
out, in_lab = [], False
for line in open(path).read().splitlines():
    stripped = line.strip()
    if stripped.endswith(":") and not stripped.startswith("-"):
        in_lab = (stripped == "lab:")
    out.append(line)
    if in_lab and stripped.startswith("api_url:"):
        out.append(" " * (len(line) - len(line.lstrip())) + "org: org-lab-only")
open(path, "w").write("\n".join(out) + "\n")
PYPIN
# The fixture has to be checked, not assumed: a pin that silently landed on both
# contexts is indistinguishable from the defect this step exists to detect.
python3 - "$XDG_CONFIG_HOME/dibbla/config.yaml" <<'PYCHK'
import sys
s = open(sys.argv[1]).read()
n = s.count("org-lab-only")
if n != 1:
    print("      the fixture pinned the org on %d contexts, want exactly 1:\n%s" % (n, s))
    sys.exit(1)
PYCHK
grep -q "org-lab-only" "$XDG_CONFIG_HOME/dibbla/config.yaml" || fail "could not pin an org on the lab context"

: > "$REQUESTS"
"$BIN" --context lab apps list >/dev/null 2>&1 || true
if ! awk -F'\t' '{print $4}' "$REQUESTS" | grep -q '^org-lab-only$'; then
    fail "the lab context's own org pin did not reach the lab server:
$(cat "$REQUESTS")"
fi
pass "the active context's org reached its own server"

# Now the failure this Part exists to prevent: switch to the live context and
# confirm the lab org does not ride along. The live instance's inbound headers
# cannot be read, so the assertion is made where it CAN be: the resolved value
# the CLI would send.
"$BIN" --context live status --no-validate >"$WORK/status-live2.txt" 2>&1 || true
if grep -q "org-lab-only" "$WORK/status-live2.txt"; then
    fail "THE OTHER SERVER'S ORGANIZATION IS ACTIVE ON THE LIVE CONTEXT — this is the wrong-org-to-the-wrong-server case:
$(cat "$WORK/status-live2.txt")"
fi
pass "the lab context's org does not leak into the live context"

# ---------------------------------------------------------------------------
step 7 "context use changes what a bare command does, and moves the legacy mirror"
# ---------------------------------------------------------------------------
"$BIN" context use live >/dev/null 2>&1 || fail "context use live failed"
: > "$REQUESTS"
"$BIN" apps list >/dev/null 2>&1 || true
[ -s "$REQUESTS" ] && fail "a bare command still reached the stub after switching to live:
$(cat "$REQUESTS")"
pass "a bare command follows the selected context"

# The legacy credentials.env is defined as "whatever context is current", which
# is what keeps a pre-context binary and the shipped demo-seeding scripts
# working. Assert it followed the switch rather than trusting the code comment.
LEGACY="$XDG_CONFIG_HOME/dibbla/credentials.env"
if [ -f "$LEGACY" ]; then
    grep -q "$LIVE_URL" "$LEGACY" \
        || fail "the legacy credentials.env did not follow 'context use'; a pre-context binary would still be on the other server:
$(cat "$LEGACY")"
    pass "the legacy credentials.env was repointed at the selected context"
else
    # On a host WITH a working keyring the tokens are not on disk at all and the
    # mirror lives in the keyring instead. Say which case this run was in
    # rather than passing an assertion that never ran.
    skip "no credentials.env on this host — the keyring is in use, so the mirror lives there"
fi

# The routing check in step 4 ran while 'lab' was the selected context, so a
# defect that resolved EVERY context to the SELECTED one's token would have
# passed it — the right token arrived for the wrong reason. Now that 'live' is
# selected, ask the same question again: an explicit --context lab must still
# carry lab's own token, not the selected context's.
#
# This gap was found by mutating the CLI and watching the script stay green,
# not by reading it.
: > "$REQUESTS"
"$BIN" --context lab apps list >/dev/null 2>&1 || true
[ -s "$REQUESTS" ] || fail "--context lab sent nothing to the stub after switching the selected context to live"
if ! awk -F'\t' '{print $3}' "$REQUESTS" | grep -q "^Bearer ak_stub_token$"; then
    fail "--context lab did not carry lab's own token once another context was selected; the stub saw:
$(cat "$REQUESTS")"
fi
if awk -F'\t' '{print $3}' "$REQUESTS" | grep -q "Bearer $LIVE_TOKEN"; then
    fail "THE SELECTED CONTEXT'S TOKEN WAS SENT TO THE OTHER SERVER — --context is not resolving per context"
fi
pass "--context still resolves per context, not to whichever context is selected"

# ---------------------------------------------------------------------------
step 8 "the marker app: present on the live instance, absent on the other"
# ---------------------------------------------------------------------------
# Asserting merely that the two inventories DIFFER would pass for the wrong
# reason, and would start failing the day both instances happened to hold the
# same app names. A uniquely-named app that exists on exactly one of them is the
# assertion that means something.
# The check runs in BOTH directions. Asserting only that the marker is absent
# from the other server would pass trivially if that server returned nothing at
# all, so the stub serves an app of its own and that one must be absent from the
# live inventory too. Neither half can pass because the other side is empty.
"$BIN" --context lab apps list >"$WORK/apps-lab.txt" 2>&1 || fail "apps list on the lab context failed"
grep -q "$LAB_ONLY_APP" "$WORK/apps-lab.txt" \
    || fail "the lab server's own app is missing from its inventory, so this step cannot discriminate anything:
$(cat "$WORK/apps-lab.txt")"
pass "the lab server's own app is listed under --context lab"

"$BIN" --context live apps list >"$WORK/apps-live.txt" 2>&1 || fail "apps list on the live context failed"
grep -q "$LAB_ONLY_APP" "$WORK/apps-live.txt" \
    && fail "the OTHER server's app appeared in the live inventory — the two contexts are reading one server"
pass "the lab server's app is absent from the live inventory"

if [ -z "$MARKER_APP" ]; then
    skip "DIBBLA_E2E_MARKER_APP not set — the live instance's side of the inventory check was NOT tested"
else
    grep -q "$MARKER_APP" "$WORK/apps-live.txt" \
        || fail "the marker app $MARKER_APP is not in the live inventory — does it exist on $LIVE_URL?
$(cat "$WORK/apps-live.txt")"
    pass "the marker app is present under --context live"

    grep -q "$MARKER_APP" "$WORK/apps-lab.txt" \
        && fail "the marker app appeared in the OTHER server's inventory — the contexts are not isolated"
    pass "the marker app is absent under --context lab"
fi

# ---------------------------------------------------------------------------
printf '\n\033[0;32mAll assertions passed.\033[0m Anything reported as SKIPPED above was not tested.\n'
