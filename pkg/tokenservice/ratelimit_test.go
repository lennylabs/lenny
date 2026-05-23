// SPDX-License-Identifier: MIT

package tokenservice

import (
	"testing"
	"time"
)

// spec: §13.3 line 607 — per-(tenant, sub) and global per-tenant
// rate-limit invariants on /v1/oauth/token.
func TestRateLimiter_CallerPerSecondAllowsUpToLimit(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rl := NewRateLimiter(RateLimitOptions{CallerPerSecond: 3}, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		dec := rl.Allow(now, "acme", "alice")
		if !dec.Allowed {
			t.Fatalf("call %d denied; want allowed", i+1)
		}
	}
	dec := rl.Allow(now, "acme", "alice")
	if dec.Allowed {
		t.Errorf("4th call allowed; want denied")
	}
	if dec.LimitTier != LimitTierCallerPerSecond {
		t.Errorf("LimitTier = %q, want %q", dec.LimitTier, LimitTierCallerPerSecond)
	}
	if dec.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %s, want > 0", dec.RetryAfter)
	}
}

// spec: §13.3 line 607 — different subs share neither caller bucket
// nor sample state.
func TestRateLimiter_CallerPerSecondIsolatedPerSub(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rl := NewRateLimiter(RateLimitOptions{CallerPerSecond: 1}, func() time.Time { return now })
	if !rl.Allow(now, "acme", "alice").Allowed {
		t.Fatal("alice first request denied")
	}
	if rl.Allow(now, "acme", "alice").Allowed {
		t.Fatal("alice second request allowed")
	}
	if !rl.Allow(now, "acme", "bob").Allowed {
		t.Errorf("bob first request denied; want isolated bucket")
	}
}

// spec: §13.3 line 607 — caller-per-minute defends against burst
// patterns that would slip past the per-second budget.
func TestRateLimiter_CallerPerMinute(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	rl := NewRateLimiter(RateLimitOptions{CallerPerSecond: 100, CallerPerMinute: 5},
		func() time.Time { return now })
	for i := 0; i < 5; i++ {
		now = base.Add(time.Duration(i) * time.Second)
		if !rl.Allow(now, "acme", "alice").Allowed {
			t.Fatalf("call %d denied", i)
		}
	}
	now = base.Add(6 * time.Second)
	dec := rl.Allow(now, "acme", "alice")
	if dec.Allowed {
		t.Errorf("6th call inside minute window allowed")
	}
	if dec.LimitTier != LimitTierCallerPerMinute {
		t.Errorf("LimitTier = %q, want %q", dec.LimitTier, LimitTierCallerPerMinute)
	}
}

// spec: §13.3 line 607 — tenant-per-second is independent of the
// caller bucket.
func TestRateLimiter_TenantPerSecond(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rl := NewRateLimiter(RateLimitOptions{TenantPerSecond: 2}, func() time.Time { return now })
	if !rl.Allow(now, "acme", "alice").Allowed {
		t.Fatal("alice denied")
	}
	if !rl.Allow(now, "acme", "bob").Allowed {
		t.Fatal("bob denied")
	}
	dec := rl.Allow(now, "acme", "carol")
	if dec.Allowed {
		t.Errorf("carol allowed past tenant budget")
	}
	if dec.LimitTier != LimitTierTenantPerSecond {
		t.Errorf("LimitTier = %q, want %q", dec.LimitTier, LimitTierTenantPerSecond)
	}
}

// spec: §13.3 line 609 — the first rejection per (tenant, sub, tier)
// inside a rolling window emits a sampled audit row; subsequent
// rejections in the window do not.
func TestRateLimiter_SamplingFirstRejectionThenSuppressed(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	rl := NewRateLimiter(RateLimitOptions{
		CallerPerSecond: 1,
		SampleWindow:    5 * time.Second,
	}, func() time.Time { return now })
	if !rl.Allow(now, "acme", "alice").Allowed {
		t.Fatal("first call denied")
	}
	dec := rl.Allow(now, "acme", "alice")
	if dec.Allowed {
		t.Fatal("second call allowed")
	}
	if !dec.AuditSampled {
		t.Errorf("first rejection should be audit-sampled")
	}
	// Same window, second rejection: not sampled.
	now = base.Add(2 * time.Second)
	rl.Allow(now, "acme", "alice") // refill the per-second bucket
	dec2 := rl.Allow(now, "acme", "alice")
	if dec2.Allowed {
		t.Fatal("third call allowed")
	}
	if dec2.AuditSampled {
		t.Errorf("second rejection inside window should NOT be audit-sampled")
	}
	// Past the window: sampled again.
	now = base.Add(10 * time.Second)
	rl.Allow(now, "acme", "alice")
	dec3 := rl.Allow(now, "acme", "alice")
	if dec3.Allowed {
		t.Fatal("post-window call allowed")
	}
	if !dec3.AuditSampled {
		t.Errorf("post-window rejection should re-sample")
	}
}

// spec: §13.3 line 607 — buckets reset at the second boundary so a
// caller that paces at the limit is never denied.
func TestRateLimiter_PerSecondBucketRollover(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	rl := NewRateLimiter(RateLimitOptions{CallerPerSecond: 1}, func() time.Time { return now })
	if !rl.Allow(now, "acme", "alice").Allowed {
		t.Fatal("call 1 denied")
	}
	if rl.Allow(now, "acme", "alice").Allowed {
		t.Fatal("call 2 inside same second allowed")
	}
	now = base.Add(time.Second)
	if !rl.Allow(now, "acme", "alice").Allowed {
		t.Errorf("call after second rollover denied")
	}
}

// spec: §13.3 — RateLimitOptions.IsZero() reports whether the limiter
// should be wired at all.
func TestRateLimitOptionsIsZero(t *testing.T) {
	if !(RateLimitOptions{}).IsZero() {
		t.Errorf("zero-value RateLimitOptions reports not-zero")
	}
	if (RateLimitOptions{CallerPerSecond: 1}).IsZero() {
		t.Errorf("RateLimitOptions with CallerPerSecond>0 reports zero")
	}
	if (RateLimitOptions{TenantPerSecond: 1}).IsZero() {
		t.Errorf("RateLimitOptions with TenantPerSecond>0 reports zero")
	}
}
