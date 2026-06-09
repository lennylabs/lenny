// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// fakeCooldownResolver returns a fixed §8.3 SEC-013 fail-open weakening
// cooldown for any interceptor name.
type fakeCooldownResolver struct {
	ts   time.Time
	secs int
	ok   bool
}

func (f fakeCooldownResolver) FailOpenCooldown(context.Context, string) (time.Time, int, bool) {
	return f.ts, f.secs, f.ok
}

func newCooldownService(t *testing.T, now time.Time, resolver delegation.InterceptorCooldownResolver) *delegation.Service {
	t.Helper()
	return delegation.NewService(memstore.New(), delegation.Options{
		Clock:               func() time.Time { return now },
		InterceptorCooldown: resolver,
	})
}

// spec: §4.8 line 1034 / §8.3 line 218 (SEC-013) — a delegate_task /
// send_message whose effective interceptorRef names an interceptor inside
// the fail-closed → fail-open weakening cooldown is rejected with
// INTERCEPTOR_WEAKENING_COOLDOWN carrying the interceptor ref and the
// remaining retry-after window. F-4.8.17.
func TestInterceptorFailPolicyCooldown_spec_4_8_1034(t *testing.T) {
	transition := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	now := transition.Add(30 * time.Second)

	t.Run("within_window_rejects", func(t *testing.T) {
		svc := newCooldownService(t, now, fakeCooldownResolver{ts: transition, secs: 60, ok: true})
		err := svc.InterceptorFailPolicyCooldown(context.Background(), "scan")
		var cd *delegation.InterceptorWeakeningCooldownError
		if !errors.As(err, &cd) {
			t.Fatalf("want InterceptorWeakeningCooldownError, got %v", err)
		}
		if cd.InterceptorRef != "scan" || cd.CooldownSeconds != 60 || cd.RetryAfterSeconds != 30 {
			t.Errorf("error = %+v, want ref scan cooldown 60 retryAfter 30", cd)
		}
	})

	t.Run("empty_ref_passes", func(t *testing.T) {
		svc := newCooldownService(t, now, fakeCooldownResolver{ts: transition, secs: 60, ok: true})
		if err := svc.InterceptorFailPolicyCooldown(context.Background(), ""); err != nil {
			t.Fatalf("empty ref should pass, got %v", err)
		}
	})

	t.Run("nil_resolver_passes", func(t *testing.T) {
		svc := newCooldownService(t, now, nil)
		if err := svc.InterceptorFailPolicyCooldown(context.Background(), "scan"); err != nil {
			t.Fatalf("nil resolver should pass, got %v", err)
		}
	})

	t.Run("not_weakened_passes", func(t *testing.T) {
		svc := newCooldownService(t, now, fakeCooldownResolver{ok: false})
		if err := svc.InterceptorFailPolicyCooldown(context.Background(), "scan"); err != nil {
			t.Fatalf("clean interceptor should pass, got %v", err)
		}
	})

	t.Run("expired_window_passes", func(t *testing.T) {
		expired := transition.Add(90 * time.Second)
		svc := newCooldownService(t, expired, fakeCooldownResolver{ts: transition, secs: 60, ok: true})
		if err := svc.InterceptorFailPolicyCooldown(context.Background(), "scan"); err != nil {
			t.Fatalf("expired window should pass, got %v", err)
		}
	})

	t.Run("clock_skew_uses_full_window", func(t *testing.T) {
		// Transition timestamp in the future relative to "now": treat as
		// freshly armed so the full window still applies.
		future := transition.Add(10 * time.Second)
		svc := newCooldownService(t, transition, fakeCooldownResolver{ts: future, secs: 60, ok: true})
		err := svc.InterceptorFailPolicyCooldown(context.Background(), "scan")
		var cd *delegation.InterceptorWeakeningCooldownError
		if !errors.As(err, &cd) || cd.RetryAfterSeconds != 60 {
			t.Fatalf("clock skew error = %v, want full 60s window", err)
		}
	})
}
