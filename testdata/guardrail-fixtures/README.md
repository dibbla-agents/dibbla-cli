# Guardrail fixtures

Reference inputs for walking a `guardrails.md` check by hand.

`guardrails.md` is **instructions an agent reads**, not executable code. There is
no runner, and two readers can read one row differently. These fixtures exist so
that a check's acceptance can be *repeated* by a second person rather than
resting on the first person's verdict: walk the check against each fixture and
compare your verdict to the expected one below. A disagreement is a defect in
the check's wording, and should be fixed there.

## Check 5 — personal data (GDPR)

| Fixture | Expected verdict |
|---|---|
| `check5-pii-no-deletion.js` | **BLOCKER** — `customers` holds email, name, address and IP with no route, action or procedure that removes one person (row "No way to delete a person"). Also a WARNING for the request-body log at line 14 and a WARNING for `Sentry.setUser` unless the report names Sentry as a recipient. |
| `check5-pii-with-deletion.js` | **PASS** — same fields, but `DELETE /api/customers/:id` exists and `orders` cascades; the log prints the user id only. The one thing the check still requires is the inventory: the report must list `customers.email`, `customers.full_name`, `customers.address` and name Postmark (email, full name). A report with the checkbox ticked and no inventory is a BLOCKER on the first row, however clean the code. |

The second fixture is where the check earns its keep: it passes on code and
fails on a report that says nothing. The point of Check 5 is the inventory, not
the tick.

## Check 10 — build-context readiness (P-0009)

| Fixture | Expected verdict |
|---|---|
| `check10-copy-vendor.Dockerfile` | **BLOCKER** — `COPY vendor/ ./vendor/` at line 6 copies a directory `deploy-api` strips from the build context. |
| `check10-multistage-from.Dockerfile` | **PASS** — the only `COPY` naming `dist` is `COPY --from=build`, which reads from an earlier build stage, not from the upload archive. Precision rule 1 exempts it. Flagging this would be a false positive. |

The second fixture is the important one. A check that fires on both is
indistinguishable from a check that fires on the string `dist`, and would train
agents to skip Check 10 — which is worse than not having it.
