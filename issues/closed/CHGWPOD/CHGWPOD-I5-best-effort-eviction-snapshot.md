---
id: CHGWPOD-I5
status: closed
kind: build-gap
severity: medium
requirements: []
closed: obsolete
closed-by: 0131
---

# Best-effort eviction snapshot has no production caller

## Specification

§28.5.3 (Intra-pod), `CH-RUNTIMEOPS` **Degradation.** states that at Standard level a checkpoint degrades to a best-effort snapshot without a runtime pause. §28 states no eviction-specific snapshot.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Best-effort eviction snapshot", status `UNWIRED`, anchor `#2851-gateway-to-pod`, deferral `R16`.
- Surface recorded on the row: "`pkg/adapter/checkpoint.go:179`, `:192`".
- Revert `54292da1b` removed `setEvicting`, `isEvicting`, and both eviction branches of `pkg/adapter/checkpoint.go`; the lines the row cites now hold other code (triage, 2026-11-17).

## Gap

The row records an eviction-only snapshot branch with no production caller.

## Suggested resolution

The row deferred the work to step R16, "Eviction, the holder path", of `gateway-runtime-comms-remediation.md`.

Closed at filing: 0131 ("IC-1: Capture in passes", "Pause budget and convergence") replaces the eviction-only branch with one rule for every capture: "When the pause budget expires during a capture, the adapter ends the capture, the runtime resumes, and the attempt continues on the best-effort path." Step 0131.S1 lands it.
