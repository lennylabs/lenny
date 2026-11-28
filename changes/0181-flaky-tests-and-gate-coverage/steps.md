# Steps: 0181 Flaky test fixes and gate coverage across tiers

## Validation owed

- **The time coverage across tiers adds.** On one commit of the 0181.S4 branch with `main` merged in, run `lenny-test gate --tier static,unit,component,contract --cover` twice in the gate image with no other heavy run on the host, once with the component and contract coverage of CV-1 and once without it, and record each run's wall time and each tier's duration in the digest of 0181.S4. A branch whose diff reaches component and contract tests is the measure; the step's own diff reaches few, so the runs use a branch whose changed packages the component tier reaches. Open decision O3 waits on it.

## Steps

### 0181.S1 The secret-probe grant and the stack tests' ports
- status: Building
- ruled-ready: 2026-11-27, the owner approved the build order whose last item is GATE-I14 with the flaky-test fixes; no open decision touches the step
- lanes: [code]
- depends-on: []
- closes: [TOKSVC-I1, LOCALDEV-I2]

FT-1 and FT-2. The secret-probe fixture waits for the grant through a `SubjectAccessReview` before the first probe. The stack package binds its forwarder legs through the `listenTCP` seam, and its tests hand the forwarder held listeners. The fixes are confirmed by repeated runs of `tests/component/secretprobe` and of the unit tests of `pkg/embedded/stack`.

### 0181.S2 The Postgres start timeout
- status: Building
- ruled-ready: 2026-11-27, the owner settled open decision O1 as decision 1, the only open decision that touched the step
- lanes: [code]
- depends-on: []
- closes: [GATE-I16]

FT-3, as decision 1 settles it, in `tests/testinfra/containers`. The next runs of the gate-main workflow confirm that the timeout does not recur.

### 0181.S3 The embedded-manifest check in the gate image
- status: Planned
- lanes: [code, documentation]
- depends-on: []
- closes: [GATE-I17]

EM-1, as open decision O2 settles. Under (a), the check moves to the static tier with its host skip, `gateDocsFiles` drops it, and `TESTING.md` names it. Under (b) or (c), the rule text of the v1 merge policy changes. The diff touches `cmd/lenny-test/` or `tests/static/`, so it is reviewed with the `review` skill before it merges.

### 0181.S4 Coverage of the component and contract tiers
- status: Planned
- ruled-ready: 2026-11-27, the owner approved the build order whose last item is GATE-I14; the issue asks for the measurement before adoption, which open decision O3 then settles
- lanes: [code, documentation]
- depends-on: []
- closes: [GATE-I14]

CV-1 to CV-4 in `cmd/lenny-test`, with their unit tests, and "Restricted runs and coverage" and "Verdict" in `TESTING.md` and the `--cover` line of `cmd/lenny-test/README.md`. The step is built and measured under "Validation owed", and it merges only when open decision O3 adopts it; the rule texts that O3 names change in the same step. The diff touches `cmd/lenny-test/`, so it is reviewed with the `review` skill before it merges.
