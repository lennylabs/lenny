// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency check for the scope of the per-slot sub-state
// machine. Every session is bound to a slot on the pod that runs it, whatever
// the pool's maxConcurrentSessions, so the sub-states that describe one slot's
// own progress (`slot_assigned`, `receiving_uploads`, `running`,
// `slot_cleanup`, and `released`) hold on a pod of either concurrency. Two
// further edges, `running ──→ failed` and `slot_cleanup ──→ leaked`, keep the
// concurrency condition, because the accounting they feed is the Redis
// slot-counter occupancy and the ceil(maxConcurrentSessions/2) unhealthy
// threshold an exclusive pod has no entry in.
//
// Three sites state that scope and must agree: the §6.2 pod state machine, the
// §7.2 cross-reference a reader follows to reach it, and the reader-facing
// mirror in docs/reference/state-machines.md. A site that keeps the
// concurrency condition on the general sub-states tells a reader the machine
// applies to concurrent pods alone.
//
// spec: 6.2 (per-slot sub-states, concurrent occupancy), 7.2 (cross-reference
// into the pod state machine), 29.10 (co-tenancy on a concurrent-session pod).

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// generalSlotEdges are the sub-state transitions a slot makes on a pod of
// either concurrency, spelled as the §6.2 diagram writes them.
var generalSlotEdges = []string{
	"slot_assigned ──→ receiving_uploads",
	"receiving_uploads ──→ running",
	"running ──→ slot_cleanup",
	"slot_cleanup ──→ released",
}

// spec: 6.2, 7.2
// diagnosis: §6.2 or the §7.2 cross-reference into it scopes the per-slot
// sub-states to a pod serving more than one concurrent session, while every
// session holds a slot on every pod. A reader following the cross-reference
// arrives at a machine they have just been told does not apply to their pool,
// and the exclusive-pod slot progression is stated nowhere.
func TestPerSlotSubStatesAreStatedForAPodOfEitherConcurrency(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	s62 := specSection(t, filepath.Join(specDir, "06_warm-pod-model.md"), "### 6.2 ")
	generalHeader := requireLine(t, s62, "Per-slot sub-states (tracked per session")
	requireAllContain(t, "§6.2 per-slot sub-state block header", generalHeader, []string{
		"a pod of either concurrency",
	})
	generalBlock := s62[strings.Index(s62, generalHeader):]
	for _, edge := range generalSlotEdges {
		if !strings.Contains(generalBlock, edge) {
			t.Errorf("spec/06 §6.2 states no %q edge for a pod of either concurrency", edge)
		}
	}

	// The two edges whose accounting reads the Redis slot-counter occupancy stay
	// with the concurrent-occupancy block.
	scopedHeader := requireLine(t, s62, "Per-slot sub-states scoped to concurrent occupancy")
	scopedBlock := s62[strings.Index(s62, scopedHeader):strings.Index(s62, generalHeader)]
	for _, edge := range []string{"running ──→ failed", "slot_cleanup ──→ leaked"} {
		if !strings.Contains(scopedBlock, edge) {
			t.Errorf("spec/06 §6.2 concurrent-occupancy block states no %q edge; its accounting reads the Redis slot-counter occupancy", edge)
		}
	}
	for _, edge := range generalSlotEdges {
		if strings.Contains(scopedBlock, edge) {
			t.Errorf("spec/06 §6.2 scopes the %q edge to concurrent occupancy; every session holds a slot on every pod", edge)
		}
	}

	// The §7.2 cross-reference sends a reader to the deconditioned machine.
	s72 := specSection(t, filepath.Join(specDir, "07_session-lifecycle.md"), "### 7.2 ")
	crossRef := requireLine(t, s72, "per-slot sub-states")
	if strings.Contains(crossRef, "maxConcurrentSessions > 1") {
		t.Errorf("spec/07 §7.2 sends a reader to the per-slot sub-states as used when maxConcurrentSessions > 1: %s", strings.TrimSpace(crossRef))
	}
}

// spec: 6.2, 29.10
// diagnosis: docs/reference/state-machines.md states the per-slot sub-states
// only inside its concurrent-session occupancy section, so the reader-facing
// mirror carries a condition the specification has dropped. An author of an
// exclusive-pool integration reads the page and concludes their session's slot
// has no sub-states.
func TestStateMachinesDocMirrorsThePerSlotSubStateScope(t *testing.T) {
	root := repoRoot(t)
	doc := stateMachinesDoc(t, root)

	general := section(doc, "Per-slot sub-states")
	if general == "" {
		t.Fatal("state-machines.md: no 'Per-slot sub-states' section; the per-slot machine holds on a pod of either concurrency and needs a section of its own")
	}
	heading := strings.SplitN(general, "\n", 2)[0]
	if strings.Contains(heading, "maxConcurrentSessions") {
		t.Errorf("state-machines.md scopes its per-slot sub-state section to a concurrency condition: %s", strings.TrimSpace(heading))
	}
	requireAllContain(t, "state-machines.md per-slot sub-state section", general, []string{
		"whatever the pool's `maxConcurrentSessions`",
		"`slot_assigned`",
		"`receiving_uploads`",
		"`running`",
		"`slot_cleanup`",
		"`released`",
	})

	concurrent := section(doc, "Concurrent-session occupancy")
	if concurrent == "" {
		t.Fatal("state-machines.md: 'Concurrent-session occupancy' section not found (renamed or removed?)")
	}
	if strings.Contains(concurrent, "`slot_assigned`") {
		t.Error("state-machines.md states the general per-slot progression inside its concurrent-session occupancy section; every session holds a slot on every pod")
	}
	requireAllContain(t, "state-machines.md concurrent-session occupancy section", concurrent, []string{
		"`running -> failed`",
		"`slot_cleanup -> leaked`",
		"`ceil(maxConcurrentSessions/2)` slots fail",
	})
}
