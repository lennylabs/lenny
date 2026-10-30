# Deviations: Four spec sites disagree on the finalize workspace-failure envelope

The implementor owns this file. It stays empty until an implementation records a departure from what the proposal states.

## Accepted: Tier-11 regression test added for the MinIO runbook correction

**Status:** accepted

**Proposal says.** The summary's "What changes" section says the documentation change (DOCS-1, which drops the finalize clause from the MinIO runbook full-outage tenant notice) carries no code, schema, or test change.

**Landed instead.** Step S5 added one tier-11 test, `tests/tier11_docs/minio_runbook_finalize_outcome_test.go`. The test asserts that no line in the runbook's `### Step 2 — Full outage` section ties `INTERNAL_ERROR` to finalize, and that the section keeps the notice "new session creation is degraded."

**Why.** The step instructions require tests at the listed tiers (0 and 11) and a regression test for each correction. The summary also records that no tier-11 gate reads the corrected text. The test fails against the pre-fix runbook and passes after the fix. It changes no product behavior.

**What a later reader would otherwise get wrong.** A reader who trusts the summary would conclude that DOCS-1 added no test and that no tier-11 check guards the runbook text. The test file above exists, and it fails if a later edit restores a finalize `INTERNAL_ERROR` clause to the full-outage section or drops the degraded-creation notice.
