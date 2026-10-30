// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos regression for the §25.4 escalation emission-retry
// (F-REL-1). The §25.4 emission path writes the escalation_created event
// to two destinations — the gateway's Redis stream and its in-memory ring
// buffer. When BOTH are unreachable at escalation-creation time, the
// record is stored with emitted=false and a background goroutine retries
// the publish every 30s until a destination recovers. Before this fix the
// retry path (escalation.Service.RetryEmission) had no production caller,
// so an escalation created during a dual Redis-plus-gateway-buffer outage
// was recovered as a record but never pushed once a destination came back.
//
// This test injects the dual-destination outage (the emitter rejects the
// publish), creates the escalation through the real escalation.Service,
// asserts emitted=false, then recovers one destination (the emitter now
// accepts) and runs the retry tick the leader-only reconciler drives,
// asserting the record transitions to emitted=true and the push fired.
// The retry tick is the closure cmd/lenny-ops wires into
// Reconcilers.EscalationEmissionRetry, exercised here as the unit of work
// the leader loop calls.

package tier8_chaos_test

import (
	"context"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/escalation"
)

// dualDestinationEmitter models the §25.4 escalation_created emission
// path's two destinations (the gateway Redis stream and its in-memory
// ring buffer) collapsed into a single up/down switch: a publish succeeds
// only when at least one destination is up. It records every attempt so
// the test can assert the retry actually fired the push on recovery.
type dualDestinationEmitter struct {
	mu       sync.Mutex
	up       bool
	attempts int
	pushes   int
}

// EmitEscalationCreated reports success only while a destination is up.
// Each call is one attempt; a successful call is one push that reached a
// live destination.
func (e *dualDestinationEmitter) EmitEscalationCreated(escalation.Escalation) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.attempts++
	if !e.up {
		return false
	}
	e.pushes++
	return true
}

func (e *dualDestinationEmitter) recover() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.up = true
}

func (e *dualDestinationEmitter) pushCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pushes
}

// spec: 25.4 (escalation emission-retry, lines 2404,2429)
// diagnosis: §25.4 escalation emission-retry (F-REL-1) regressed. An
// escalation created while both the Redis emitter and the gateway buffer
// are unavailable is stored with emitted=false. Once a destination
// recovers, the leader-only emission-retry reconciler tick must re-publish
// the record and flip emitted=true. A failure means the retry path has no
// effect — the escalation stays emitted=false forever and the
// escalation_created event never reaches webhook/SSE subscribers (e.g.
// PagerDuty), the exact F-REL-1 gap: pre-fix RetryEmission had no
// production caller, so the record was recovered but never pushed.
func TestEscalationEmissionRetryAfterDualDestinationRecovery(t *testing.T) {
	ctx := context.Background()

	// Inject the dual-destination outage: both the Redis stream and the
	// gateway buffer are unreachable at creation time, so the emitter
	// rejects every publish.
	em := &dualDestinationEmitter{up: false}
	svc := escalation.NewService(em)

	// Create the escalation during the outage. §25.4: the record is stored
	// (in the always-present Tier 3 buffer here) with emitted=false.
	esc, err := svc.Create(ctx, escalation.CreateRequest{
		Severity: escalation.SeverityCritical,
		Summary:  "warm pool exhausted, scaling failed",
		Source:   "prod-watchdog",
	})
	if err != nil {
		t.Fatalf("create escalation during dual-destination outage: %v", err)
	}
	if esc.Emitted {
		t.Fatalf("emitted=true despite both destinations down; want emitted=false for the retry path")
	}
	if got := em.pushCount(); got != 0 {
		t.Fatalf("push fired %d time(s) during the outage; want 0", got)
	}

	// A retry tick during the still-down outage must not flip the flag:
	// the record stays emitted=false until a destination recovers.
	if n := svc.RetryEmission(ctx); n != 0 {
		t.Fatalf("retry emitted %d record(s) while both destinations are down; want 0", n)
	}
	if again, err := svc.Get(ctx, esc.ID); err != nil {
		t.Fatalf("get escalation: %v", err)
	} else if again.Emitted {
		t.Fatalf("emitted=true after a retry that ran during the outage; want emitted=false")
	}

	// Recover one destination (the gateway buffer comes back). This is the
	// half-recovery §25.4 names: a single destination is enough for the
	// emission to succeed.
	em.recover()

	// Run the emission-retry tick once — the unit of work the leader-only
	// Reconcilers.EscalationEmissionRetry loop drives in cmd/lenny-ops.
	if n := svc.RetryEmission(ctx); n != 1 {
		t.Fatalf("retry emitted %d record(s) after recovery; want exactly 1", n)
	}

	// The record transitioned to emitted=true and the push reached the
	// recovered destination.
	recovered, err := svc.Get(ctx, esc.ID)
	if err != nil {
		t.Fatalf("get escalation after retry: %v", err)
	}
	if !recovered.Emitted {
		t.Errorf("emitted=false after the retry tick fired on recovery; the escalation_created event never reached subscribers (F-REL-1)")
	}
	if got := em.pushCount(); got != 1 {
		t.Errorf("push fired %d time(s) after recovery; want exactly 1 (the §25.4 exactly-once emission)", got)
	}

	// Exactly-once: a second retry tick must not re-publish an already
	// emitted record.
	if n := svc.RetryEmission(ctx); n != 0 {
		t.Errorf("second retry re-emitted %d record(s); want 0 (emission is exactly-once)", n)
	}
	if got := em.pushCount(); got != 1 {
		t.Errorf("push fired %d time(s) total; want exactly 1 across both retry ticks", got)
	}
}
