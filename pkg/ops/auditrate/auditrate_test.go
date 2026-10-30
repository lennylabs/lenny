// SPDX-License-Identifier: MIT

package auditrate_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/auditrate"
)

var base = time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)

func TestFirstCallEmits(t *testing.T) {
	l := auditrate.New(60)
	if d := l.Decide("sa-1", "pool", "default", base); d != auditrate.Emit {
		t.Errorf("first call = %v, want emit", d)
	}
}

func TestRepeatWithinWindowCoalesces(t *testing.T) {
	l := auditrate.New(60)
	l.Decide("sa-1", "pool", "default", base)

	for _, dt := range []time.Duration{time.Second, 30 * time.Second, 59 * time.Second} {
		if d := l.Decide("sa-1", "pool", "default", base.Add(dt)); d != auditrate.Coalesce {
			t.Errorf("repeat at +%v = %v, want coalesce", dt, d)
		}
	}
}

func TestRepeatAfterWindowEmits(t *testing.T) {
	l := auditrate.New(60)
	l.Decide("sa-1", "pool", "default", base)
	if d := l.Decide("sa-1", "pool", "default", base.Add(61*time.Second)); d != auditrate.Emit {
		t.Errorf("repeat after the 60s window = %v, want emit", d)
	}
}

func TestDistinctResourcesEmitSeparately(t *testing.T) {
	l := auditrate.New(60)
	if d := l.Decide("sa-1", "pool", "alpha", base); d != auditrate.Emit {
		t.Errorf("pool/alpha = %v, want emit", d)
	}
	if d := l.Decide("sa-1", "pool", "beta", base); d != auditrate.Emit {
		t.Errorf("pool/beta = %v, want emit (a distinct resource)", d)
	}
}

func TestRateLimitDropsExcess(t *testing.T) {
	l := auditrate.New(3)
	// Three distinct resources within a minute: all emit.
	for i, id := range []string{"a", "b", "c"} {
		if d := l.Decide("sa-1", "session", id, base.Add(time.Duration(i)*time.Second)); d != auditrate.Emit {
			t.Fatalf("event %d = %v, want emit", i, d)
		}
	}
	// The fourth distinct event for the same account is dropped.
	if d := l.Decide("sa-1", "session", "d", base.Add(4*time.Second)); d != auditrate.Drop {
		t.Errorf("fourth event = %v, want drop (rate limit)", d)
	}
	// A different service account has its own budget.
	if d := l.Decide("sa-2", "session", "d", base.Add(4*time.Second)); d != auditrate.Emit {
		t.Errorf("other account = %v, want emit (independent rate budget)", d)
	}
}

func TestRateBudgetRecoversAfterAMinute(t *testing.T) {
	l := auditrate.New(2)
	l.Decide("sa-1", "session", "a", base)
	l.Decide("sa-1", "session", "b", base)
	if d := l.Decide("sa-1", "session", "c", base.Add(30*time.Second)); d != auditrate.Drop {
		t.Fatalf("third event within the minute = %v, want drop", d)
	}
	// More than a minute after the first two: the budget has freed up.
	if d := l.Decide("sa-1", "session", "c", base.Add(61*time.Second)); d != auditrate.Emit {
		t.Errorf("event after the rate window = %v, want emit", d)
	}
}

func TestSweepClearsStaleState(t *testing.T) {
	l := auditrate.New(60)
	l.Decide("sa-1", "pool", "default", base)
	// Long after the windows, a sweep drops the state; the next call
	// for the same resource is a fresh emit.
	l.Sweep(base.Add(10 * time.Minute))
	if d := l.Decide("sa-1", "pool", "default", base.Add(10*time.Minute)); d != auditrate.Emit {
		t.Errorf("post-sweep call = %v, want emit", d)
	}
}

// TestCoalescingEmitsSingleEventWithInvocationCount confirms the §25.9
// coalescing window emits exactly one audit event carrying the
// accumulated invocationCount once the window closes. F-25.9.15.
func TestCoalescingEmitsSingleEventWithInvocationCount_spec_25_9_3699(t *testing.T) {
	var emitted []auditrate.Event
	l := auditrate.New(60).WithFlush(func(e auditrate.Event) { emitted = append(emitted, e) })
	call := auditrate.Call{ServiceAccount: "sa-1", ResourceType: "pool", ResourceID: "default", EventType: "diagnostics.pool_diagnosed", OperationID: "op-7"}
	if d := l.Record(call, base); d != auditrate.Emit {
		t.Fatalf("first call = %v, want emit", d)
	}
	for _, dt := range []time.Duration{time.Second, 30 * time.Second, 59 * time.Second} {
		if d := l.Record(call, base.Add(dt)); d != auditrate.Coalesce {
			t.Fatalf("repeat at +%v = %v, want coalesce", dt, d)
		}
	}
	// Nothing is emitted until the window closes.
	if len(emitted) != 0 {
		t.Fatalf("emitted %d events before the window closed, want 0", len(emitted))
	}
	l.Sweep(base.Add(coalesceWindowTest))
	if len(emitted) != 1 {
		t.Fatalf("emitted %d events after the window closed, want 1: %+v", len(emitted), emitted)
	}
	got := emitted[0]
	if got.InvocationCount != 4 {
		t.Errorf("invocationCount = %d, want 4 (1 open + 3 coalesced)", got.InvocationCount)
	}
	if got.EventType != "diagnostics.pool_diagnosed" || got.OperationID != "op-7" {
		t.Errorf("event metadata = %+v, want eventType+operationId carried through", got)
	}
	if !got.FirstAt.Equal(base) {
		t.Errorf("FirstAt = %v, want the window-open instant %v", got.FirstAt, base)
	}
}

// TestExpiredWindowFlushesOnNextRecord confirms a closed window emits
// lazily on the next Record for any resource, and the same resource
// reopens a fresh window. F-25.9.15.
func TestExpiredWindowFlushesOnNextRecord_spec_25_9_3699(t *testing.T) {
	var emitted []auditrate.Event
	l := auditrate.New(60).WithFlush(func(e auditrate.Event) { emitted = append(emitted, e) })
	call := auditrate.Call{ServiceAccount: "sa-1", ResourceType: "session", ResourceID: "s-1", EventType: "diagnostics.session_diagnosed"}
	l.Record(call, base)
	// A call past the window: the first window flushes and a new one opens.
	if d := l.Record(call, base.Add(61*time.Second)); d != auditrate.Emit {
		t.Fatalf("post-window call = %v, want emit", d)
	}
	if len(emitted) != 1 || emitted[0].InvocationCount != 1 {
		t.Fatalf("emitted = %+v, want one event with invocationCount 1", emitted)
	}
}

// TestFlushDrainsOpenWindows confirms the shutdown drain emits every open
// window regardless of age. F-25.9.15.
func TestFlushDrainsOpenWindows_spec_25_9_3699(t *testing.T) {
	var emitted []auditrate.Event
	l := auditrate.New(60).WithFlush(func(e auditrate.Event) { emitted = append(emitted, e) })
	l.Record(auditrate.Call{ServiceAccount: "sa-1", ResourceType: "pool", ResourceID: "a"}, base)
	l.Record(auditrate.Call{ServiceAccount: "sa-1", ResourceType: "pool", ResourceID: "b"}, base)
	l.Flush()
	if len(emitted) != 2 {
		t.Fatalf("Flush emitted %d events, want 2 (one per open window)", len(emitted))
	}
}

// coalesceWindowTest mirrors the package's unexported 60s coalescing
// window so the test can advance time past it.
const coalesceWindowTest = 60 * time.Second
