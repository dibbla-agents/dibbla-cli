# Guardrail fixtures

Reference inputs for walking a `guardrails.md` check by hand.

`guardrails.md` is **instructions an agent reads**, not executable code. There is
no runner, and two readers can read one row differently. These fixtures exist so
that a check's acceptance can be *repeated* by a second person rather than
resting on the first person's verdict: walk the check against each fixture and
compare your verdict to the expected one below. A disagreement is a defect in
the check's wording, and should be fixed there.

## Check 9 — build-context readiness (P-0009)

| Fixture | Expected verdict |
|---|---|
| `check9-copy-vendor.Dockerfile` | **BLOCKER** — `COPY vendor/ ./vendor/` at line 6 copies a directory `deploy-api` strips from the build context. |
| `check9-multistage-from.Dockerfile` | **PASS** — the only `COPY` naming `dist` is `COPY --from=build`, which reads from an earlier build stage, not from the upload archive. Precision rule 1 exempts it. Flagging this would be a false positive. |

The second fixture is the important one. A check that fires on both is
indistinguishable from a check that fires on the string `dist`, and would train
agents to skip Check 9 — which is worse than not having it.
