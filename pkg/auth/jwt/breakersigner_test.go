// SPDX-License-Identifier: MIT

package jwt_test

import (
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// stubSigner is a counting fake that returns configurable errors.
type stubSigner struct {
	signs  int
	errOn  func(n int) error
	output string
}

func (s *stubSigner) Sign(c jwt.Claims) (string, error) {
	s.signs++
	if s.errOn != nil {
		if err := s.errOn(s.signs); err != nil {
			return "", err
		}
	}
	if s.output == "" {
		return "tok", nil
	}
	return s.output, nil
}

func (s *stubSigner) KeyID() string                       { return "stub-kid" }
func (s *stubSigner) Verify(string) (jwt.Claims, error)   { return jwt.Claims{}, nil }

// stubObserver records breaker callbacks.
type stubObserver struct {
	fail     int
	reject   int
	opened   int
	closed   int
}

func (o *stubObserver) OnSigningFailure()  { o.fail++ }
func (o *stubObserver) OnRejected()        { o.reject++ }
func (o *stubObserver) OnCircuitOpen()     { o.opened++ }
func (o *stubObserver) OnCircuitClosed()   { o.closed++ }

// spec: §10.2 line 225 — > 3 consecutive failures inside the window
// trip the breaker open. F-10.2.6.
func TestBreakerSignerTripsAfterMoreThanThresholdFailures(t *testing.T) {
	inner := &stubSigner{errOn: func(int) error { return errors.New("kms boom") }}
	obs := &stubObserver{}
	now := time.Unix(1_000_000, 0)
	b := &jwt.BreakerSigner{
		Inner:     inner,
		Observer:  obs,
		Threshold: jwt.SigningBreakerThreshold,
		Window:    jwt.SigningBreakerWindow,
		Cooldown:  jwt.SigningBreakerCooldown,
		Now:       func() time.Time { return now },
	}
	// Threshold = 3 — the breaker trips on the 4th consecutive
	// failure (count > 3).
	for i := 0; i < 4; i++ {
		_, err := b.Sign(jwt.Claims{})
		if err == nil {
			t.Fatalf("Sign #%d: expected error", i+1)
		}
		if errors.Is(err, jwt.ErrSigningUnavailable) {
			t.Fatalf("Sign #%d: breaker tripped too early (%d failures)", i+1, i+1)
		}
	}
	if obs.opened != 1 {
		t.Errorf("OnCircuitOpen calls = %d, want 1", obs.opened)
	}
	// Next Sign should short-circuit to ErrSigningUnavailable without
	// calling the inner signer.
	innerCalls := inner.signs
	_, err := b.Sign(jwt.Claims{})
	if !errors.Is(err, jwt.ErrSigningUnavailable) {
		t.Fatalf("post-trip Sign: got %v, want ErrSigningUnavailable", err)
	}
	if inner.signs != innerCalls {
		t.Errorf("inner.signs = %d, want unchanged at %d", inner.signs, innerCalls)
	}
	if obs.reject != 1 {
		t.Errorf("OnRejected = %d, want 1", obs.reject)
	}
	if b.State() != "open" {
		t.Errorf("State = %q, want open", b.State())
	}
}

// spec: §10.2 line 225 — cooldown elapses; one half-open probe
// admitted; success closes the breaker. F-10.2.6.
func TestBreakerSignerHalfOpenProbeSuccessCloses(t *testing.T) {
	calls := 0
	inner := &stubSigner{errOn: func(n int) error {
		calls = n
		if n <= 4 {
			return errors.New("kms boom")
		}
		return nil
	}}
	obs := &stubObserver{}
	now := time.Unix(2_000_000, 0)
	cur := now
	b := &jwt.BreakerSigner{
		Inner:    inner,
		Observer: obs,
		Cooldown: 5 * time.Second,
		Now:      func() time.Time { return cur },
	}
	// Trip the breaker.
	for i := 0; i < 4; i++ {
		_, _ = b.Sign(jwt.Claims{})
	}
	if b.State() != "open" {
		t.Fatalf("setup: breaker not open after 4 failures")
	}
	// Cooldown elapses.
	cur = now.Add(6 * time.Second)
	out, err := b.Sign(jwt.Claims{})
	if err != nil {
		t.Fatalf("half-open probe: %v", err)
	}
	if out != "tok" {
		t.Fatalf("probe output = %q, want tok", out)
	}
	if b.State() != "closed" {
		t.Errorf("State = %q, want closed", b.State())
	}
	if obs.closed != 1 {
		t.Errorf("OnCircuitClosed = %d, want 1", obs.closed)
	}
	_ = calls
}

// Failures older than the window slide out so the breaker doesn't
// over-trip on slow drips. F-10.2.6.
func TestBreakerSignerFailuresAgeOutWindow(t *testing.T) {
	cur := time.Unix(3_000_000, 0)
	inner := &stubSigner{errOn: func(int) error { return errors.New("kms boom") }}
	b := &jwt.BreakerSigner{
		Inner:     inner,
		Threshold: 3,
		Window:    1 * time.Second,
		Cooldown:  10 * time.Second,
		Now:       func() time.Time { return cur },
	}
	// 3 failures at t=0; the breaker trips on the 4th. Spread the
	// failures across enough time that earlier ones age out.
	for i := 0; i < 3; i++ {
		_, _ = b.Sign(jwt.Claims{})
	}
	// Sleep past window: the next failure starts a fresh count.
	cur = cur.Add(2 * time.Second)
	_, err := b.Sign(jwt.Claims{})
	if err == nil {
		t.Fatal("expected inner error")
	}
	if errors.Is(err, jwt.ErrSigningUnavailable) {
		t.Fatal("breaker tripped despite failures aging out")
	}
	if b.State() != "closed" {
		t.Errorf("State = %q, want closed", b.State())
	}
}

// A successful Sign zeroes the failure counter so a single transient
// glitch can't combine with later failures to trip the breaker.
func TestBreakerSignerSuccessResetsCount(t *testing.T) {
	calls := 0
	inner := &stubSigner{errOn: func(n int) error {
		calls = n
		switch n {
		case 1, 2, 3, 5, 6, 7:
			return nil
		default:
			return errors.New("kms boom")
		}
	}}
	cur := time.Unix(4_000_000, 0)
	b := &jwt.BreakerSigner{
		Inner: inner,
		Now:   func() time.Time { return cur },
	}
	// 3 successes, 1 failure (zero), 3 more successes — never trip.
	for i := 0; i < 7; i++ {
		_, _ = b.Sign(jwt.Claims{})
	}
	if b.State() != "closed" {
		t.Errorf("State = %q, want closed; calls=%d", b.State(), calls)
	}
}

// Spec sentinel: ErrSigningUnavailable is exported. F-10.2.6.
func TestErrSigningUnavailableIsExported(t *testing.T) {
	if jwt.ErrSigningUnavailable == nil {
		t.Fatal("ErrSigningUnavailable must be a non-nil sentinel")
	}
}

// Verify delegates to inner. F-10.2.6.
func TestBreakerSignerVerifyDelegatesToInner(t *testing.T) {
	inner := &stubSigner{}
	b := &jwt.BreakerSigner{Inner: inner}
	if _, err := b.Verify("anything"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
