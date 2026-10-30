// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the §5.2 concurrent-session slot
// lifecycle reconciliation (proposal 0035, F-5.2.31). SPEC-D amended four
// normative statements that the reader-facing docs/reference/state-machines.md
// and docs/reference/metrics.md must agree with:
//
//  1. Persistent leak counting. A `failed` slot is counted within a rolling
//     5-minute window; a `leaked` slot is counted persistently for as long as
//     the slot remains leaked. This lands in §5.2 (whole-pod replacement
//     trigger), §6.2 (leaked slot semantics + the concurrent-occupancy diagram
//     edge), and the §10.1 (Horizontal Scaling) whole-pod-connection-loss
//     restatement, and it must match the concurrent-session occupancy table row
//     in state-machines.md.
//  2. The per-release `maxSessionsPerPod` drain. On a concurrent
//     (`maxConcurrentSessions > 1`) non-`vm-restart` pool the pod drains on the
//     session release that drives the served-session count to
//     `maxSessionsPerPod`, decoupled from the occupancy-zero whole-pod scrub.
//     This lands in §5.2 (Session count limit bullet) and §6.2 (a new
//     concurrent-occupancy diagram edge), and it must be represented as its own
//     `claimed → draining` row in the state-machines.md concurrent-session
//     occupancy table, parallel to the existing `maxPodUptimeSeconds` trigger.
//  3. Two partitioned retirement counters plus a summing rule. The gateway
//     counter `lenny_gateway_pod_retirement_total` carries `session_count_limit`
//     and `scrub_failure_limit`; the controller counter
//     `lenny_controller_pod_retirement_total` carries `uptime_limit`; the
//     recording rule `lenny:pod_retirement:total` sums them. This lands in §5.2
//     (retirement-logging sentence) and §16.1 (the two metric rows plus the
//     recording rule), and it must match docs/reference/metrics.md.
//  4. The `CreationTimestamp`-derived level-triggered uptime drain. The
//     `maxPodUptimeSeconds` retirement is a WarmPoolController-owned drain
//     derived from the pod `CreationTimestamp`, level-triggered regardless of
//     session activity. This lands in §5.2 (Uptime limit bullet), §6.2 (base
//     occupancy-projection `idle → draining` edge and the concurrent-occupancy
//     uptime edge), and §16.1 (the controller counter row), and it must match
//     the base recycle-transitions `idle → draining` row in state-machines.md.
//
// This test pins that agreement so a later edit to any one site cannot silently
// diverge from the amended spec. It reads the repository state directly (no
// build tag, no infrastructure), the same posture as the other tier-11 doc
// checks.
//
// spec: 5.2 (per-release maxSessionsPerPod drain, persistent leak counting,
// CreationTimestamp uptime drain, retirement counters), 6.2 (leaked slot
// semantics, concurrent-occupancy diagram edges), 10.1 (whole-pod-connection-
// loss trigger restatement in Horizontal Scaling), 16.1 (retirement counters +
// recording rule).

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// stateMachinesDoc reads docs/reference/state-machines.md into a string.
func stateMachinesDoc(t *testing.T, root string) string {
	t.Helper()
	return readDocPage(t, filepath.Join(root, "docs", "reference", "state-machines.md"))
}

// spec: 5.2, 6.2, 10.1
// diagnosis: §5.2, §6.2, the §10.1 whole-pod-connection-loss restatement,
//
//	or the state-machines.md concurrent-session occupancy table disagree on the
//	leaked-slot counting lifetime. Proposal 0035 (SPEC-D) made a `failed` slot
//	counted within a rolling 5-minute window and a `leaked` slot counted
//	persistently for as long as the slot remains leaked, so a pod accumulating
//	permanent leaks slowly still reaches the ceil(maxConcurrentSessions/2)
//	unhealthy threshold. A failure here means one site reverted to counting
//	leaks within the 5-minute window (the pre-fix framing that ages permanent
//	leaks out of the health count), leaving the leaked-slot lifetime
//	self-contradictory across the spec and the reader-facing state-machine doc.
func TestLeakedSlotCountingLifetimeAgrees_F5231(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	// §6.2 leaked-slot semantics paragraph: leaks persistent, failures windowed.
	s62 := specSection(t, filepath.Join(specDir, "06_warm-pod-model.md"), "### 6.2 ")
	leakedLine := requireLine(t, s62, "`leaked` slot semantics")
	requireAllContain(t, "§6.2 leaked slot semantics", leakedLine, []string{
		"persists until pod termination",
		"counted with different lifetimes",
		"rolling 5-minute window",
		"counted persistently for as long as the slot remains leaked",
	})

	// §5.2 whole-pod replacement trigger bullet: same lifetime split.
	s52 := specSection(t, filepath.Join(specDir, "05_runtime-registry-and-pool-model.md"), "### 5.2 ")
	// Anchor on the bullet's own label: the **Failure isolation:** bullet
	// above it also names the trigger, and a bare-name match lands there.
	triggerLine := requireLine(t, s52, "- **Whole-pod replacement trigger:**")
	requireAllContain(t, "§5.2 whole-pod replacement trigger", triggerLine, []string{
		"counted within a rolling 5-minute window",
		"counted persistently for as long as the slots remain leaked",
	})

	// §10.1 whole-pod-connection-loss restatement: window for failures,
	// persistent for leaks. The restatement lives in the §10.1 Horizontal
	// Scaling text; scope to the sentence carrying the trigger.
	s10 := readDocPage(t, filepath.Join(specDir, "10_gateway-internals.md"))
	restateLine := lineContaining(s10, "The whole-pod replacement trigger")
	if restateLine == "" {
		t.Fatal("spec/10: no line restates the whole-pod replacement trigger (renamed or removed?)")
	}
	requireAllContain(t, "§10 whole-pod-connection-loss restatement", restateLine, []string{
		"fail (5-minute window)",
		"leak (persistent)",
	})
	if strings.Contains(restateLine, "fail or leak** within a 5-minute window") ||
		strings.Contains(restateLine, "fail or leak within a 5-minute window") {
		t.Error("spec/10 restatement still applies the 5-minute window to leaks; leaks are counted persistently per the amended §5.2/§6.2")
	}

	// state-machines.md concurrent-session occupancy table fail-or-leak row.
	doc := stateMachinesDoc(t, root)
	concurrentSec := section(doc, "Concurrent-session occupancy")
	if concurrentSec == "" {
		t.Fatal("state-machines.md: 'Concurrent-session occupancy' section not found (renamed or removed?)")
	}
	failLeakRow := lineContaining(concurrentSec, "slots fail")
	if failLeakRow == "" {
		t.Fatal("state-machines.md concurrent-session occupancy table: no 'slots fail' row (renamed or removed?)")
	}
	requireAllContain(t, "state-machines.md fail-or-leak row", failLeakRow, []string{
		"rolling 5-minute window",
		"counted persistently for as long as the slots remain leaked",
	})
	// The pre-fix row applied the 5-minute window to both failures and leaks.
	if strings.Contains(failLeakRow, "fail or leak within a 5-minute window") {
		t.Error("state-machines.md concurrent-session occupancy table still applies the 5-minute window to leaks; the amended §6.2 counts leaks persistently")
	}
}

// spec: 5.2, 6.2
// diagnosis: §5.2, §6.2, or the state-machines.md concurrent-session
//
//	occupancy table disagree on the per-release maxSessionsPerPod drain.
//	Proposal 0035 (SPEC-D) decoupled the concurrent non-`vm-restart` pool's
//	maxSessionsPerPod retirement from the occupancy-zero whole-pod scrub: the
//	pod drains on the session release that drives the served-session count to
//	maxSessionsPerPod, because a persistently leaked slot can hold total
//	occupancy above zero indefinitely. A failure here means one site still ties
//	the concurrent-pool retirement to the whole-pod scrub (the pre-fix framing
//	that never fires while a leaked slot pins occupancy above zero), or the
//	state-machine doc lost its per-release `claimed → draining` row, leaving the
//	retirement trigger self-contradictory across the spec and the doc.
func TestPerReleaseSessionCountDrainAgrees_F5231(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	// §5.2 Session count limit bullet: per-release drain on a concurrent
	// non-vm-restart pool, decoupled from the whole-pod scrub.
	s52 := specSection(t, filepath.Join(specDir, "05_runtime-registry-and-pool-model.md"), "### 5.2 ")
	sessionCountLine := requireLine(t, s52, "Session count limit")
	requireAllContain(t, "§5.2 Session count limit", sessionCountLine, []string{
		"On a concurrent pool (`maxConcurrentSessions > 1`) whose `scrubProfile` is not `vm-restart`",
		"session release that drives the served-session count to `maxSessionsPerPod`",
		"decoupled from the whole-pod scrub",
	})

	// §6.2 concurrent-occupancy diagram carries the per-release maxSessionsPerPod
	// edge parallel to the maxPodUptimeSeconds edge.
	s62 := specSection(t, filepath.Join(specDir, "06_warm-pod-model.md"), "### 6.2 ")
	perReleaseEdge := lineContaining(s62, "served-session count reaches recycle.maxSessionsPerPod")
	if perReleaseEdge == "" {
		t.Fatal("spec/06 §6.2 concurrent-occupancy diagram: no per-release maxSessionsPerPod edge (renamed or removed?)")
	}

	// state-machines.md concurrent-session occupancy table: a per-release
	// maxSessionsPerPod `claimed → draining` row parallel to the uptime trigger.
	doc := stateMachinesDoc(t, root)
	concurrentSec := section(doc, "Concurrent-session occupancy")
	if concurrentSec == "" {
		t.Fatal("state-machines.md: 'Concurrent-session occupancy' section not found (renamed or removed?)")
	}
	perReleaseRow := lineContaining(concurrentSec, "Served-session count reaches")
	if perReleaseRow == "" {
		t.Fatal("state-machines.md concurrent-session occupancy table: no per-release maxSessionsPerPod `claimed → draining` row; the amended §5.2 decouples the concurrent-pool retirement from the whole-pod scrub")
	}
	requireAllContain(t, "state-machines.md per-release row", perReleaseRow, []string{
		"`recycle.maxSessionsPerPod`",
		"session release",
		"`vm-restart`",
		"decoupled from the occupancy-zero whole-pod scrub",
	})
}

// spec: 5.2, 16.1
// diagnosis: §5.2, §16.1, or docs/reference/metrics.md disagree on the two
//
//	partitioned retirement counters. Proposal 0035 (CODE-F) split the single
//	lenny_pod_retirement_total into a gateway-owned
//	lenny_gateway_pod_retirement_total (session_count_limit, scrub_failure_limit)
//	and a controller-owned lenny_controller_pod_retirement_total (uptime_limit),
//	summed by the recording rule lenny:pod_retirement:total. A failure here
//	means one site still names the old single counter, mis-attributes a reason
//	to the wrong process counter, or drops the summing recording rule, leaving
//	an operator dashboard under-reporting retirements across the two processes.
func TestRetirementCounterPartitionAgrees_F5231(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	// The old single counter name must be gone from every reconciled site.
	oldCounter := "lenny_pod_retirement_total"
	newGateway := "lenny_gateway_pod_retirement_total"
	newController := "lenny_controller_pod_retirement_total"
	summingRule := "lenny:pod_retirement:total = sum(lenny_gateway_pod_retirement_total) + sum(lenny_controller_pod_retirement_total)"

	// §5.2 retirement-logging sentence: gateway counter carries
	// session_count_limit/scrub_failure_limit; controller counter carries
	// uptime_limit.
	s52 := specSection(t, filepath.Join(specDir, "05_runtime-registry-and-pool-model.md"), "### 5.2 ")
	logLine := requireLine(t, s52, "increments `"+newGateway+"`")
	requireAllContain(t, "§5.2 retirement-logging sentence", logLine, []string{
		"`" + newGateway + "`",
		"`session_count_limit` or `scrub_failure_limit`",
		"`" + newController + "`",
		"`reason=\"uptime_limit\"`",
	})
	// The bare old-counter token must not be reintroduced. Guard with a word
	// boundary so the gateway/controller counters (which share the suffix) do
	// not false-positive.
	assertNoBareOldCounter(t, "§5.2", logLine, oldCounter)

	// §16.1 metric inventory: both rows plus the summing recording rule.
	s16 := readDocPage(t, filepath.Join(specDir, "16_observability.md"))
	gatewayRow := lineContaining(s16, "`"+newGateway+"`")
	if gatewayRow == "" {
		t.Fatalf("spec/16 §16.1 has no %s row", newGateway)
	}
	requireAllContain(t, "§16.1 gateway retirement row", gatewayRow, []string{
		"`session_count_limit`",
		"`scrub_failure_limit`",
	})
	if strings.Contains(gatewayRow, "uptime_limit") {
		t.Errorf("spec/16 §16.1 %s row lists uptime_limit; that reason belongs to the controller counter", newGateway)
	}
	controllerRow := lineContaining(s16, "`"+newController+"`")
	if controllerRow == "" {
		t.Fatalf("spec/16 §16.1 has no %s row", newController)
	}
	requireAllContain(t, "§16.1 controller retirement row", controllerRow, []string{
		"`uptime_limit`",
		summingRule,
	})

	// docs/reference/metrics.md: both counters, correct reasons, summing rule.
	metrics := readDocPage(t, filepath.Join(root, "docs", "reference", "metrics.md"))
	docGatewayRow := lineContaining(metrics, "`"+newGateway+"`")
	if docGatewayRow == "" {
		t.Fatalf("docs/reference/metrics.md has no %s row", newGateway)
	}
	requireAllContain(t, "metrics.md gateway retirement row", docGatewayRow, []string{
		"`session_count_limit`",
		"`scrub_failure_limit`",
	})
	if strings.Contains(docGatewayRow, "uptime_limit") {
		t.Errorf("docs/reference/metrics.md %s row lists uptime_limit; that reason belongs to the controller counter", newGateway)
	}
	docControllerRow := lineContaining(metrics, "`"+newController+"`")
	if docControllerRow == "" {
		t.Fatalf("docs/reference/metrics.md has no %s row", newController)
	}
	requireAllContain(t, "metrics.md controller retirement row", docControllerRow, []string{
		"`uptime_limit`",
		summingRule,
	})
}

// spec: 5.2, 6.2, 16.1
// diagnosis: §5.2, §6.2, §16.1, or the state-machines.md recycle-transitions
//
//	table disagree on the maxPodUptimeSeconds drain mechanism. Proposal 0035
//	(SPEC-D + CODE-E) made the uptime retirement a WarmPoolController-owned
//	drain derived from the pod CreationTimestamp, level-triggered regardless of
//	session activity, replacing the pre-fix gateway-driven dispatch-gated check
//	that could not reclaim an idle or placement-frozen over-uptime pod. A
//	failure here means one site reverted to the dispatch-gated "checked before
//	next assignment" framing or attributed the uptime drain to the gateway,
//	leaving the reclaimer unable to drain an idle over-uptime pod and
//	contradicting §4.6.1.
func TestUptimeDrainMechanismAgrees_F5231(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	// §5.2 Uptime limit bullet: WarmPoolController-owned, CreationTimestamp-
	// derived, level-triggered.
	s52 := specSection(t, filepath.Join(specDir, "05_runtime-registry-and-pool-model.md"), "### 5.2 ")
	uptimeLine := requireLine(t, s52, "Uptime limit")
	requireAllContain(t, "§5.2 Uptime limit", uptimeLine, []string{
		"The WarmPoolController derives the pod's uptime from its `CreationTimestamp`",
		"level-triggers the transition to `draining`",
		"regardless of session activity",
		"`expiredByUptime`",
	})
	// The pre-fix bullet said the gateway checks uptime before dispatching the
	// next session; that dispatch-gated framing must be gone.
	if strings.Contains(uptimeLine, "checks uptime before dispatching the next session") {
		t.Error("§5.2 Uptime limit still describes the gateway dispatch-gated check; the amended §5.2 makes the drain WarmPoolController-owned and CreationTimestamp-derived")
	}

	// §6.2 base occupancy-projection idle → draining uptime edge: level-triggered
	// from the pod CreationTimestamp.
	s62 := specSection(t, filepath.Join(specDir, "06_warm-pod-model.md"), "### 6.2 ")
	idleEdge := lineContaining(s62, "level-triggers the drain from the pod CreationTimestamp")
	if idleEdge == "" {
		t.Fatal("spec/06 §6.2 base occupancy-projection idle → draining edge does not state the CreationTimestamp-derived level-triggered drain; it must match the amended §5.2/§4.6.1")
	}
	if strings.Contains(s62, "checked before next assignment") {
		t.Error("spec/06 §6.2 still carries the dispatch-gated 'checked before next assignment' uptime framing; the amended §5.2/§4.6.1 make it a level-triggered CreationTimestamp drain")
	}

	// §16.1 controller counter row attributes the uptime drain to the
	// WarmPoolController and the pod CreationTimestamp.
	s16 := readDocPage(t, filepath.Join(specDir, "16_observability.md"))
	controllerRow := lineContaining(s16, "`lenny_controller_pod_retirement_total`")
	if controllerRow == "" {
		t.Fatal("spec/16 §16.1 has no controller retirement counter row")
	}
	requireAllContain(t, "§16.1 controller retirement row", controllerRow, []string{
		"level-triggered `maxPodUptimeSeconds` drain",
		"pod `CreationTimestamp`",
	})

	// state-machines.md base recycle-transitions idle → draining row: the
	// WarmPoolController CreationTimestamp-derived level-triggered drain.
	doc := stateMachinesDoc(t, root)
	recycleSec := section(doc, "Recycle transitions")
	if recycleSec == "" {
		t.Fatal("state-machines.md: 'Recycle transitions' section not found (renamed or removed?)")
	}
	idleRow := lineContaining(recycleSec, "`recycle.maxPodUptimeSeconds` exceeded")
	if idleRow == "" {
		t.Fatal("state-machines.md recycle-transitions table: no idle → draining maxPodUptimeSeconds row (renamed or removed?)")
	}
	requireAllContain(t, "state-machines.md idle → draining uptime row", idleRow, []string{
		"WarmPoolController",
		"`CreationTimestamp`",
		"level-triggers",
		"regardless of session activity",
	})
	// The pre-fix row read "exceeded while idle" with no mechanism; the amended
	// row must state the CreationTimestamp-derived level-triggered drain.
	if strings.Contains(idleRow, "exceeded while idle |") {
		t.Error("state-machines.md idle → draining uptime row still reads 'exceeded while idle' with no mechanism; the amended §5.2/§4.6.1 make it a WarmPoolController CreationTimestamp-derived level-triggered drain")
	}
}

// assertNoBareOldCounter fails when body contains the old single retirement
// counter token as a standalone identifier. The gateway and controller
// counters share the `_pod_retirement_total` suffix, so a naive substring
// match would false-positive on them; this checks the old token is not
// preceded by `gateway_` or `controller_`.
func assertNoBareOldCounter(t *testing.T, site, body, oldToken string) {
	t.Helper()
	idx := 0
	for {
		i := strings.Index(body[idx:], oldToken)
		if i < 0 {
			return
		}
		pos := idx + i
		prefix := body[:pos]
		if !strings.HasSuffix(prefix, "gateway_") && !strings.HasSuffix(prefix, "controller_") {
			t.Errorf("%s still names the removed single counter %q; proposal 0035 (CODE-F) renamed it to the gateway/controller pair", site, oldToken)
			return
		}
		idx = pos + len(oldToken)
	}
}

// collapsedSpec reads a spec file with every run of whitespace collapsed to
// one space, so an assertion can quote a hard-wrapped paragraph, table row,
// or diagram line as one string.
func collapsedSpec(t *testing.T, root, file string) string {
	t.Helper()
	return strings.Join(strings.Fields(readDocPage(t, filepath.Join(root, "spec", file))), " ")
}

// requireNear fails unless body contains anchor, and fails when any of
// forbidden appears within window bytes of the anchor, so a check reads the
// edited sentence rather than an unrelated sentence elsewhere in the file.
func requireNear(t *testing.T, label, body, anchor string, window int, forbidden ...string) {
	t.Helper()
	i := strings.Index(body, anchor)
	if i < 0 {
		t.Errorf("%s: %q not found (reverted or reworded?)", label, anchor)
		return
	}
	lo, hi := max(0, i-window), min(len(body), i+len(anchor)+window)
	for _, f := range forbidden {
		if strings.Contains(body[lo:hi], f) {
			t.Errorf("%s: %q appears beside %q", label, f, anchor)
		}
	}
}

// spec: 5.2 (Slot assignment atomicity), 12.4 (Redis HA and failure modes),
// 10.1.4 (Coordinator-Loss Detection and Hold State), 6.2 (Pod State
// Machine), 4.6.1 (Warm Pool Controller (Pod Lifecycle)), 28.3, 28.6, 29.2,
// 29.10
// diagnosis: a spec site reverted the per-pod slot occupancy from a set keyed
// by session to a decremented counter (an INCR, a reset to 0, an "atomic
// per-pod counter", or "occupancy decremented"), or dropped the stale-member
// release by the gateway leader, so the sites no longer describe one
// occupancy record and one keyed release.
func TestSlotOccupancyIsAKeyedSetAtEverySite_spec_5_2(t *testing.T) {
	root := repoRoot(t)
	s52 := strings.Join(strings.Fields(requireLine(t, specSection(t,
		filepath.Join(root, "spec", "05_runtime-registry-and-pool-model.md"), "### 5.2 "),
		"**Slot assignment atomicity")), " ")
	requireAllContain(t, "§5.2 Slot assignment atomicity", s52, []string{
		"Redis set", "keyed by the session identifier", "is stale", "the stale members",
		"`claimOrphanTimeout`", "`lenny-gateway-leader` Lease",
		"`maxFinalizingTimeoutSeconds`", "the `resuming` watchdog's duration", "`maxWorkspaceSealDurationSeconds`",
	})
	for _, banned := range []string{"INCR", "reset to 0", "atomic per-pod counter"} {
		if strings.Contains(s52, banned) {
			t.Errorf("§5.2 Slot assignment atomicity names %q", banned)
		}
	}

	s10 := requireLine(t, readDocPage(t, filepath.Join(root, "spec", "10_gateway-internals.md")),
		"**Whole-pod connection loss when `maxConcurrentSessions > 1`.**")
	requireAllContain(t, "§10.1.4 whole-pod connection loss", s10, []string{"Redis slot set"})
	for _, banned := range []string{"INCR", "reset to 0", "resets the counter"} {
		if strings.Contains(s10, banned) {
			t.Errorf("§10.1.4 whole-pod connection loss names %q", banned)
		}
	}

	reg := requireLine(t, readDocPage(t, filepath.Join(root, "spec", "28_communication-channels.md")), "| `REG-SLOTCOUNT` |")
	requireAllContain(t, "§28.3 REG-SLOTCOUNT row", reg, []string{"per-pod set"})
	if strings.Contains(reg, "INCR") || strings.Contains(reg, "counter") {
		t.Errorf("§28.3 REG-SLOTCOUNT row describes a counter: %s", reg)
	}

	c06 := collapsedSpec(t, root, "06_warm-pod-model.md")
	requireAllContain(t, "§6.2 concurrent-occupancy diagram", c06, []string{"Redis slot reservation succeeds", "Redis-counter occupancy"})
	if strings.Contains(c06, "occupancy decremented") {
		t.Error("§6.2 concurrent-occupancy diagram still says occupancy decremented")
	}
	requireAllContain(t, "§6.2 Partial occupancy", requireLine(t, readDocPage(t, filepath.Join(root, "spec", "06_warm-pod-model.md")),
		"- **Partial occupancy.**"), []string{"Redis slot set"})

	c28 := collapsedSpec(t, root, "28_communication-channels.md")
	requireAllContain(t, "§28.6", c28, []string{"`REG-SLOTCOUNT` ceilings a pod's concurrent slots"})
	c29 := collapsedSpec(t, root, "29_communication-scenarios.md")
	for label, body := range map[string]string{"§28": c28, "§29": c29} {
		for _, banned := range []string{"atomic per-pod counter", "checks the counter and increments it"} {
			if strings.Contains(body, banned) {
				t.Errorf("%s names %q", label, banned)
			}
		}
	}
	requireAllContain(t, "§29.2 step 8", c29, []string{
		"The gateway reserves the intra-pod slot through the atomic reservation of [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) **Slot assignment atomicity**",
	})
	requireAllContain(t, "§4.6.1", collapsedSpec(t, root, "04_system-components.md"), []string{"gated by the Redis slot set"})
}

// spec: 4.6.3 (CRD Field Ownership and Write Boundaries), 5.2 (Recycle
// lifecycle), 6.2 (Pod State Machine), 4.7, 7.1, 28.3, 29.4
// diagnosis: an edit that states when a release deletes the per-pod claim
// lost the one predicate wording, "ends the occupancy without reuse", or
// reintroduced the retired default disposition ("On the default disposition
// the pod is replaced"), so the sites disagree on which releases delete the
// claim and which patch it recycling.
func TestOccupancyEndingReleasePredicateAtEverySite_spec_4_6_3(t *testing.T) {
	root := repoRoot(t)
	sites := []struct{ file, anchor string }{
		{"04_system-components.md", "when a gateway release ends the occupancy without reuse"},
		{"04_system-components.md", "a release that ends the occupancy without reuse, orphan GC, or pod termination"},
		{"04_system-components.md", "it is deleted by the release that ends the occupancy without reuse"},
		{"04_system-components.md", "When occupancy reaches zero on a recycling pod, unless the release ends the occupancy without reuse"},
		{"04_system-components.md", "or when a release ends the occupancy without reuse. Every release on a pool with `maxConcurrentSessions: 1` ends the occupancy"},
		{"04_system-components.md", "occupancy zero on a recycling pod unless the release ends the occupancy without reuse"},
		{"05_runtime-registry-and-pool-model.md", "When the pod's occupancy reaches zero, unless the release ends the occupancy without reuse"},
		{"05_runtime-registry-and-pool-model.md", "recycles when occupancy reaches zero unless the release ends the occupancy without reuse"},
		{"06_warm-pod-model.md", "the recycle edges above apply unless the last release ends the occupancy without reuse"},
		{"06_warm-pod-model.md", "occupancy reaches zero, unless the release ends the occupancy without reuse under §4.6.3"},
		{"06_warm-pod-model.md", "When occupancy reaches zero on a recycling pod, unless the release ends the occupancy without reuse"},
		{"06_warm-pod-model.md", "the last release deletes the claim when it ends the occupancy without reuse"},
		{"07_session-lifecycle.md", "whole-pod scrub runs when occupancy reaches zero on a recycling pool unless the release ends the occupancy without reuse"},
		{"28_communication-channels.md", "when a release ends the occupancy without reuse ([§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)), and the WarmPoolController leader"},
		{"29_communication-scenarios.md", "When the release ends the occupancy without reuse, the gateway deletes the claim"},
		{"29_communication-scenarios.md", "when occupancy reaches zero on a recycling pod unless the release ends the occupancy without reuse"},
	}
	for _, s := range sites {
		requireNear(t, s.file, collapsedSpec(t, root, s.file), s.anchor, 0)
	}
	for _, file := range []string{"04_system-components.md", "29_communication-scenarios.md"} {
		if strings.Contains(collapsedSpec(t, root, file), "On the default disposition the pod is replaced") {
			t.Errorf("%s still states the retired default disposition", file)
		}
	}
	if strings.Contains(collapsedSpec(t, root, "11_policy-and-controls.md"), "USER_REVOKED") {
		t.Error("§11.4 still names the retired USER_REVOKED shutdown reason")
	}
}

// spec: 4.6.3 (CRD Field Ownership and Write Boundaries), 7.2, 6.2, 4.9,
// 8.3, 15.1, 24.11
// diagnosis: a sentence that says a pod or claim is released lost its link
// to §4.6.3, which states what the release does to the claim, or went back
// to saying the pod returns "to the pool", which is wrong for a release that
// ends the occupancy without reuse.
func TestPodReleasePointersLinkTheClaimRule_spec_4_6_3(t *testing.T) {
	root := repoRoot(t)
	const link = "04_system-components.md#463-crd-field-ownership-and-write-boundaries"
	forbidden := []string{"released to the pool", "released back to the pool", "back to the pool", "back to the warm pool"}
	for _, s := range []struct{ file, anchor string }{
		{"07_session-lifecycle.md", "before the gateway releases the half-claimed replacement pod ([§4.6.3](" + link + ")"},
		{"06_warm-pod-model.md", "the half-claimed replacement pod, which the gateway then releases ([§4.6.3](" + link + "))"},
		{"04_system-components.md", "it releases the claimed pod ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)) and returns"},
		{"08_recursive-delegation.md", "the gateway releases the pod ([Section 4.6.3](" + link + ")) and returns"},
		{"15_external-api-surface.md", "releases the pod claim ([§4.6.3](" + link + "))"},
		{"24_lenny-ctl-command-reference.md", "and the gateway releases the assigned pod ([§4.6.3](" + link + "))"},
	} {
		requireNear(t, s.file, collapsedSpec(t, root, s.file), s.anchor, 200, forbidden...)
	}
}

// spec: 4.6.1 (Orphaned SandboxClaim detection), 11.3 (orphan timeout row),
// 4.7.1 (role and gateway RPC contract), 7.1 (Pod-side reclaim on a failed
// bind)
// diagnosis: the claimOrphanTimeout default or the adapter's absent answer
// drifted: predicate 1 or the §11.3 row no longer states the 15-minute
// default, rule 11 no longer reports an unclean exit after an in-flight or
// unclean Shutdown teardown, or rule 15 or §7.1 still says an absent answer
// is always clean, which would let the stale-slot reaper free a slot whose
// runtime may still run.
func TestOrphanTimeoutAndAbsentAnswerAgree_spec_4_7_1(t *testing.T) {
	root := repoRoot(t)
	c04 := collapsedSpec(t, root, "04_system-components.md")
	requireAllContain(t, "§4.6.1 predicate 1", c04, []string{"(default: 15 minutes"})
	requireAllContain(t, "§11.3 orphan-timeout row", requireLine(t, readDocPage(t, filepath.Join(root, "spec", "11_policy-and-controls.md")),
		"| SandboxClaim orphan timeout"), []string{"900s"})
	rule11 := requireLine(t, readDocPage(t, filepath.Join(root, "spec", "04_system-components.md")), "11. **The no-entry rule.**")
	requireAllContain(t, "§4.7.1 rule 11", rule11, []string{
		"reports an unclean exit while another `Shutdown` that removed the session's entry is still performing its teardown",
		"after the last `Shutdown` that removed the session's entry reported an unclean exit",
	})
	if strings.Contains(c04, "a response reporting `absent` or `superseded` reports a clean exit") {
		t.Error("§4.7.1 rule 15 still reports every absent answer clean")
	}
	if strings.Contains(collapsedSpec(t, root, "07_session-lifecycle.md"), "a reclaim answered `superseded` or `absent` is acknowledged clean") {
		t.Error("§7.1 Pod-side reclaim on a failed bind still acknowledges every absent answer clean")
	}
}
