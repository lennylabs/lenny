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
