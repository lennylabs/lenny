// SPDX-License-Identifier: MIT

package jwt

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/kms"
)

// ErrSigningUnavailable is the sentinel a breaker-open refusal wraps.
// Callers (the Token Service §13.3 handler, the gateway session-mint
// path) map every *SigningError to 503 KMS_SIGNING_UNAVAILABLE with
// `retryable: true` per §10.2; errors.Is against this sentinel holds
// only for a refusal the breaker made without calling the KMS.
var ErrSigningUnavailable = errors.New("jwt: signing unavailable (circuit open)")

// Sign outcome values BreakerSigner reports through
// BreakerObserver.OnOutcome, one per Sign call. They are the `outcome`
// label values of the §16.1 JWT sign requests counter.
//
// spec: §16.1 (JWT sign requests).
const (
	SignOutcomeOK          = "ok"
	SignOutcomeThrottled   = "throttled"
	SignOutcomeCircuitOpen = "circuit_open"
	SignOutcomeError       = "error"
)

// SigningError is the error every failed BreakerSigner.Sign returns.
// RetryAfter is the §10.2 `Retry-After` duration: the remaining
// breaker cooldown when the breaker is open, and one second otherwise.
// Callers detect a signing error with errors.As and set the response
// header from RetryAfterSeconds.
//
// spec: §10.2 (KMS signing failure mode).
type SigningError struct {
	RetryAfter time.Duration
	Err        error
}

func (e *SigningError) Error() string {
	return fmt.Sprintf("jwt: signing failed (retry after %s): %v", e.RetryAfter, e.Err)
}

// Unwrap exposes the inner error so errors.Is(err, kms.ErrThrottled)
// and errors.Is(err, ErrSigningUnavailable) keep holding.
func (e *SigningError) Unwrap() error { return e.Err }

// RetryAfterSeconds returns RetryAfter rounded up to whole seconds with
// a floor of 1, the §10.2 `Retry-After` header value.
//
// spec: §10.2 (KMS signing failure mode).
func (e *SigningError) RetryAfterSeconds() int {
	secs := int((e.RetryAfter + time.Second - 1) / time.Second)
	if secs < 1 {
		return 1
	}
	return secs
}

// SigningRetryAfterSeconds returns the §10.2 `Retry-After` value, in
// whole seconds, for an error a mint path's Sign call returned: the
// *SigningError's RetryAfterSeconds when err carries one, and 1 (the
// value for a signing error while the breaker is not open) otherwise.
// Every KMS_SIGNING_UNAVAILABLE writer sets the header from it.
//
// spec: §10.2 (KMS signing failure mode).
func SigningRetryAfterSeconds(err error) int {
	var serr *SigningError
	if errors.As(err, &serr) {
		return serr.RetryAfterSeconds()
	}
	return int(signingRetryAfter / time.Second)
}

// Signer component values, the `component` label of the §16.1 JWT sign
// requests counter. Each signer process stamps its own value.
//
// spec: §16.1 (JWT sign requests).
const (
	SignComponentGateway      = "gateway"
	SignComponentTokenService = "token-service"
)

// BreakerObserver receives signing notifications. It is the seam the
// gateway uses to increment `lenny_gateway_kms_signing_errors_total`,
// count sign requests by outcome, and flip the §16.5
// KMSSigningUnavailable alert. Implementations must be non-blocking;
// BreakerSigner holds no internal lock when it calls into them.
type BreakerObserver interface {
	// OnSigningFailure fires for every signing attempt the breaker
	// counted as a failure: an inner error other than provider
	// throttling, returned while the caller's context was live
	// (including expiry of the per-call signing timeout). The breaker
	// state machine has already counted the failure before this fires.
	OnSigningFailure()
	// OnCircuitOpen fires once when the breaker transitions from
	// closed/half-open to open. Use it as the level-trigger for the
	// state gauge.
	OnCircuitOpen()
	// OnCircuitClosed fires once when the breaker transitions from
	// half-open to closed. Used to clear the state gauge.
	OnCircuitClosed()
	// OnRejected fires for every Sign call that BreakerSigner refused
	// to forward because the breaker was open or a half-open probe was
	// in flight. Distinct from OnSigningFailure so dashboards can
	// compare "we tried and the KMS failed" against "we short-circuited".
	OnRejected()
	// OnOutcome fires exactly once per Sign call with one of
	// SignOutcomeOK, SignOutcomeThrottled, SignOutcomeCircuitOpen, or
	// SignOutcomeError.
	OnOutcome(outcome string)
}

// SigningBreakerThreshold is the §10.2 consecutive-failure
// threshold ("> 3 consecutive signing failures"). A failure count
// strictly greater than 3 trips the breaker, matching the spec's
// inequality.
const SigningBreakerThreshold = 3

// SigningBreakerWindow is the §10.2 rolling window
// ("within 30s"): a failure older than the window slides out of the
// running count.
const SigningBreakerWindow = 30 * time.Second

// SigningBreakerCooldown is the open-state duration before a half-open
// probe is admitted. Matched to the window so the breaker has time to
// drain a transient KMS outage.
const SigningBreakerCooldown = 30 * time.Second

// SigningCallTimeout bounds one inner Sign call. A call that outlives
// it while the caller's context is live counts as a signing failure,
// so a hung KMS trips the breaker rather than holding mints open.
//
// spec: §10.2 (KMS signing failure mode).
const SigningCallTimeout = 5 * time.Second

// signingRetryAfter is the §10.2 `Retry-After` for a signing error
// while the breaker is not open.
const signingRetryAfter = time.Second

// BreakerSigner wraps a Signer with the §10.2 in-memory
// JWTSigner circuit breaker. More than SigningBreakerThreshold
// consecutive counted Sign failures inside SigningBreakerWindow trip
// the breaker open; subsequent Sign calls are refused without invoking
// the inner signer until the cooldown elapses and a half-open probe
// runs. A successful probe closes the breaker; a counted probe failure
// reopens it and resets the cooldown. Provider throttling and errors
// returned after the caller's context ended are not counted: they
// neither trip nor clear the failure run, and a half-open probe that
// ends that way releases the probe slot so the next call probes.
//
// BreakerSigner is safe for concurrent use.
//
// spec: §10.2 (KMS signing failure mode). F-10.2.6.
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
	// CallTimeout overrides SigningCallTimeout when > 0.
	CallTimeout time.Duration
	// Now substitutes the wall clock. Defaults to time.Now.
	Now func() time.Time

	mu            sync.Mutex
	state         breakerState
	failures      []time.Time // consecutive counted failures within the rolling window
	openedAt      time.Time
	probeInFlight bool
}

type breakerState uint8

const (
	breakerClosed breakerState = iota
	breakerHalfOpen
	breakerOpen
)

// admission is the breaker's decision for one Sign call.
type admission struct {
	allowed bool
	probe   bool          // the call holds the half-open probe slot
	retry   time.Duration // Retry-After for a refusal
}

// Sign mints a JWT through the wrapped signer, applying the §10.2
// circuit-breaker admission gate. A closed breaker forwards every
// call; an open breaker refuses until the cooldown elapses; a
// half-open breaker admits exactly one probe at a time. Every error is
// a *SigningError.
func (s *BreakerSigner) Sign(ctx context.Context, c Claims) (string, error) {
	adm := s.admit()
	if !adm.allowed {
		s.notify(func(o BreakerObserver) { o.OnRejected(); o.OnOutcome(SignOutcomeCircuitOpen) })
		return "", &SigningError{RetryAfter: adm.retry, Err: ErrSigningUnavailable}
	}
	callCtx, cancel := context.WithTimeout(ctx, s.callTimeout())
	out, err := s.Inner.Sign(callCtx, c)
	cancel()
	switch {
	case err == nil:
		s.recordSuccess(adm.probe)
		s.notify(func(o BreakerObserver) { o.OnOutcome(SignOutcomeOK) })
		return out, nil
	case errors.Is(err, kms.ErrThrottled):
		retry := s.releaseUncounted(adm.probe)
		s.notify(func(o BreakerObserver) { o.OnOutcome(SignOutcomeThrottled) })
		return "", &SigningError{RetryAfter: retry, Err: err}
	case ctx.Err() != nil:
		// The caller's context ended, so the error says nothing about
		// KMS health. Test the caller's context rather than the error
		// type: expiry of the per-call timeout is also DeadlineExceeded.
		retry := s.releaseUncounted(adm.probe)
		s.notify(func(o BreakerObserver) { o.OnOutcome(SignOutcomeError) })
		return "", &SigningError{RetryAfter: retry, Err: err}
	default:
		retry := s.recordFailure(adm.probe)
		s.notify(func(o BreakerObserver) { o.OnSigningFailure(); o.OnOutcome(SignOutcomeError) })
		return "", &SigningError{RetryAfter: retry, Err: err}
	}
}

// notify calls fn with the observer when one is configured.
func (s *BreakerSigner) notify(fn func(BreakerObserver)) {
	if s.Observer != nil {
		fn(s.Observer)
	}
}

// KeyID forwards the inner signer's key id, so the kid it reports is
// the one stamped on the JOSE header of every minted token.
func (s *BreakerSigner) KeyID() string {
	if s.Inner == nil {
		return ""
	}
	return s.Inner.KeyID()
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

func (s *BreakerSigner) admit() admission {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.evictExpired(now)
	switch s.state {
	case breakerClosed:
		return admission{allowed: true}
	case breakerOpen:
		if now.Sub(s.openedAt) < s.cooldown() {
			return admission{retry: s.retryAfterLocked(now)}
		}
		s.state = breakerHalfOpen
		s.probeInFlight = true
		return admission{allowed: true, probe: true}
	default: // breakerHalfOpen
		if s.probeInFlight {
			return admission{retry: signingRetryAfter}
		}
		s.probeInFlight = true
		return admission{allowed: true, probe: true}
	}
}

// recordSuccess ends a call that succeeded. A success always clears
// the closed-state failure run, so a straggler admitted while the
// breaker was closed still resets it. Only the call holding the
// half-open probe slot closes a half-open breaker and frees the slot: a
// half-open breaker transitions on the probe's result alone, and a
// non-probe call that completes while the breaker is half-open leaves
// the state and the in-flight probe untouched.
//
// spec: §10.2 (KMS signing failure mode).
func (s *BreakerSigner) recordSuccess(probe bool) {
	s.mu.Lock()
	s.failures = nil
	closedNow := false
	if probe && s.state == breakerHalfOpen {
		closedNow = true
		s.state = breakerClosed
		s.probeInFlight = false
	}
	s.mu.Unlock()
	if closedNow {
		s.notify(func(o BreakerObserver) { o.OnCircuitClosed() })
	}
}

// releaseUncounted ends a call whose result the breaker does not
// count. The failure run is left as it is; a half-open probe gives up
// its slot so the next call is admitted as the probe. It returns the
// Retry-After for the call's error.
func (s *BreakerSigner) releaseUncounted(probe bool) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if probe && s.state == breakerHalfOpen {
		s.probeInFlight = false
	}
	return s.retryAfterLocked(s.now())
}

// recordFailure counts one signing failure, trips the breaker when the
// §10.2 predicate holds, and returns the Retry-After for the call's
// error. In closed state every counted failure extends the run. A
// half-open breaker reopens only on its probe's failure; a non-probe
// call admitted before the trip that fails while the breaker is
// half-open or open changes neither the state nor the probe slot.
//
// spec: §10.2 (KMS signing failure mode).
func (s *BreakerSigner) recordFailure(probe bool) time.Duration {
	s.mu.Lock()
	now := s.now()
	s.evictExpired(now)
	openedNow := false
	switch s.state {
	case breakerHalfOpen:
		if probe {
			s.trip(now)
			openedNow = true
		}
	case breakerClosed:
		s.failures = append(s.failures, now)
		if len(s.failures) > s.threshold() {
			s.trip(now)
			openedNow = true
		}
	}
	retry := s.retryAfterLocked(now)
	s.mu.Unlock()
	if openedNow {
		s.notify(func(o BreakerObserver) { o.OnCircuitOpen() })
	}
	return retry
}

// retryAfterLocked is the §10.2 Retry-After: the remaining cooldown
// while the breaker is open, one second otherwise. Caller holds s.mu.
func (s *BreakerSigner) retryAfterLocked(now time.Time) time.Duration {
	if s.state != breakerOpen {
		return signingRetryAfter
	}
	if remaining := s.cooldown() - now.Sub(s.openedAt); remaining > 0 {
		return remaining
	}
	return signingRetryAfter
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

func (s *BreakerSigner) callTimeout() time.Duration {
	if s.CallTimeout > 0 {
		return s.CallTimeout
	}
	return SigningCallTimeout
}

func (s *BreakerSigner) cooldown() time.Duration {
	if s.Cooldown > 0 {
		return s.Cooldown
	}
	return SigningBreakerCooldown
}

// Compile-time check: BreakerSigner satisfies Signer.
var _ Signer = (*BreakerSigner)(nil)
