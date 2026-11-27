---
id: GATE-I17
status: open
kind: test-gap
severity: medium
requirements: []
---

# The docs tier on the host always fails the embedded-manifest check, so a real drift passes as a known failure

## Specification

None: the defect is in how the v1 merge policy runs the docs tier before a merge, `.claude/rules/git-workflow.md` ("v1 merge policy"). The issue is filed under `GATE`.

## Evidence

- `TestEmbeddedManifestsMatchDevProfileRender_spec_17_4` in `tests/docs/embedded_manifests_sync_test.go` compares `pkg/embedded/manifests/manifests.yaml` with a fresh `helm template` render. On the owner's workstation the host's helm renders differently from the gate image's, so the test fails on every run of `lenny-test run --tier docs` on the host, and the digests list it among the known failures beside the 18 of GATE-I9.
- Step 0177.S2 added the chart value `audit.retiredReceiptKeyIds` and did not regenerate the embedded manifests. Its digest listed the docs tier with "the same 19 failures as on `main`", which included this test. Gate-main workflow run `45913906788` on `a9c2b3c29` (2026-11-26, gate run `76738376a896824500c2766a`) then failed the test in the gate image, which renders with the pinned helm. The fix regenerated the manifests in the gate image (`fix/embedded-manifests-retired-receipt-keys`).
- The 0171.S3 merge avoided the gap by rendering the manifests in the gate image by hand.

## Gap

Before a merge, a branch that changes `charts/` cannot tell a real drift of the embedded manifests from the host's rendering difference, so a drift reaches `main` and fails the gate there.

## Suggested resolution

Run the embedded-manifest check in the gate image whenever a branch touches `charts/` or `pkg/embedded/manifests/`, for example by running the docs tier through `lenny-test run --tier docs --container`, or by adding the check to the gate's static tier, and state it in the v1 merge policy.
