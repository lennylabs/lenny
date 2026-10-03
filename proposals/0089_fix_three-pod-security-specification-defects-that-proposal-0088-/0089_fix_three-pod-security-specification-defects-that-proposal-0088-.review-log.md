# Review log: Pod-security specification defects left unstaged by proposal 0088

## Standing context

## Ledger

### [f1.human-decisions]
DECISION: OD-1 (group C embedded-model `adapter` name-keying residual: accept and state it, or enforce the read-only-mount structural rule) written to summary.md `## Open decisions for human to make` as one stamped entry with question, recommendation (option 1, staged as SPEC-C, moderate confidence), ground, the losing alternative, the cost of option 2, and the condition that would force option 2; the "No entry yet" preamble is removed. Authority: pkg/podsecurity/podsecurity.go:317, pkg/podsecurity/podsecurity.go:145-148, pkg/controller/sandbox/podspec/podspec.go:550, problem-statement.md:18-19, charts/lenny/templates/controller-rbac.yaml:83. No staged change file and no checklist step changed.
WATCHOUT: charts/lenny/templates/restore-test-cronjob.yaml:59-61 grants the `lenny-restore-test` ClusterRole cluster-wide Job `create`, which yields pods created by the Job controller from a caller-supplied template. OD-1's "only a CREATE-controlling actor" premise holds only if that path cannot place an embedded-model agent pod; it was not adjudicated in this firing.

### [f1.out-of-scope-defects]
FACT: The CH-RUNTIMEOPS peer check, the egress-capture comment and "Line 25" citation hygiene items, and the CH-MSGSOCK and CH-RUNTIMEOPS nonce stay out of scope (disposition out-of-scope-stands). Wrote one entry to summary.md `## Defects in the shipped tree that this proposal does not stage` naming each defect at file:line and the reason it is not staged. Authority: pkg/adapter/runtimeops.go:135, pkg/adapter/runtimeops.go:181, pkg/controller/sandbox/podspec/podspec.go:299-301, cmd/lenny-controller/flags.go:143, pkg/podsecurity/podsecurity.go:234, gateway-runtime-comms-remediation.md:2299-2306. No staged change file and no checklist step changed.
WATCHOUT: 0088 also records the CH-MSGSOCK SO_PEERCRED peer-check defect (F-4.7.25, pkg/adapter/socketruntime.go:179); summary.md Non-goals names only the CH-MSGSOCK nonce. Not adjudicated in this firing.

### [f1.other-proposals]
DECISION: Impact row for 0088 (disposition impact-row) rewritten in summary.md `## Impacts on other proposals`: status dated Approved (2026-10-03); the row now names SPEC-B2 among the membership-paragraph edits, separates CODE-B's constant comment from 0088 TEST-1's edits in the shared test file, states that TEST-1's substring assertion survives the §15.1 row deletion, and states that going ahead is the owner's decision. Authority: proposals/0088_fix_agent-pods-have-no-enforced-rule-that-only-the/0088_fix_agent-pods-have-no-enforced-rule-that-only-the.status.md (approved-date 2026-10-03), spec/13_security-model.md:28, pkg/admission/ephemeral_container_cred_guard/guard.go:107, tests/tier9_security/admission_ephemeral_test.go:42-43 and :96, 0088 non-spec-changes.md:111. No staged change file and no checklist step changed.
