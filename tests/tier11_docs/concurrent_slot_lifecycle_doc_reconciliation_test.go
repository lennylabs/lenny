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
//     This lands in §5.2 (Session count limit bullet), §6.2 (a new
//     concurrent-occupancy diagram edge), and §12 (the sessions_served schema
//     prose and column comment), and it must be represented as its own
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
// loss trigger restatement in Horizontal Scaling), 12.1 (sessions_served
// schema), 16.1 (retirement counters + recording rule).

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
	triggerLine := requireLine(t, s52, "Whole-pod replacement trigger")
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

// spec: 5.2, 6.2, 12.1
// diagnosis: §5.2, §6.2, §12, or the state-machines.md concurrent-session
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

	// §12 sessions_served schema: evaluated on each session release on a
	// concurrent non-vm-restart pool.
	s12 := readDocPage(t, filepath.Join(specDir, "12_storage-architecture.md"))
	if !strings.Contains(s12, "on a concurrent non-`vm-restart` pool evaluated on each session release") &&
		!strings.Contains(s12, "on each session release on a concurrent non-`vm-restart` pool") {
		t.Error("spec/12 does not state the per-release maxSessionsPerPod evaluation point for a concurrent non-vm-restart pool; it must match the amended §5.2")
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
