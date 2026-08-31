#!/usr/bin/env bash
# DIB-576 live dev proof with the real CLI binary.
#
# Required:
#   DIBBLA_E2E_LIVE_TOKEN       owner/admin token used to enable and run
#   DIBBLA_E2E_REVIEWER_TOKEN   owner/admin token used for the human decision
#   DIBBLA_E2E_ALLOW_APPROVE=1  explicit permission to approve the dev proposal
#
# Optional:
#   DIBBLA_E2E_LIVE_URL         default https://api.dibbla.net
#   DIBBLA_E2E_MAINTENANCE_APP  default lumen; must be a safe dev fixture
#   DIBBLA_E2E_PROPOSAL_ID      ready maintenance proposal if this run records
#                               no new proposal (for example a deduplicated run)
#   DIBBLA_E2E_BIN              existing binary; otherwise built from this tree
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
LIVE_URL=${DIBBLA_E2E_LIVE_URL:-https://api.dibbla.net}
APP=${DIBBLA_E2E_MAINTENANCE_APP:-lumen}
OPERATOR_TOKEN=${DIBBLA_E2E_LIVE_TOKEN:?DIBBLA_E2E_LIVE_TOKEN is required}
REVIEWER_TOKEN=${DIBBLA_E2E_REVIEWER_TOKEN:?DIBBLA_E2E_REVIEWER_TOKEN is required}
ALLOW_APPROVE=${DIBBLA_E2E_ALLOW_APPROVE:-}

if [[ "$ALLOW_APPROVE" != "1" ]]; then
  echo "DIBBLA_E2E_ALLOW_APPROVE=1 is required: this test approves and deploys a proposal in dev" >&2
  exit 2
fi
if [[ "$LIVE_URL" != "https://api.dibbla.net" ]]; then
  echo "refusing non-dev API URL: $LIVE_URL" >&2
  exit 2
fi
if [[ ! "$APP" =~ ^[a-z][a-z0-9-]{2,62}[a-z0-9]$ ]]; then
  echo "invalid fixture alias: $APP" >&2
  exit 2
fi

WORK=$(mktemp -d "$ROOT/.e2e-maintenance.XXXXXX")
OPERATOR_HOME="$WORK/operator-home"
REVIEWER_HOME="$WORK/reviewer-home"
mkdir -p "$OPERATOR_HOME" "$REVIEWER_HOME"
RESTORE_DISABLED=0

cleanup() {
  if [[ "$RESTORE_DISABLED" == "1" ]]; then
    run_operator apps maintenance disable "$APP" --yes --json >/dev/null 2>&1 || true
  fi
  case "$WORK" in
    "$ROOT"/.e2e-maintenance.*) rm -rf "$WORK" ;;
    *) echo "refusing unsafe cleanup target: $WORK" >&2 ;;
  esac
}
trap cleanup EXIT

BIN=${DIBBLA_E2E_BIN:-}
if [[ -z "$BIN" ]]; then
  BIN="$WORK/dibbla"
  (cd "$ROOT" && go build -o "$BIN" ./cmd/dibbla)
fi
if [[ ! -x "$BIN" ]]; then
  echo "DIBBLA_E2E_BIN is not executable: $BIN" >&2
  exit 2
fi

run_operator() {
  env -u DIBBLA_CONTEXT -u DIBBLA_ORG_ID \
    HOME="$OPERATOR_HOME" XDG_CONFIG_HOME="$OPERATOR_HOME/.config" \
    DIBBLA_API_URL="$LIVE_URL" DIBBLA_API_TOKEN="$OPERATOR_TOKEN" \
    "$BIN" "$@"
}

run_reviewer() {
  env -u DIBBLA_CONTEXT -u DIBBLA_ORG_ID \
    HOME="$REVIEWER_HOME" XDG_CONFIG_HOME="$REVIEWER_HOME/.config" \
    DIBBLA_API_URL="$LIVE_URL" DIBBLA_API_TOKEN="$REVIEWER_TOKEN" \
    "$BIN" "$@"
}

json_value() {
  local file=$1 expression=$2
  python3 - "$file" "$expression" <<'PY'
import json, sys
value = json.load(open(sys.argv[1], encoding="utf-8"))
for key in sys.argv[2].split("."):
    if key:
        value = value.get(key) if isinstance(value, dict) else None
print("" if value is None else (str(value).lower() if isinstance(value, bool) else value))
PY
}

echo "==> build/status: real binary, isolated operator config"
"$BIN" --version
run_operator apps maintenance status "$APP" --json >"$WORK/status.json"
if [[ "$(json_value "$WORK/status.json" settings.enabled)" != "true" ]]; then
  run_operator apps maintenance enable "$APP" --yes --json >"$WORK/enabled.json"
  RESTORE_DISABLED=1
fi

KEY="dib-576-$(date -u +%Y%m%dT%H%M%SZ)-$$"
echo "==> run: one async dispatch"
run_operator apps maintenance run "$APP" --async --json --idempotency-key "$KEY" >"$WORK/first.json"
FIRST_EXECUTION=$(json_value "$WORK/first.json" execution_id)
FIRST_RUN=$(json_value "$WORK/first.json" run_id)
if [[ -z "$FIRST_EXECUTION" || -z "$FIRST_RUN" ]]; then
  echo "first dispatch did not return execution_id + run_id" >&2
  exit 1
fi

echo "==> replay: same key returns the same execution and no second run"
run_operator apps maintenance run "$APP" --async --json --idempotency-key "$KEY" >"$WORK/replay.json"
python3 - "$WORK/first.json" "$WORK/replay.json" <<'PY'
import json, sys
first, replay = (json.load(open(p, encoding="utf-8")) for p in sys.argv[1:])
assert replay.get("code") == "replayed", replay
assert replay.get("replayed") is True, replay
assert replay.get("execution_id") == first.get("execution_id"), (first, replay)
assert replay.get("run_id") == first.get("run_id"), (first, replay)
PY

echo "==> follow: replay the intent and wait for exactly one typed terminal"
set +e
run_operator apps maintenance run "$APP" --follow --json --idempotency-key "$KEY" >"$WORK/follow.ndjson"
FOLLOW_EXIT=$?
set -e
if [[ "$FOLLOW_EXIT" != "0" && "$FOLLOW_EXIT" != "11" ]]; then
  echo "maintenance follow exited $FOLLOW_EXIT" >&2
  tail -n 5 "$WORK/follow.ndjson" >&2
  exit "$FOLLOW_EXIT"
fi
python3 - "$WORK/follow.ndjson" "$FIRST_EXECUTION" "$FOLLOW_EXIT" >"$WORK/terminal.json" <<'PY'
import json, sys
lines = [json.loads(line) for line in open(sys.argv[1], encoding="utf-8") if line.strip()]
terminal = [line for line in lines if line.get("type") == "summary"]
assert len(terminal) == 1, terminal
doc = terminal[0]
assert doc.get("schema_version") == 1, doc
assert doc.get("execution_id") == sys.argv[2], doc
assert doc.get("exit_code") == int(sys.argv[3]), doc
assert doc.get("execution", {}).get("status") not in ("queued", "running", None), doc
print(json.dumps(doc))
PY

PROPOSAL_ID=$(json_value "$WORK/terminal.json" execution.proposal_id)
if [[ -z "$PROPOSAL_ID" ]]; then
  PROPOSAL_ID=${DIBBLA_E2E_PROPOSAL_ID:-}
fi
if [[ -z "$PROPOSAL_ID" ]]; then
  echo "run produced no proposal; set DIBBLA_E2E_PROPOSAL_ID to a ready maintenance proposal on the fixture" >&2
  exit 1
fi

echo "==> proposal: list and fetch exact server diff + evidence"
run_reviewer apps proposals list "$APP" --json >"$WORK/proposals.json"
run_reviewer apps proposals show "$APP" "$PROPOSAL_ID" --diff --json >"$WORK/review.json"
python3 - "$WORK/proposals.json" "$WORK/review.json" "$PROPOSAL_ID" <<'PY'
import json, sys
listing, review = (json.load(open(p, encoding="utf-8")) for p in sys.argv[1:3])
pid = sys.argv[3]
assert any(p.get("id") == pid for p in (listing.get("proposals") or [])), pid
proposal, diff_response = review["proposal"], review["diff"]
assert proposal.get("id") == pid, proposal
assert proposal.get("source") == "maintenance_agent", proposal
assert proposal.get("status") == "ready", proposal
assert proposal.get("decision", {}).get("can_decide") is True, proposal.get("decision")
diff = diff_response.get("diff", {})
assert diff.get("base_sha") == proposal.get("base_sha"), diff
assert diff.get("head_sha") == proposal.get("head_sha"), diff
assert diff.get("total_files", 0) > 0, diff
assert diff_response.get("evidence"), diff_response
PY

echo "==> distinct approval: reviewer decision differs from synthetic author"
run_reviewer apps proposals approve "$APP" "$PROPOSAL_ID" --yes --json >"$WORK/approved.json"
python3 - "$WORK/review.json" "$WORK/approved.json" <<'PY'
import json, sys
before, approved = (json.load(open(p, encoding="utf-8")) for p in sys.argv[1:])
author = before["proposal"].get("author_id")
decider = approved.get("decision_by")
assert author and decider and author != decider, (author, decider)
assert approved.get("status") in {"queued", "deploying", "finalizing", "shipped"}, approved
PY

echo "PASS: run=$FIRST_RUN execution=$FIRST_EXECUTION replayed=true proposal=$PROPOSAL_ID distinct_approval=true"
