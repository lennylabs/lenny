// SPDX-License-Identifier: MIT

package jwt_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
)

// stubSigner is a counting fake that returns configurable errors.
type stubSigner struct {
	signs  int
	errOn  func(n int) error
	output string
}

func (s *stubSigner) Sign(_ context.Context, c jwt.Claims) (string, error) {
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

func (s *stubSigner) KeyID() string                     { return "stub-kid" }
func (s *stubSigner) Verify(string) (jwt.Claims, error) { return jwt.Claims{}, nil }

// stubObserver records breaker callbacks.
type stubObserver struct {
	mu       sync.Mutex
	fail     int
	reject   int
	opened   int
	closed   int
	outcomes []string
}

func (o *stubObserver) OnSigningFailure() { o.mu.Lock(); o.fail++; o.mu.Unlock() }
func (o *stubObserver) OnRejected()       { o.mu.Lock(); o.reject++; o.mu.Unlock() }
func (o *stubObserver) OnCircuitOpen()    { o.mu.Lock(); o.opened++; o.mu.Unlock() }
func (o *stubObserver) OnCircuitClosed()  { o.mu.Lock(); o.closed++; o.mu.Unlock() }
func (o *stubObserver) OnOutcome(outcome string) {
	o.mu.Lock()
	o.outcomes = append(o.outcomes, outcome)
	o.mu.Unlock()
}

// spec: §10.2 — > 3 consecutive failures inside the window
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
		_, err := b.Sign(context.Background(), jwt.Claims{})
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
	_, err := b.Sign(context.Background(), jwt.Claims{})
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

// spec: §10.2 — cooldown elapses; one half-open probe
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
		_, _ = b.Sign(context.Background(), jwt.Claims{})
	}
	if b.State() != "open" {
		t.Fatalf("setup: breaker not open after 4 failures")
	}
	// Cooldown elapses.
	cur = now.Add(6 * time.Second)
	out, err := b.Sign(context.Background(), jwt.Claims{})
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
		_, _ = b.Sign(context.Background(), jwt.Claims{})
	}
	// Sleep past window: the next failure starts a fresh count.
	cur = cur.Add(2 * time.Second)
	_, err := b.Sign(context.Background(), jwt.Claims{})
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
		_, _ = b.Sign(context.Background(), jwt.Claims{})
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

// ---------------------------------------------------------------------
// Throttling backpressure, Retry-After, and the per-call outcome.
// ---------------------------------------------------------------------

// Inner result kinds the scripted signer produces. The kind travels in
// Claims.Subject so one signer serves every call of a scenario.
const (
	kindOK        = "ok"
	kindThrottled = "throttled"
	kindHard      = "hard"
	kindTimeout   = "timeout"   // outlives the breaker's CallTimeout
	kindCancelled = "cancelled" // errors after the caller's ctx ended
)

var errHardKMS = errors.New("kms: internal error")

// scriptedSigner returns the result named by Claims.Subject. A call
// whose Claims.JWTID names a registered gate blocks inside the signer
// until the gate is closed, which holds the call in flight.
type scriptedSigner struct {
	mu      sync.Mutex
	calls   int
	gates   map[string]chan struct{}
	entered chan string
}

func newScriptedSigner() *scriptedSigner {
	return &scriptedSigner{gates: map[string]chan struct{}{}, entered: make(chan string, 64)}
}

func (s *scriptedSigner) hold(id string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := make(chan struct{})
	s.gates[id] = g
	return g
}

func (s *scriptedSigner) Sign(ctx context.Context, c jwt.Claims) (string, error) {
	s.mu.Lock()
	s.calls++
	gate := s.gates[c.JWTID]
	s.mu.Unlock()
	if gate != nil {
		s.entered <- c.JWTID
		<-gate
	}
	switch c.Subject {
	case kindOK:
		return "tok", nil
	case kindThrottled:
		return "", fmt.Errorf("aws kms: %w", kms.ErrThrottled)
	case kindTimeout:
		<-ctx.Done()
		return "", ctx.Err()
	case kindCancelled:
		return "", fmt.Errorf("transport closed: %w", context.Canceled)
	default:
		return "", errHardKMS
	}
}

func (s *scriptedSigner) innerCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *scriptedSigner) KeyID() string                     { return "scripted" }
func (s *scriptedSigner) Verify(string) (jwt.Claims, error) { return jwt.Claims{}, nil }

// fakeClock is a mutex-guarded settable clock.
type fakeClock struct {
	mu  sync.Mutex
	cur time.Time
}

func (c *fakeClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.cur }
func (c *fakeClock) Advance(d time.Duration) { c.mu.Lock(); c.cur = c.cur.Add(d); c.mu.Unlock() }

// breakerModel is the §10.2 oracle the interleaving checker compares
// the breaker against. It states the trip predicate directly: a closed
// breaker opens when more than three consecutive counted failures lie
// inside the 30 s window; throttling and caller-ended errors are not
// counted and do not break the run; a half-open breaker admits one
// probe at a time and reopens only on a counted probe failure.
type breakerModel struct {
	state         string // closed | half_open | open
	run           []time.Time
	openedAt      time.Time
	probeInFlight bool
	opens, closes int
	failures      int
}

const (
	modelWindow   = 30 * time.Second
	modelCooldown = 30 * time.Second
)

type modelAdmission struct {
	allowed bool
	probe   bool
	retry   time.Duration
}

func (m *breakerModel) admit(now time.Time) modelAdmission {
	switch m.state {
	case "closed":
		return modelAdmission{allowed: true}
	case "open":
		if rem := modelCooldown - now.Sub(m.openedAt); rem > 0 {
			return modelAdmission{retry: rem}
		}
		m.state, m.probeInFlight = "half_open", true
		return modelAdmission{allowed: true, probe: true}
	default:
		if m.probeInFlight {
			return modelAdmission{retry: time.Second}
		}
		m.probeInFlight = true
		return modelAdmission{allowed: true, probe: true}
	}
}

func (m *breakerModel) retryAfter(now time.Time) time.Duration {
	if m.state == "open" {
		if rem := modelCooldown - now.Sub(m.openedAt); rem > 0 {
			return rem
		}
	}
	return time.Second
}

// complete applies one admitted call's result and returns its outcome
// and expected Retry-After (zero for success).
func (m *breakerModel) complete(kind string, probe, callerDone bool, now time.Time) (string, time.Duration) {
	switch {
	case kind == kindOK:
		m.run = nil
		if m.state == "half_open" {
			m.state = "closed"
			m.closes++
		}
		m.probeInFlight = false
		return jwt.SignOutcomeOK, 0
	case kind == kindThrottled || callerDone:
		if probe && m.state == "half_open" {
			m.probeInFlight = false
		}
		outcome := jwt.SignOutcomeError
		if kind == kindThrottled {
			outcome = jwt.SignOutcomeThrottled
		}
		return outcome, m.retryAfter(now)
	default:
		m.failures++
		kept := m.run[:0]
		for _, f := range m.run {
			if now.Sub(f) < modelWindow {
				kept = append(kept, f)
			}
		}
		m.run = kept
		switch m.state {
		case "half_open":
			m.trip(now)
		case "closed":
			m.run = append(m.run, now)
			if len(m.run) > 3 {
				m.trip(now)
			}
		}
		return jwt.SignOutcomeError, m.retryAfter(now)
	}
}

func (m *breakerModel) trip(now time.Time) {
	m.state, m.openedAt, m.run, m.probeInFlight = "open", now, nil, false
	m.opens++
}

// heldCall is a Sign call parked inside the inner signer.
type heldCall struct {
	id     string
	kind   string
	probe  bool
	cancel context.CancelFunc
	gate   chan struct{}
	done   chan signResult
}

type signResult struct {
	out string
	err error
}

// checkSignResult asserts the error contract of one completed call.
func checkSignResult(t *testing.T, step int, res signResult, wantOutcome string, wantRetry time.Duration) {
	t.Helper()
	if wantOutcome == jwt.SignOutcomeOK {
		if res.err != nil || res.out != "tok" {
			t.Fatalf("step %d: got (%q, %v), want success", step, res.out, res.err)
		}
		return
	}
	var se *jwt.SigningError
	if !errors.As(res.err, &se) {
		t.Fatalf("step %d: error %v is not a *SigningError", step, res.err)
	}
	if se.RetryAfter != wantRetry {
		t.Fatalf("step %d: RetryAfter = %v, want %v", step, se.RetryAfter, wantRetry)
	}
	if got := errors.Is(res.err, jwt.ErrSigningUnavailable); got != (wantOutcome == jwt.SignOutcomeCircuitOpen) {
		t.Fatalf("step %d: errors.Is(ErrSigningUnavailable) = %v for outcome %s", step, got, wantOutcome)
	}
	if got := errors.Is(res.err, kms.ErrThrottled); got != (wantOutcome == jwt.SignOutcomeThrottled) {
		t.Fatalf("step %d: errors.Is(kms.ErrThrottled) = %v for outcome %s", step, got, wantOutcome)
	}
}

// spec: §10.2 (KMS signing failure mode), §16.1 (JWT sign requests).
// Randomized interleavings of every inner result, with calls held in
// flight across breaker transitions, checked step by step against the
// §10.2 oracle: trip and reopen exactly when the predicate holds,
// OnSigningFailure only for counted failures, half-open refusal only
// while a probe is in flight, the Retry-After on every error, and one
// OnOutcome per call.
func TestBreakerSignerRandomizedInterleavingsMatchSpecModel_spec_10_2(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	for trial := 0; trial < 40; trial++ {
		runInterleavingTrial(t, rng, trial)
	}
}

func runInterleavingTrial(t *testing.T, rng *rand.Rand, trial int) {
	inner := newScriptedSigner()
	obs := &stubObserver{}
	clock := &fakeClock{cur: time.Unix(10_000_000, 0)}
	b := &jwt.BreakerSigner{Inner: inner, Observer: obs, CallTimeout: time.Millisecond, Now: clock.Now}
	model := &breakerModel{state: "closed"}
	var held []*heldCall
	completed := 0
	advances := []time.Duration{0, 0, 100 * time.Millisecond, time.Second, 5 * time.Second, 29900 * time.Millisecond, 30100 * time.Millisecond}
	kinds := []string{kindOK, kindThrottled, kindHard, kindHard, kindHard, kindTimeout, kindCancelled}

	expectOutcome := func(step int, want string) {
		t.Helper()
		completed++
		obs.mu.Lock()
		defer obs.mu.Unlock()
		if len(obs.outcomes) != completed {
			t.Fatalf("trial %d step %d: %d OnOutcome calls after %d completed calls", trial, step, len(obs.outcomes), completed)
		}
		if got := obs.outcomes[completed-1]; got != want {
			t.Fatalf("trial %d step %d: OnOutcome %q, want %q", trial, step, got, want)
		}
	}

	for step := 0; step < 120; step++ {
		clock.Advance(advances[rng.Intn(len(advances))])
		now := clock.Now()
		switch action := rng.Intn(10); {
		case action < 3 && len(held) > 0:
			// Release a held call.
			i := rng.Intn(len(held))
			h := held[i]
			held = append(held[:i], held[i+1:]...)
			callerDone := h.kind == kindCancelled
			if callerDone {
				h.cancel()
			}
			close(h.gate)
			res := <-h.done
			h.cancel()
			outcome, retry := model.complete(h.kind, h.probe, callerDone, now)
			checkSignResult(t, step, res, outcome, retry)
			expectOutcome(step, outcome)
		default:
			kind := kinds[rng.Intn(len(kinds))]
			hold := action < 6 && kind != kindTimeout && len(held) < 4
			adm := model.admit(now)
			id := fmt.Sprintf("t%d-s%d", trial, step)
			ctx, cancel := context.WithCancel(context.Background())
			callerDone := kind == kindCancelled
			before := inner.innerCalls()
			if !adm.allowed || !hold {
				if callerDone {
					cancel()
				}
				out, err := b.Sign(ctx, jwt.Claims{Subject: kind, JWTID: id})
				cancel()
				res := signResult{out, err}
				if !adm.allowed {
					if inner.innerCalls() != before {
						t.Fatalf("trial %d step %d: refused call reached the inner signer", trial, step)
					}
					checkSignResult(t, step, res, jwt.SignOutcomeCircuitOpen, adm.retry)
					expectOutcome(step, jwt.SignOutcomeCircuitOpen)
					continue
				}
				outcome, retry := model.complete(kind, adm.probe, callerDone, now)
				checkSignResult(t, step, res, outcome, retry)
				expectOutcome(step, outcome)
				continue
			}
			h := &heldCall{id: id, kind: kind, probe: adm.probe, cancel: cancel, gate: inner.hold(id), done: make(chan signResult, 1)}
			go func() {
				out, err := b.Sign(ctx, jwt.Claims{Subject: kind, JWTID: id})
				h.done <- signResult{out, err}
			}()
			if got := <-inner.entered; got != id {
				t.Fatalf("trial %d step %d: signer entered by %q, want %q", trial, step, got, id)
			}
			held = append(held, h)
		}
		if got := b.State(); got != model.state {
			t.Fatalf("trial %d step %d: breaker state %q, model %q", trial, step, got, model.state)
		}
		obs.mu.Lock()
		fail, opened, closed, reject := obs.fail, obs.opened, obs.closed, obs.reject
		obs.mu.Unlock()
		if fail != model.failures || opened != model.opens || closed != model.closes {
			t.Fatalf("trial %d step %d: observer fail/open/close = %d/%d/%d, model %d/%d/%d",
				trial, step, fail, opened, closed, model.failures, model.opens, model.closes)
		}
		if circuitOpen := countOutcome(obs, jwt.SignOutcomeCircuitOpen); reject != circuitOpen {
			t.Fatalf("trial %d step %d: OnRejected %d, circuit_open outcomes %d", trial, step, reject, circuitOpen)
		}
	}
	for _, h := range held {
		close(h.gate)
		<-h.done
		h.cancel()
	}
}

func countOutcome(o *stubObserver, want string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, got := range o.outcomes {
		if got == want {
			n++
		}
	}
	return n
}

// newBoundaryBreaker builds a breaker on the spec defaults with a
// scripted signer and a settable clock.
func newBoundaryBreaker() (*jwt.BreakerSigner, *fakeClock, *stubObserver) {
	clock := &fakeClock{cur: time.Unix(20_000_000, 0)}
	obs := &stubObserver{}
	return &jwt.BreakerSigner{Inner: newScriptedSigner(), Observer: obs, CallTimeout: time.Millisecond, Now: clock.Now}, clock, obs
}

func signKind(b *jwt.BreakerSigner, kind string) error {
	_, err := b.Sign(context.Background(), jwt.Claims{Subject: kind})
	return err
}

// spec: §10.2 (KMS signing failure mode). The trip predicate at its
// boundaries: three failures stay closed, a fourth inside the 30 s
// window trips, a fourth after the first has left the window does not,
// and a success between failures restarts the consecutive run.
func TestBreakerSignerTripBoundaries_spec_10_2(t *testing.T) {
	cases := []struct {
		name  string
		steps []boundaryStep
		want  string
	}{
		{"three failures stay closed", []boundaryStep{{0, kindHard}, {time.Second, kindHard}, {2 * time.Second, kindHard}}, "closed"},
		{"fourth failure at 29.9s trips", []boundaryStep{{0, kindHard}, {time.Second, kindHard}, {2 * time.Second, kindHard}, {29900 * time.Millisecond, kindHard}}, "open"},
		{"fourth failure at 30.1s stays closed", []boundaryStep{{0, kindHard}, {time.Second, kindHard}, {2 * time.Second, kindHard}, {30100 * time.Millisecond, kindHard}}, "closed"},
		{"fail fail ok fail fail stays closed", []boundaryStep{{0, kindHard}, {time.Second, kindHard}, {2 * time.Second, kindOK}, {3 * time.Second, kindHard}, {4 * time.Second, kindHard}}, "closed"},
		{"timeouts count toward the trip", []boundaryStep{{0, kindTimeout}, {time.Second, kindTimeout}, {2 * time.Second, kindTimeout}, {3 * time.Second, kindTimeout}}, "open"},
		{"throttles neither trip nor clear the run", []boundaryStep{{0, kindHard}, {time.Second, kindThrottled}, {2 * time.Second, kindHard}, {3 * time.Second, kindThrottled}, {4 * time.Second, kindThrottled}, {5 * time.Second, kindHard}, {6 * time.Second, kindHard}}, "open"},
		{"five throttles stay closed", []boundaryStep{{0, kindThrottled}, {0, kindThrottled}, {0, kindThrottled}, {0, kindThrottled}, {0, kindThrottled}}, "closed"},
		{"caller-ended errors stay closed", []boundaryStep{{0, kindCancelled}, {0, kindCancelled}, {0, kindCancelled}, {0, kindCancelled}, {0, kindCancelled}}, "closed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, clock, _ := newBoundaryBreaker()
			start := clock.Now()
			for _, st := range tc.steps {
				clock.Advance(start.Add(st.at).Sub(clock.Now()))
				ctx, cancel := context.WithCancel(context.Background())
				if st.kind == kindCancelled {
					cancel()
				}
				_, _ = b.Sign(ctx, jwt.Claims{Subject: st.kind})
				cancel()
			}
			if got := b.State(); got != tc.want {
				t.Fatalf("State = %q, want %q", got, tc.want)
			}
		})
	}
}

// boundaryStep is one call at an offset from the scenario start.
type boundaryStep struct {
	at   time.Duration
	kind string
}

// tripOpen drives the breaker open with four hard failures at the
// current instant and then advances past the cooldown.
func tripOpen(t *testing.T, b *jwt.BreakerSigner, clock *fakeClock) {
	t.Helper()
	for i := 0; i < 4; i++ {
		_ = signKind(b, kindHard)
	}
	if b.State() != "open" {
		t.Fatalf("setup: State = %q, want open", b.State())
	}
	clock.Advance(jwt.SigningBreakerCooldown)
}

// spec: §10.2 (KMS signing failure mode). A half-open probe that the
// provider throttles, or whose caller goes away, gives up the probe
// slot without reopening: the next call reaches the inner signer as
// the probe, and its success closes the breaker. Before throttling was
// classified, a throttled probe reopened the breaker.
func TestBreakerSignerHalfOpenProbeReleasedByThrottleOrCancel_spec_10_2(t *testing.T) {
	for _, kind := range []string{kindThrottled, kindCancelled} {
		t.Run(kind, func(t *testing.T) {
			b, clock, obs := newBoundaryBreaker()
			inner := b.Inner.(*scriptedSigner)
			tripOpen(t, b, clock)
			ctx, cancel := context.WithCancel(context.Background())
			if kind == kindCancelled {
				cancel()
			}
			_, err := b.Sign(ctx, jwt.Claims{Subject: kind})
			cancel()
			var se *jwt.SigningError
			if !errors.As(err, &se) || se.RetryAfter != time.Second {
				t.Fatalf("probe error = %v, want *SigningError with RetryAfter 1s", err)
			}
			if got := b.State(); got != "half_open" {
				t.Fatalf("State after %s probe = %q, want half_open", kind, got)
			}
			before := inner.innerCalls()
			if err := signKind(b, kindOK); err != nil {
				t.Fatalf("next probe: %v", err)
			}
			if inner.innerCalls() != before+1 {
				t.Fatal("next call did not reach the inner signer")
			}
			if got := b.State(); got != "closed" {
				t.Fatalf("State after successful probe = %q, want closed", got)
			}
			if obs.opened != 1 || obs.closed != 1 || obs.fail != 4 {
				t.Fatalf("observer open/close/fail = %d/%d/%d, want 1/1/4", obs.opened, obs.closed, obs.fail)
			}
		})
	}
}

// spec: §10.2 (KMS signing failure mode). A half-open breaker refuses
// a second call while the probe is in flight, with Retry-After 1s and
// the circuit_open outcome, and admits the next call once the probe
// releases its slot.
func TestBreakerSignerHalfOpenRefusesOnlyWhileProbeInFlight_spec_10_2(t *testing.T) {
	b, clock, obs := newBoundaryBreaker()
	inner := b.Inner.(*scriptedSigner)
	tripOpen(t, b, clock)
	gate := inner.hold("probe")
	done := make(chan error, 1)
	go func() {
		_, err := b.Sign(context.Background(), jwt.Claims{Subject: kindThrottled, JWTID: "probe"})
		done <- err
	}()
	<-inner.entered
	err := signKind(b, kindOK)
	var se *jwt.SigningError
	if !errors.As(err, &se) || !errors.Is(err, jwt.ErrSigningUnavailable) || se.RetryAfter != time.Second {
		t.Fatalf("call during probe = %v, want circuit-open SigningError with RetryAfter 1s", err)
	}
	close(gate)
	if err := <-done; !errors.Is(err, kms.ErrThrottled) {
		t.Fatalf("probe error = %v, want throttled", err)
	}
	if err := signKind(b, kindOK); err != nil {
		t.Fatalf("call after probe release: %v", err)
	}
	want := []string{
		jwt.SignOutcomeError, jwt.SignOutcomeError, jwt.SignOutcomeError, jwt.SignOutcomeError,
		jwt.SignOutcomeCircuitOpen, jwt.SignOutcomeThrottled, jwt.SignOutcomeOK,
	}
	if fmt.Sprint(obs.outcomes) != fmt.Sprint(want) {
		t.Fatalf("outcomes = %v, want %v", obs.outcomes, want)
	}
	if obs.reject != 1 {
		t.Fatalf("OnRejected = %d, want 1", obs.reject)
	}
}

// spec: §10.2 (KMS signing failure mode). An inner call that outlives
// the per-call timeout while the caller's context is live is a counted
// failure: it fires OnSigningFailure and reports the error outcome.
func TestBreakerSignerCallTimeoutCountsAsFailure_spec_10_2(t *testing.T) {
	b, _, obs := newBoundaryBreaker()
	err := signKind(b, kindTimeout)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if obs.fail != 1 || fmt.Sprint(obs.outcomes) != "[error]" {
		t.Fatalf("fail = %d outcomes = %v, want 1 [error]", obs.fail, obs.outcomes)
	}
}

// spec: §10.2 (KMS signing failure mode). The inner call runs under a
// deadline of SigningCallTimeout when no override is set.
func TestBreakerSignerAppliesDefaultCallTimeout_spec_10_2(t *testing.T) {
	var deadline time.Time
	var ok bool
	inner := &deadlineSigner{record: func(ctx context.Context) { deadline, ok = ctx.Deadline() }}
	b := &jwt.BreakerSigner{Inner: inner}
	start := time.Now()
	if _, err := b.Sign(context.Background(), jwt.Claims{}); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !ok {
		t.Fatal("inner context carries no deadline")
	}
	if d := deadline.Sub(start); d < 4*time.Second || d > 6*time.Second {
		t.Fatalf("inner deadline %v after start, want about 5s", d)
	}
}

type deadlineSigner struct{ record func(context.Context) }

func (d *deadlineSigner) Sign(ctx context.Context, _ jwt.Claims) (string, error) {
	d.record(ctx)
	return "tok", nil
}
func (d *deadlineSigner) KeyID() string { return "deadline" }

// spec: §10.2 (KMS signing failure mode). A breaker-open refusal
// carries the remaining cooldown as Retry-After.
func TestBreakerSignerOpenRefusalCarriesRemainingCooldown_spec_10_2(t *testing.T) {
	b, clock, _ := newBoundaryBreaker()
	for i := 0; i < 4; i++ {
		_ = signKind(b, kindHard)
	}
	clock.Advance(27700 * time.Millisecond)
	var se *jwt.SigningError
	if err := signKind(b, kindOK); !errors.As(err, &se) || !errors.Is(err, jwt.ErrSigningUnavailable) {
		t.Fatalf("err = %v, want circuit-open SigningError", err)
	}
	if se.RetryAfter != 2300*time.Millisecond || se.RetryAfterSeconds() != 3 {
		t.Fatalf("RetryAfter = %v (%ds), want 2.3s (3s)", se.RetryAfter, se.RetryAfterSeconds())
	}
}

// spec: §10.2 (KMS signing failure mode). Retry-After is whole seconds,
// rounded up, and at least 1.
func TestSigningErrorRetryAfterSecondsRoundsUpWithFloor_spec_10_2(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want int
	}{
		{0, 1},
		{-time.Second, 1},
		{400 * time.Millisecond, 1},
		{time.Second, 1},
		{time.Second + time.Nanosecond, 2},
		{2300 * time.Millisecond, 3},
		{30 * time.Second, 30},
	}
	for _, tc := range cases {
		if got := (&jwt.SigningError{RetryAfter: tc.in}).RetryAfterSeconds(); got != tc.want {
			t.Errorf("RetryAfterSeconds(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// spec: §10.2 (KMS signing failure mode). SigningError wraps the inner
// error and its message names it.
func TestSigningErrorWrapsInner_spec_10_2(t *testing.T) {
	err := error(&jwt.SigningError{RetryAfter: time.Second, Err: errHardKMS})
	if !errors.Is(err, errHardKMS) {
		t.Fatal("SigningError does not unwrap to the inner error")
	}
	if !strings.Contains(err.Error(), errHardKMS.Error()) {
		t.Fatalf("message %q omits the inner error", err.Error())
	}
}

// spec: §10.2 (KMS signing failure mode). Concurrent calls with mixed
// results keep the per-call contract under the race detector: every
// call reports exactly one outcome and every error is a SigningError.
func TestBreakerSignerConcurrentCallsReportOneOutcomeEach_spec_10_2(t *testing.T) {
	b, _, obs := newBoundaryBreaker()
	kinds := []string{kindOK, kindThrottled, kindHard, kindCancelled}
	const n = 200
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(kind string) {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			if kind == kindCancelled {
				cancel()
			}
			defer cancel()
			if _, err := b.Sign(ctx, jwt.Claims{Subject: kind}); err != nil {
				var se *jwt.SigningError
				if !errors.As(err, &se) || se.RetryAfterSeconds() < 1 {
					errs <- fmt.Errorf("%s: %v is not a SigningError with Retry-After", kind, err)
				}
			}
		}(kinds[i%len(kinds)])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := len(obs.outcomes); got != n {
		t.Fatalf("OnOutcome calls = %d, want %d", got, n)
	}
	if obs.reject != countOutcome(obs, jwt.SignOutcomeCircuitOpen) {
		t.Fatalf("OnRejected %d != circuit_open outcomes", obs.reject)
	}
}
