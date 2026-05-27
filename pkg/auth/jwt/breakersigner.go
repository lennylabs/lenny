// SPDX-License-Identifier: MIT

package jwt

import (
	"errors"
	"sync"
	"time"
)

// ErrSigningUnavailable is the sentinel BreakerSigner returns when its
// circuit is open. Callers (the Token Service §13.3 handler, the
// gateway session-mint path) map it to 503 KMS_SIGNING_UNAVAILABLE
// with `retryable: true` per §10.2 line 225.
var ErrSigningUnavailable = errors.New("jwt: signing unavailable (circuit open)")

// BreakerObserver receives signing-error notifications. It is the seam
// the gateway uses to increment `lenny_gateway_kms_signing_errors_total`
// and flip the §16.5 KMSSigningUnavailable alert. Implementations must
// be non-blocking; BreakerSigner holds no internal lock when it calls
// into them.
type BreakerObserver interface {
	// OnSigningFailure fires for every signing attempt that returned a
	// non-nil error from the wrapped Signer. The breaker state machine
	// has already counted the failure before this fires.
	OnSigningFailure()
	// OnCircuitOpen fires once when the breaker transitions from
	// closed/half-open to open. Use it as the level-trigger for the
	// state gauge.
	OnCircuitOpen()
	// OnCircuitClosed fires once when the breaker transitions from
	// half-open to closed. Used to clear the state gauge.
	OnCircuitClosed()
	// OnRejected fires for every Sign call that BreakerSigner refused
	// to forward because the breaker was open. Distinct from
	// OnSigningFailure so dashboards can compare "we tried and the KMS
	// failed" against "we short-circuited".
	OnRejected()
}

// SigningBreakerThreshold is the §10.2 line 225 consecutive-failure
// threshold ("> 3 consecutive signing failures"). A failure count
// strictly greater than 3 trips the breaker, matching the spec's
// inequality.
const SigningBreakerThreshold = 3

// SigningBreakerWindow is the §10.2 line 225 rolling window
// ("within 30s"): a failure older than the window slides out of the
// running count.
const SigningBreakerWindow = 30 * time.Second

// SigningBreakerCooldown is the open-state duration before a half-open
// probe is admitted. Matched to the window so the breaker has time to
// drain a transient KMS outage.
const SigningBreakerCooldown = 30 * time.Second

// BreakerSigner wraps a Signer with the §10.2 line 225 in-memory
// JWTSigner circuit breaker. More than SigningBreakerThreshold
// consecutive Sign failures inside SigningBreakerWindow trip the
// breaker open; subsequent Sign calls return ErrSigningUnavailable
// without invoking the inner signer until the cooldown elapses and a
// half-open probe runs. A successful probe closes the breaker; a
// failed probe reopens it and resets the cooldown.
//
// BreakerSigner is safe for concurrent use.
//
// spec: §10.2 line 225. F-10.2.6.
type BreakerSigner struct {
	Inner    Signer
	Observer BreakerObserver

	// Threshold overrides SigningBreakerThreshold when > 0. Tests use
	// this to assert the trip boundary without running through 4
	// failures every assertion.
	Threshold int
	// Window overrides SigningBreakerWindow when > 0.
	Window time.Duration
	// Cooldown overrides SigningBreakerCooldown when > 0.
	Cooldown time.Duration
	// Now substitutes the wall clock. Defaults to time.Now.
	Now func() time.Time

	mu            sync.Mutex
	state         breakerState
	failures      []time.Time // failure timestamps within the rolling window
	openedAt      time.Time
	probeInFlight bool
}

type breakerState uint8

const (
	breakerClosed breakerState = iota
	breakerHalfOpen
	breakerOpen
)

// Sign mints a JWT through the wrapped signer, applying the §10.2
// circuit-breaker admission gate. A closed breaker forwards every
// call; an open breaker short-circuits to ErrSigningUnavailable until
// the cooldown elapses; a half-open breaker admits exactly one probe.
func (s *BreakerSigner) Sign(c Claims) (string, error) {
	if !s.allow() {
		if s.Observer != nil {
			s.Observer.OnRejected()
		}
		return "", ErrSigningUnavailable
	}
	out, err := s.Inner.Sign(c)
	if err != nil {
		s.recordFailure()
		if s.Observer != nil {
			s.Observer.OnSigningFailure()
		}
		return "", err
	}
	s.recordSuccess()
	return out, nil
}

// KeyID forwards the inner signer's key id so the JOSE header on
// minted tokens matches the underlying KMS-wrapped key.
func (s *BreakerSigner) KeyID() string {
	if s.Inner == nil {
		return ""
	}
	return s.Inner.KeyID()
}

// Verify forwards to the inner signer when it also satisfies Verifier
// (both HMACSigner and KMSSigner do). The verify path does not consult
// the breaker — verification is local memory and never reaches KMS.
// Returns an error when the inner does not implement Verifier so a
// caller cannot silently bypass token validation.
func (s *BreakerSigner) Verify(token string) (Claims, error) {
	v, ok := s.Inner.(Verifier)
	if !ok {
		return Claims{}, errors.New("jwt: BreakerSigner inner does not implement Verifier")
	}
	return v.Verify(token)
}

// State returns the breaker's current state as a stable string so
// metric-collector wiring can read it without inspecting internals.
// Values: "closed", "half_open", "open".
func (s *BreakerSigner) State() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case breakerHalfOpen:
		return "half_open"
	case breakerOpen:
		return "open"
	default:
		return "closed"
	}
}

func (s *BreakerSigner) allow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.evictExpired(now)
	switch s.state {
	case breakerClosed:
		return true
	case breakerOpen:
		if now.Sub(s.openedAt) < s.cooldown() {
			return false
		}
		s.state = breakerHalfOpen
		s.probeInFlight = true
		return true
	case breakerHalfOpen:
		if s.probeInFlight {
			return false
		}
		s.probeInFlight = true
		return true
	default:
		return false
	}
}

func (s *BreakerSigner) recordSuccess() {
	s.mu.Lock()
	s.failures = nil
	closedNow := false
	if s.state == breakerHalfOpen {
		closedNow = true
		s.state = breakerClosed
	}
	s.probeInFlight = false
	s.mu.Unlock()
	if closedNow && s.Observer != nil {
		s.Observer.OnCircuitClosed()
	}
}

func (s *BreakerSigner) recordFailure() {
	s.mu.Lock()
	now := s.now()
	s.evictExpired(now)
	openedNow := false
	switch s.state {
	case breakerHalfOpen:
		s.trip(now)
		openedNow = true
	default:
		s.failures = append(s.failures, now)
		if len(s.failures) > s.threshold() {
			s.trip(now)
			openedNow = true
		}
	}
	s.mu.Unlock()
	if openedNow && s.Observer != nil {
		s.Observer.OnCircuitOpen()
	}
}

// trip is called with s.mu held.
func (s *BreakerSigner) trip(now time.Time) {
	s.state = breakerOpen
	s.openedAt = now
	s.failures = nil
	s.probeInFlight = false
}

// evictExpired drops failure timestamps older than the rolling window
// so that failures separated by long quiet periods do not accumulate.
// Caller holds s.mu.
func (s *BreakerSigner) evictExpired(now time.Time) {
	if len(s.failures) == 0 {
		return
	}
	cutoff := now.Add(-s.window())
	i := 0
	for ; i < len(s.failures); i++ {
		if s.failures[i].After(cutoff) {
			break
		}
	}
	if i > 0 {
		s.failures = append(s.failures[:0], s.failures[i:]...)
	}
}

func (s *BreakerSigner) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *BreakerSigner) threshold() int {
	if s.Threshold > 0 {
		return s.Threshold
	}
	return SigningBreakerThreshold
}

func (s *BreakerSigner) window() time.Duration {
	if s.Window > 0 {
		return s.Window
	}
	return SigningBreakerWindow
}

func (s *BreakerSigner) cooldown() time.Duration {
	if s.Cooldown > 0 {
		return s.Cooldown
	}
	return SigningBreakerCooldown
}

// Compile-time check: BreakerSigner satisfies Signer.
var _ Signer = (*BreakerSigner)(nil)
