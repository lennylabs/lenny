// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/slotstate"
)

// fakeSlotBinder records the calls applySlotRetryPolicy makes and replays a
// scripted sequence of BindSlot outcomes (one per attempt).
type fakeSlotBinder struct {
	results  []*podsession.BindResult
	errs     []error
	bindCall int

	released []slotRelease
	drained  []string

	// releaseErr, when non-nil, makes ReleaseSlotReservation fail so the
	// failed slot is not reclaimed — the §6.2 leak path.
	releaseErr error
}

func (f *fakeSlotBinder) BindSlot(_ context.Context, _ podsession.SlotBindRequest) (*podsession.BindResult, error) {
	i := f.bindCall
	f.bindCall++
	var res *podsession.BindResult
	if i < len(f.results) {
		res = f.results[i]
	}
	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return res, err
}

// slotRelease records one ReleaseSlotReservation call: the pod, the slot
// identifier, and the leaked disposition the caller passed.
type slotRelease struct {
	pod, slotID string
	leaked      bool
}

func (f *fakeSlotBinder) ReleaseSlotReservation(_ context.Context, pod, slotID string, leaked bool) error {
	f.released = append(f.released, slotRelease{pod: pod, slotID: slotID, leaked: leaked})
	return f.releaseErr
}

func (f *fakeSlotBinder) DrainSandbox(_ context.Context, pod string) error {
	f.drained = append(f.drained, pod)
	return nil
}

func slotBindErr(pod, slotID, stage string, code codes.Code) *podsession.SlotBindError {
	return &podsession.SlotBindError{
		Pod: pod, SlotID: slotID, Stage: stage,
		Err: fmt.Errorf("stage failed: %w", status.Error(code, "boom")),
	}
}

func req(pool string, maxConcurrentSessions int32) podsession.SlotBindRequest {
	return podsession.SlotBindRequest{Pool: pool, SessionID: "sess-1", MaxConcurrentSessions: maxConcurrentSessions}
}

// spec: §5.2 — a transient slot failure is retried once on a fresh slot;
// the retry succeeding yields the bound result and no client error.
func TestSlotRetryTransientThenSuccess_spec_5_2(t *testing.T) {
	binder := &fakeSlotBinder{
		results: []*podsession.BindResult{nil, {SessionID: "sess-1", SandboxName: "pod-a", SlotID: "sess-1"}},
		errs:    []error{slotBindErr("pod-a", "sess-1", "session_start", codes.Unavailable), nil},
	}
	health := slothealth.New()
	res, err := applySlotRetryPolicy(context.Background(), binder, health, nil, nil, nil, req("pool-x", 4))
	if err != nil {
		t.Fatalf("expected success after one retry, got %v", err)
	}
	if res == nil || res.SandboxName != "pod-a" {
		t.Fatalf("unexpected result %+v", res)
	}
	if binder.bindCall != 2 {
		t.Errorf("BindSlot calls = %d, want 2 (original + one retry)", binder.bindCall)
	}
	// §5.2 fresh-slot guarantee: the failed slot is released before retry.
	if len(binder.released) != 1 || binder.released[0] != (slotRelease{pod: "pod-a", slotID: "sess-1"}) {
		t.Errorf("released = %v, want one release of pod-a/sess-1", binder.released)
	}
	// maxConcurrent=4 → threshold 2; a single failure must not drain.
	if len(binder.drained) != 0 {
		t.Errorf("drained = %v, want none (below unhealthy threshold)", binder.drained)
	}
}

// spec: §5.2 — a non-retryable reason (workspace_validation) is returned to
// the client immediately without a retry, as a structured SlotFailedError.
func TestSlotRetryNonRetryableNoRetry_spec_5_2(t *testing.T) {
	binder := &fakeSlotBinder{
		errs: []error{slotBindErr("pod-b", "sess-1", "workspace_prep", codes.InvalidArgument)},
	}
	health := slothealth.New()
	_, err := applySlotRetryPolicy(context.Background(), binder, health, nil, nil, nil, req("pool-x", 4))
	var sf *podsession.SlotFailedError
	if !errors.As(err, &sf) {
		t.Fatalf("expected *SlotFailedError, got %v", err)
	}
	if sf.Category != string(podsession.SlotReasonWorkspaceValidation) {
		t.Errorf("category = %q, want workspace_validation", sf.Category)
	}
	if sf.SessionID != "sess-1" {
		t.Errorf("sessionId = %q, want sess-1", sf.SessionID)
	}
	if binder.bindCall != 1 {
		t.Errorf("BindSlot calls = %d, want 1 (no retry for a non-retryable reason)", binder.bindCall)
	}
	if len(binder.released) != 1 {
		t.Errorf("released = %v, want the failed slot released once", binder.released)
	}
}

// spec: §5.2 — an exhausted retry (both attempts transient-fail) returns the
// structured SlotFailedError with the transient category.
func TestSlotRetryExhaustedReturnsStructuredError_spec_5_2(t *testing.T) {
	binder := &fakeSlotBinder{
		errs: []error{
			slotBindErr("pod-c", "sess-1", "session_start", codes.Unavailable),
			slotBindErr("pod-c", "sess-1", "session_start", codes.Unavailable),
		},
	}
	health := slothealth.New()
	_, err := applySlotRetryPolicy(context.Background(), binder, health, nil, nil, nil, req("pool-x", 8))
	var sf *podsession.SlotFailedError
	if !errors.As(err, &sf) {
		t.Fatalf("expected *SlotFailedError, got %v", err)
	}
	if sf.Category != string(podsession.SlotReasonTransient) {
		t.Errorf("category = %q, want transient", sf.Category)
	}
	if binder.bindCall != 2 {
		t.Errorf("BindSlot calls = %d, want 2 (original + one retry)", binder.bindCall)
	}
}

// spec: §5.2 / §6.2 — when a pod crosses the ceil(maxConcurrent/2)
// fail threshold the pod is drained as a whole and the replacement counter
// fires once per drain.
func TestSlotRetryDrainsUnhealthyPod_spec_5_2(t *testing.T) {
	// maxConcurrent=2 → threshold ceil(2/2)=1: a single failure on pod-d
	// trips it. Two pods so each attempt lands on a distinct pod, both of
	// which fail and drain.
	binder := &fakeSlotBinder{
		errs: []error{
			slotBindErr("pod-d", "sess-1", "session_start", codes.Unavailable),
			slotBindErr("pod-e", "sess-1", "session_start", codes.Unavailable),
		},
	}
	health := slothealth.New()
	var replacements []string
	repl := func(pool string) { replacements = append(replacements, pool) }

	_, err := applySlotRetryPolicy(context.Background(), binder, health, nil, repl, nil, req("pool-x", 2))
	var sf *podsession.SlotFailedError
	if !errors.As(err, &sf) {
		t.Fatalf("expected *SlotFailedError after exhausted retry, got %v", err)
	}
	if len(binder.drained) != 2 {
		t.Errorf("drained = %v, want both pods drained on the threshold", binder.drained)
	}
	if len(replacements) != 2 {
		t.Errorf("replacement counter fired %d times, want 2 (one per drained pod)", len(replacements))
	}
}

// spec: §6.2 "`leaked` slot semantics" — when the failed slot's reservation
// cannot be reclaimed, the slot is leaked: it is recorded in the per-slot
// registry and the per-pod lenny_adapter_leaked_slots gauge is published. The
// slot is counted once toward the unhealthy threshold through the persistent
// RecordLeak (not also the windowed RecordFailure), so a single leak below
// threshold does not drain.
func TestSlotRetryLeakOnReleaseFailure_spec_6_2(t *testing.T) {
	// One transient failure, then a success on retry — but the failed slot's
	// release errors so it is leaked. maxConcurrent=4 → threshold 2, so the
	// pod is not drained and the leaked slot persists in the gauge.
	binder := &fakeSlotBinder{
		results:    []*podsession.BindResult{nil, {SessionID: "sess-1", SandboxName: "pod-a", SlotID: "sess-1"}},
		errs:       []error{slotBindErr("pod-a", "sess-1", "session_start", codes.Unavailable), nil},
		releaseErr: errors.New("slot cleanup timed out"),
	}
	health := slothealth.New()
	slots := slotstate.NewRegistry()
	var gauge []struct {
		pod, pool string
		leaked    int
	}
	leakGauge := func(pod, pool string, leaked int) {
		gauge = append(gauge, struct {
			pod, pool string
			leaked    int
		}{pod, pool, leaked})
	}

	res, err := applySlotRetryPolicy(context.Background(), binder, health, slots, nil, leakGauge, req("pool-x", 4))
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if res == nil || res.SandboxName != "pod-a" {
		t.Fatalf("unexpected result %+v", res)
	}
	// The leaked slot is recorded for the pod and published to the gauge.
	if got := slots.LeakedCount("pod-a"); got != 1 {
		t.Errorf("registry leaked count for pod-a = %d, want 1", got)
	}
	if st, ok := slots.State("sess-1"); !ok || st != slotstate.Leaked {
		t.Errorf("slot sess-1 state = %q ok=%v, want leaked/true", st, ok)
	}
	if len(gauge) != 1 || gauge[0].pod != "pod-a" || gauge[0].pool != "pool-x" || gauge[0].leaked != 1 {
		t.Errorf("leak gauge = %+v, want one set of pod-a/pool-x/1", gauge)
	}
	// Below threshold: a single failed/leaked slot must not drain the pod.
	if len(binder.drained) != 0 {
		t.Errorf("drained = %v, want none (one leak is below the maxConcurrent=4 threshold)", binder.drained)
	}
}

// spec: §6.2 — when a pod with a leaked slot crosses the unhealthy
// threshold and is drained for replacement, its leaked-slot tracking is
// cleared and the gauge series is zeroed (the leaked slots are reclaimed
// with the terminated pod).
func TestSlotRetryDrainClearsLeakGauge_spec_6_2(t *testing.T) {
	// maxConcurrent=2 → threshold 1: a single failure trips it. Two pods so
	// each attempt lands on a distinct pod, both fail, both leak on a failing
	// release, and both drain — the drain zeroes each pod's leak gauge.
	binder := &fakeSlotBinder{
		errs: []error{
			slotBindErr("pod-d", "sess-1", "session_start", codes.Unavailable),
			slotBindErr("pod-e", "sess-1", "session_start", codes.Unavailable),
		},
		releaseErr: errors.New("slot cleanup timed out"),
	}
	health := slothealth.New()
	slots := slotstate.NewRegistry()
	lastGauge := -1
	leakGauge := func(_, _ string, leaked int) { lastGauge = leaked }

	_, err := applySlotRetryPolicy(context.Background(), binder, health, slots, func(string) {}, leakGauge, req("pool-x", 2))
	var sf *podsession.SlotFailedError
	if !errors.As(err, &sf) {
		t.Fatalf("expected *SlotFailedError after exhausted retry, got %v", err)
	}
	if len(binder.drained) != 2 {
		t.Fatalf("drained = %v, want both pods drained on the threshold", binder.drained)
	}
	if slots.LeakedCount("pod-d") != 0 || slots.LeakedCount("pod-e") != 0 {
		t.Errorf("drained pods' leaked slots must be cleared, got pod-d=%d pod-e=%d",
			slots.LeakedCount("pod-d"), slots.LeakedCount("pod-e"))
	}
	if lastGauge != 0 {
		t.Errorf("gauge must be zeroed on drain, last value = %d", lastGauge)
	}
}

// spec: §6.2 "`leaked` slot semantics" — a leaked slot (its reservation
// could not be reclaimed) is counted persistently rather than in the rolling
// 5-minute window, so leaks that accumulate slowly (more than one window
// apart) still reach the ceil(maxConcurrent/2) unhealthy threshold instead of
// aging out. This is the SPEC-D bind-time leaked-slot producer fix (CODE-D/D1
// extension): the pre-fix path recorded a bind-time leak through the windowed
// RecordFailure, so two permanent leaks arriving more than five minutes apart
// each aged out and never combined to trip the threshold. This test drives a
// second bind-time leak more than a window after the first and asserts the pod
// is drained on the persistent leaked count; it fails against the pre-fix
// RecordFailure path where the first leak has aged out.
func TestSlotRetryBindLeakCountedPersistently_spec_6_2(t *testing.T) {
	// maxConcurrent=3 → threshold ceil(3/2)=2: two leaked slots trip it. A
	// monotonic clock lets us place the two leaks more than a window apart so a
	// windowed count would prune the first before the second lands.
	now := time.Unix(0, 0)
	health := slothealth.New(slothealth.WithClock(func() time.Time { return now }))
	slots := slotstate.NewRegistry()

	// A non-retryable reason so the single failing attempt leaks (release errors)
	// and returns without re-binding a fresh slot; the leak is the only recorded
	// event.
	binderOne := &fakeSlotBinder{
		errs:       []error{slotBindErr("pod-a", "slot-1", "workspace_prep", codes.InvalidArgument)},
		releaseErr: errors.New("slot cleanup timed out"),
	}
	_, _ = applySlotRetryPolicy(context.Background(), binderOne, health, slots, func(string) {}, nil, req("pool-x", 3))
	if len(binderOne.drained) != 0 {
		t.Fatalf("first leak alone must not drain (below threshold 2), drained=%v", binderOne.drained)
	}

	// Advance well past the 5-minute rolling window. A windowed leak count (the
	// pre-fix RecordFailure path) prunes the first leak here; a persistent count
	// (RecordLeak) retains it.
	now = now.Add(6 * time.Minute)

	binder := &fakeSlotBinder{
		errs:       []error{slotBindErr("pod-a", "slot-2", "workspace_prep", codes.InvalidArgument)},
		releaseErr: errors.New("slot cleanup timed out"),
	}
	var replacements []string
	repl := func(pool string) { replacements = append(replacements, pool) }
	_, err := applySlotRetryPolicy(context.Background(), binder, health, slots, repl, nil, req("pool-x", 3))
	var sf *podsession.SlotFailedError
	if !errors.As(err, &sf) {
		t.Fatalf("expected *SlotFailedError, got %v", err)
	}
	// Two permanent leaks a window apart reach threshold 2 only if the first was
	// counted persistently. The pre-fix windowed RecordFailure would have pruned
	// leak one, leaving the count at 1 and the pod undrained.
	if len(binder.drained) != 1 || binder.drained[0] != "pod-a" {
		t.Errorf("drained = %v, want pod-a drained on two persistent leaks reaching the threshold", binder.drained)
	}
	if len(replacements) != 1 {
		t.Errorf("replacement counter fired %d times, want 1 (one drain of pod-a)", len(replacements))
	}
}

// spec: §6.2 "`leaked` slot semantics" — a transient failure (the failed
// slot's reservation released cleanly) stays counted in the rolling 5-minute
// window, so two transient failures more than a window apart age the first out
// and never combine to trip the threshold. This confirms the release-outcome
// branch split records a cleanly-released failure through the windowed
// RecordFailure rather than the persistent RecordLeak.
func TestSlotRetryTransientFailureAgesOutOfWindow_spec_6_2(t *testing.T) {
	// maxConcurrent=3 → threshold 2. Two transient failures a window apart must
	// NOT drain, because a cleanly-released failure is windowed.
	now := time.Unix(0, 0)
	health := slothealth.New(slothealth.WithClock(func() time.Time { return now }))

	failOnce := func(slotID string) []string {
		binder := &fakeSlotBinder{
			// Non-retryable, clean release (releaseErr nil) → transient windowed
			// failure, no leak.
			errs: []error{slotBindErr("pod-b", slotID, "workspace_prep", codes.InvalidArgument)},
		}
		_, _ = applySlotRetryPolicy(context.Background(), binder, health, slotstate.NewRegistry(), func(string) {}, nil, req("pool-x", 3))
		return binder.drained
	}
	if d := failOnce("slot-1"); len(d) != 0 {
		t.Fatalf("first transient failure alone must not drain, drained=%v", d)
	}
	now = now.Add(6 * time.Minute)
	if d := failOnce("slot-2"); len(d) != 0 {
		t.Errorf("two transient failures a window apart must not drain (first aged out), drained=%v", d)
	}
	// Counts confirms the split: no leaks recorded, one windowed failure survives.
	failed, leaked := health.Counts("pod-b")
	if leaked != 0 {
		t.Errorf("leaked count = %d, want 0 (clean releases are not leaks)", leaked)
	}
	if failed != 1 {
		t.Errorf("windowed failed count = %d, want 1 (only the most recent survives the window)", failed)
	}
}

// spec: §5.2 — a reservation-exhaustion sentinel is not a slot
// failure: it is returned unchanged (no release, no record, no retry) so
// the handler maps it to WARM_POOL_EXHAUSTED.
func TestSlotRetryPassesThroughExhaustionSentinel_spec_5_2(t *testing.T) {
	binder := &fakeSlotBinder{errs: []error{podclaim.ErrNoConcurrentSlot}}
	health := slothealth.New()
	_, err := applySlotRetryPolicy(context.Background(), binder, health, nil, nil, nil, req("pool-x", 4))
	if !errors.Is(err, podclaim.ErrNoConcurrentSlot) {
		t.Fatalf("expected ErrNoConcurrentSlot unchanged, got %v", err)
	}
	if binder.bindCall != 1 {
		t.Errorf("BindSlot calls = %d, want 1 (no retry on exhaustion)", binder.bindCall)
	}
	if len(binder.released) != 0 || len(binder.drained) != 0 {
		t.Errorf("exhaustion must not release or drain: released=%v drained=%v", binder.released, binder.drained)
	}
}

// SlotBindError.Reason classifies gRPC codes into the §5.2 retry categories.
func TestSlotBindErrorReason_spec_5_2(t *testing.T) {
	cases := []struct {
		stage string
		code  codes.Code
		want  podsession.SlotFailureReason
	}{
		{"workspace_prep", codes.InvalidArgument, podsession.SlotReasonWorkspaceValidation},
		{"session_start", codes.ResourceExhausted, podsession.SlotReasonOOM},
		{"session_start", codes.PermissionDenied, podsession.SlotReasonPolicyRejection},
		// The adapter's §5.2 reclaim hold, the §4.7.1 identity gate and the
		// start-confirmation rollback answer Aborted, which takes the
		// transient default at every stage, so the slot retry loop repeats
		// the bind on a fresh attempt.
		{"session_start", codes.Aborted, podsession.SlotReasonTransient},
		{"workspace_prep", codes.Aborted, podsession.SlotReasonTransient},
		// The §4.7.1 started-session refusal answers FailedPrecondition, which
		// is non-retryable inside the slot retry loop; the client envelope is
		// chosen separately by writePodClaimError.
		{"session_start", codes.FailedPrecondition, podsession.SlotReasonPolicyRejection},
		{"setup", codes.FailedPrecondition, podsession.SlotReasonPolicyRejection},
		// A FailedPrecondition in the workspace stage is an ordinary
		// materialization failure, not a policy rejection: stay transient.
		{"workspace_prep", codes.FailedPrecondition, podsession.SlotReasonTransient},
		{"session_start", codes.Unavailable, podsession.SlotReasonTransient},
		{"connect", codes.Unknown, podsession.SlotReasonTransient},
	}
	for _, c := range cases {
		e := slotBindErr("pod", "slot", c.stage, c.code)
		if got := e.Reason(); got != c.want {
			t.Errorf("Reason(stage=%s, code=%s) = %q, want %q", c.stage, c.code, got, c.want)
		}
		if c.want.NonRetryable() == (c.want == podsession.SlotReasonTransient) {
			t.Errorf("NonRetryable(%q) inconsistent", c.want)
		}
	}
}

// spec: §5.2 "Client error on exhaustion" — the structured client error
// names the session whose slot failed, taken from the bind request, so the
// 422 body identifies the session on every route that reaches the mapper.
// A bind that fails before the pod-side slot is addressable reports no slot
// on its SlotBindError; the session identifier the request already carries
// is what the client is told, because a session-mode slot's identifier is
// its session's identifier.
func TestSlotRetryFailureNamesSessionWithoutSlotAddress_spec_5_2(t *testing.T) {
	binder := &fakeSlotBinder{
		errs: []error{slotBindErr("pod-b", "", "workspace_prep", codes.InvalidArgument)},
	}
	_, err := applySlotRetryPolicy(context.Background(), binder, slothealth.New(), nil, nil, nil, req("pool-x", 4))
	var sf *podsession.SlotFailedError
	if !errors.As(err, &sf) {
		t.Fatalf("expected *SlotFailedError, got %v", err)
	}
	if sf.SessionID != "sess-1" {
		t.Errorf("sessionId = %q, want sess-1 (the request's session, not the bind error's slot address)", sf.SessionID)
	}
	if sf.Pool != "pool-x" {
		t.Errorf("pool = %q, want pool-x", sf.Pool)
	}
}

// spec: §5.2 "Client error on exhaustion" — the no-retry slot paths (the
// create-time reservation and the reconnect to a slot reserved at create)
// classify a post-reservation failure as the structured client error, so the
// 422 body names the session whose slot failed and carries the §5.2 failure
// category. Without the classification the failure reaches the handler as a
// bare bind error and falls through to the retryable creation fallback.
func TestClassifySlotBindFailureNamesSession_spec_5_2(t *testing.T) {
	err := classifySlotBindFailure(
		slotBindErr("pod-b", "sess-1", "connect", codes.InvalidArgument), req("pool-x", 4),
	)
	var sf *podsession.SlotFailedError
	if !errors.As(err, &sf) {
		t.Fatalf("expected *SlotFailedError, got %v", err)
	}
	if sf.SessionID != "sess-1" {
		t.Errorf("sessionId = %q, want sess-1", sf.SessionID)
	}
	if sf.Category != string(podsession.SlotReasonWorkspaceValidation) {
		t.Errorf("category = %q, want workspace_validation", sf.Category)
	}
	if sf.Pool != "pool-x" {
		t.Errorf("pool = %q, want pool-x", sf.Pool)
	}
}

// spec: §5.2 "Client error on exhaustion" — the client error is conditioned
// on either no retry being attempted (a non-retryable category) or the retry
// budget being exhausted. The no-retry slot paths carry no budget, so a
// transient post-reservation failure (a dial Unavailable, a DeadlineExceeded,
// a workspace-prep FailedPrecondition) satisfies neither half and must pass
// through unclassified, keeping the retryable SESSION_CREATION_FAILED /
// STARTING_FAILED fallback and its Retry-After (§15.1). Classifying it would
// answer 422 SLOT_FAILED with category "transient" and retryable=false,
// telling the client a recoverable transport failure is terminal.
func TestClassifySlotBindFailurePassesTransientThrough_spec_5_2(t *testing.T) {
	transient := []struct {
		name  string
		stage string
		code  codes.Code
	}{
		{"dial unavailable", "connect", codes.Unavailable},
		{"deadline exceeded", "session_start", codes.DeadlineExceeded},
		{"workspace-prep failed precondition", "workspace_prep", codes.FailedPrecondition},
		{"unknown", "connect", codes.Unknown},
		{"session-start aborted refusal", "session_start", codes.Aborted},
		{"workspace-prep aborted refusal", "workspace_prep", codes.Aborted},
	}
	for _, tc := range transient {
		t.Run(tc.name, func(t *testing.T) {
			in := slotBindErr("pod-b", "sess-1", tc.stage, tc.code)
			out := classifySlotBindFailure(in, req("pool-x", 4))
			if out != error(in) {
				t.Fatalf("transient bind failure was rewritten: %v", out)
			}
			var sf *podsession.SlotFailedError
			if errors.As(out, &sf) {
				t.Errorf("transient bind failure classified as the §5.2 client error: category = %q, retryable=false", sf.Category)
			}
		})
	}
}

// spec: §5.2 — a reservation-exhaustion sentinel reserved no slot, so it is
// not a slot failure and passes through unchanged for the
// WARM_POOL_EXHAUSTED (or creation-atomicity) mapping.
func TestClassifySlotBindFailurePassesExhaustionThrough_spec_5_2(t *testing.T) {
	in := fmt.Errorf("claim: %w", podclaim.ErrNoConcurrentSlot)
	out := classifySlotBindFailure(in, req("pool-x", 4))
	if out != in {
		t.Fatalf("exhaustion sentinel was rewritten: %v", out)
	}
	var sf *podsession.SlotFailedError
	if errors.As(out, &sf) {
		t.Errorf("exhaustion sentinel classified as a slot failure: %v", sf)
	}
}

// spec: §7.3 (setup_command_failed non-retryable), §5.2 "Client error on
// exhaustion", §15.1 (SETUP_COMMAND_FAILED) — a setup-command exit that
// reaches the no-retry slot paths is still answered with the typed
// SETUP_COMMAND_FAILED envelope rather than the §5.2 SLOT_FAILED one. The
// classification wraps the bind error rather than replacing it, so the
// setup failure stays on the unwrap chain and the typed handler, which the
// §15.1 mapper checks before the slot case, wins. Replacing the chain with
// the classified cause, or checking the slot case first, would answer a
// deterministic setup exit with the slot envelope and lose the per-command
// transcript the client reads.
func TestClassifiedSlotFailureKeepsSetupCommandEnvelope_spec_7_3(t *testing.T) {
	setupFail := &podsession.SetupCommandFailure{
		Pod:   "pod-b",
		Cause: status.Error(codes.FailedPrecondition, "run setup commands: exit 1"),
	}
	bindErr := &podsession.SlotBindError{
		Pod: "pod-b", SlotID: "sess-1", Stage: "session_start",
		Err: fmt.Errorf("start session: %w", setupFail),
	}
	out := classifySlotBindFailure(bindErr, req("pool-x", 4))

	var sf *podsession.SlotFailedError
	if !errors.As(out, &sf) {
		t.Fatalf("a policy_rejection-class bind failure was not classified: %v", out)
	}
	var reachedSetup *podsession.SetupCommandFailure
	if !errors.As(out, &reachedSetup) {
		t.Fatalf("the setup failure left the unwrap chain: %v", out)
	}

	s := New(memstore.New(), Options{})
	w := httptest.NewRecorder()
	s.writePodClaimError(w, out, "SESSION_CREATION_FAILED", "could not place the session on a warm pod")
	if w.Code != 422 {
		t.Fatalf("status = %d, want 422; body=%s", w.Code, w.Body.String())
	}
	body := decodeErrorBody(t, w.Body.Bytes())
	if body["code"] != "SETUP_COMMAND_FAILED" {
		t.Errorf("code = %v, want SETUP_COMMAND_FAILED (the typed handler wins over SLOT_FAILED)", body["code"])
	}
}

// The §5.2 retry policy releases a failed slot with the disposition the
// binder's compensating reclaim put on the SlotBindError: a reclaim not
// acknowledged clean is released leaked, so the slot stays counted, and a
// clean one is released not leaked.
//
// spec: §7.1 (normal flow); §5.2 (pool configuration and execution modes);
// §6.2 (pod state machine)
func TestSlotRetryReleasesWithTheReclaimDisposition_spec_7_1(t *testing.T) {
	for _, leaked := range []bool{false, true} {
		sbe := slotBindErr("pod-a", "sess-1", "session_start", codes.Unavailable)
		sbe.Leaked = leaked
		binder := &fakeSlotBinder{
			results: []*podsession.BindResult{nil, {SessionID: "sess-1", SandboxName: "pod-b", SlotID: "sess-1"}},
			errs:    []error{sbe, nil},
		}
		if _, err := applySlotRetryPolicy(context.Background(), binder, slothealth.New(), slotstate.NewRegistry(), nil, nil, req("pool-x", 4)); err != nil {
			t.Fatalf("leaked=%v: expected success after one retry, got %v", leaked, err)
		}
		want := slotRelease{pod: "pod-a", slotID: "sess-1", leaked: leaked}
		if len(binder.released) != 1 || binder.released[0] != want {
			t.Errorf("released = %+v, want [%+v]", binder.released, want)
		}
	}
}

// leakRecorder captures every lenny_adapter_leaked_slots gauge publication the
// §5.2 accounting makes.
type leakRecorder struct{ sets []int }

func (l *leakRecorder) gauge(_, _ string, leaked int) { l.sets = append(l.sets, leaked) }

// accountingFixture bundles the collaborators accountSlotFailure reads, so a
// case asserts the disposition on the tracker and the registry directly
// rather than only through the drain.
type accountingFixture struct {
	binder *fakeSlotBinder
	health *slothealth.Tracker
	slots  *slotstate.Registry
	leaks  *leakRecorder
	repl   []string
	now    time.Time
}

func newAccountingFixture() *accountingFixture {
	f := &accountingFixture{
		binder: &fakeSlotBinder{},
		slots:  slotstate.NewRegistry(),
		leaks:  &leakRecorder{},
		now:    time.Unix(0, 0),
	}
	f.health = slothealth.New(slothealth.WithClock(func() time.Time { return f.now }))
	return f
}

func (f *accountingFixture) account(pod, slotID string, maxConcurrent int32, leaked bool) {
	sbe := slotBindErr(pod, slotID, "session_start", codes.Unavailable)
	accountSlotFailure(context.Background(), f.binder, f.health, f.slots,
		func(pool string) { f.repl = append(f.repl, pool) }, f.leaks.gauge,
		"pool-x", maxConcurrent, sbe, leaked)
}

// The shared §5.2 accounting books an unacknowledged compensation as a leak
// and a compensated failure in the rolling window, at maxConcurrentSessions 4
// (threshold 2). The retry path's release carries the reclaim's disposition,
// and a reclaim not acknowledged clean whose reservation release succeeded
// still takes the leaked arm: the discriminator is the reclaim disposition
// together with the release outcome, not the release outcome alone.
//
// spec: §5.2 (pool configuration and execution modes); §6.2 (pod state
// machine); §7.1 (normal flow)
func TestSlotFailureAccountingReadsTheReclaimDisposition_spec_5_2(t *testing.T) {
	t.Run("unacknowledged compensation takes the leaked arm", func(t *testing.T) {
		sbe := slotBindErr("pod-a", "sess-1", "session_start", codes.Unavailable)
		sbe.Leaked = true
		binder := &fakeSlotBinder{
			results: []*podsession.BindResult{nil, {SessionID: "sess-1", SandboxName: "pod-b", SlotID: "sess-1"}},
			errs:    []error{sbe, nil},
		}
		health := slothealth.New()
		slots := slotstate.NewRegistry()
		leaks := &leakRecorder{}
		if _, err := applySlotRetryPolicy(context.Background(), binder, health, slots, nil, leaks.gauge, req("pool-x", 4)); err != nil {
			t.Fatalf("expected success after one retry, got %v", err)
		}
		if want := (slotRelease{pod: "pod-a", slotID: "sess-1", leaked: true}); len(binder.released) != 1 || binder.released[0] != want {
			t.Errorf("released = %+v, want [%+v]", binder.released, want)
		}
		failed, leaked := health.Counts("pod-a")
		if failed != 0 || leaked != 1 {
			t.Errorf("Counts(pod-a) = (failed=%d, leaked=%d), want (0, 1): an unacknowledged reclaim is a leak", failed, leaked)
		}
		if st, ok := slots.State("sess-1"); !ok || st != slotstate.Leaked {
			t.Errorf("slot sess-1 state = %q ok=%v, want leaked", st, ok)
		}
		if len(leaks.sets) != 1 || leaks.sets[0] != 1 {
			t.Errorf("leak gauge = %v, want one publication of 1", leaks.sets)
		}
		if len(binder.drained) != 0 {
			t.Errorf("drained = %v, want none: one leak is below threshold 2", binder.drained)
		}
	})
	t.Run("compensated failure takes the windowed arm", func(t *testing.T) {
		binder := &fakeSlotBinder{
			results: []*podsession.BindResult{nil, {SessionID: "sess-1", SandboxName: "pod-b", SlotID: "sess-1"}},
			errs:    []error{slotBindErr("pod-a", "sess-1", "session_start", codes.Unavailable), nil},
		}
		health := slothealth.New()
		leaks := &leakRecorder{}
		if _, err := applySlotRetryPolicy(context.Background(), binder, health, slotstate.NewRegistry(), nil, leaks.gauge, req("pool-x", 4)); err != nil {
			t.Fatalf("expected success after one retry, got %v", err)
		}
		if want := (slotRelease{pod: "pod-a", slotID: "sess-1"}); len(binder.released) != 1 || binder.released[0] != want {
			t.Errorf("released = %+v, want [%+v]", binder.released, want)
		}
		if failed, leaked := health.Counts("pod-a"); failed != 1 || leaked != 0 {
			t.Errorf("Counts(pod-a) = (failed=%d, leaked=%d), want (1, 0)", failed, leaked)
		}
		if len(leaks.sets) != 0 {
			t.Errorf("leak gauge = %v, want no publication for a compensated failure", leaks.sets)
		}
	})
	t.Run("leaks of two distinct slots drain at threshold 2", func(t *testing.T) {
		f := newAccountingFixture()
		f.account("pod-a", "sess-1", 4, true)
		if len(f.binder.drained) != 0 {
			t.Fatalf("drained = %v after one leak, want none", f.binder.drained)
		}
		f.account("pod-a", "sess-2", 4, true)
		if len(f.binder.drained) != 1 || f.binder.drained[0] != "pod-a" {
			t.Errorf("drained = %v, want pod-a drained on two leaked slots", f.binder.drained)
		}
		if len(f.repl) != 1 || f.repl[0] != "pool-x" {
			t.Errorf("replacements = %v, want one for pool-x", f.repl)
		}
		if last := f.leaks.sets[len(f.leaks.sets)-1]; last != 0 {
			t.Errorf("last leak gauge = %d, want 0 after the drain", last)
		}
		if f.slots.LeakedCount("pod-a") != 0 {
			t.Errorf("registry leaked count = %d after drain, want 0", f.slots.LeakedCount("pod-a"))
		}
	})
	t.Run("a windowed failure ages out while a leak persists", func(t *testing.T) {
		f := newAccountingFixture()
		f.account("pod-a", "sess-1", 4, false)
		f.account("pod-b", "sess-2", 4, true)
		f.now = f.now.Add(6 * time.Minute)
		if failed, _ := f.health.Counts("pod-a"); failed != 0 {
			t.Errorf("windowed failure count after 6m = %d, want 0 (aged out)", failed)
		}
		if _, leaked := f.health.Counts("pod-b"); leaked != 1 {
			t.Errorf("leaked count after 6m = %d, want 1 (a leak persists)", leaked)
		}
	})
}

// The persistent leak record is keyed by slot. A slot the report route
// already recorded as leaked reaches the accounting again through the unclean
// compensation response; §5.2 enters it into leaked once, so at
// maxConcurrentSessions 4 the persistent count stays 1 and the pod is not
// drained.
//
// spec: §5.2 (pool configuration and execution modes); §6.2 (pod state machine)
func TestSlotFailureAccountingCountsARepeatedSlotOnce_spec_5_2(t *testing.T) {
	f := newAccountingFixture()
	f.health.RecordLeak("pod-a", "sess-1")
	f.account("pod-a", "sess-1", 4, true)
	if _, leaked := f.health.Counts("pod-a"); leaked != 1 {
		t.Errorf("persistent leaked count = %d, want 1 (one slot recorded twice)", leaked)
	}
	if len(f.binder.drained) != 0 {
		t.Errorf("drained = %v, want none: a single leaked slot is below threshold 2", f.binder.drained)
	}
}

// At maxConcurrentSessions 2 the shipped threshold is 1, so a single failure
// of either kind drains the pod. This pins the shipped threshold's behaviour
// rather than evidence of the leaked disposition.
//
// spec: §5.2 (pool configuration and execution modes); §6.2 (pod state machine)
func TestSlotFailureAccountingDrainsOnOneFailureAtTwo_spec_5_2(t *testing.T) {
	for _, leaked := range []bool{false, true} {
		f := newAccountingFixture()
		f.account("pod-a", "sess-1", 2, leaked)
		if len(f.binder.drained) != 1 || f.binder.drained[0] != "pod-a" {
			t.Errorf("leaked=%v: drained = %v, want pod-a drained at threshold 1", leaked, f.binder.drained)
		}
	}
}

// The reconnect to a slot reserved at create reaches the §5.2 accounting.
// BindReservedSlot owns the reservation release and books its outcome on
// sbe.Leaked, so an acknowledged compensation whose release succeeded takes
// the windowed arm, a release that errored takes the leaked arm, and a
// connect-stage failure (no compensation) whose own release errored takes
// the leaked arm too. At maxConcurrentSessions 4 (threshold 2).
//
// spec: §5.2 (pool configuration and execution modes); §6.2 (pod state
// machine); §7.1 (normal flow)
func TestReservedSlotBindReachesTheAccounting_spec_5_2(t *testing.T) {
	cases := []struct {
		name       string
		stage      string
		leaked     bool
		wantFailed int
		wantLeaked int
	}{
		{"acknowledged compensation, clean release", "session_start", false, 1, 0},
		{"acknowledged compensation, release errored", "session_start", true, 0, 1},
		{"connect-stage failure, release errored", "connect", true, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAccountingFixture()
			sbe := slotBindErr("pod-r", "sess-1", tc.stage, codes.Unavailable)
			sbe.Leaked = tc.leaked
			out := failReservedSlot(context.Background(), f.binder, f.health, f.slots, nil, f.leaks.gauge,
				req("pool-x", 4), fmt.Errorf("bind reserved slot: %w", sbe))
			if failed, leaked := f.health.Counts("pod-r"); failed != tc.wantFailed || leaked != tc.wantLeaked {
				t.Errorf("Counts(pod-r) = (failed=%d, leaked=%d), want (%d, %d)", failed, leaked, tc.wantFailed, tc.wantLeaked)
			}
			if got := f.slots.LeakedCount("pod-r"); got != tc.wantLeaked {
				t.Errorf("registry leaked count = %d, want %d", got, tc.wantLeaked)
			}
			// The reserved path keeps its own release: the accounting sends none.
			if len(f.binder.released) != 0 {
				t.Errorf("released = %+v, want none from the accounting", f.binder.released)
			}
			// A transient failure keeps the retryable fallback.
			var sf *podsession.SlotFailedError
			if errors.As(out, &sf) {
				t.Errorf("transient reserved-slot failure classified as the §5.2 client error: %v", out)
			}
		})
	}
}

// startedRefusal and supersededRefusal build the two §4.7.1 slot-bind
// refusals as the adapter client's translation returns them: the gateway
// sentinel and the gRPC status, both on the chain.
func startedRefusal() error {
	return fmt.Errorf("%w: %w", adapterclient.ErrSlotBindAlreadyStarted,
		status.Error(codes.FailedPrecondition, "slot bind already started"))
}

func supersededRefusal() error {
	return fmt.Errorf("%w: %w", adapterclient.ErrSlotBindAttemptSuperseded,
		status.Error(codes.Aborted, "slot bind attempt superseded"))
}

// Either §4.7.1 slot-bind refusal answers the endpoint's retryable 503
// fallback with a Retry-After at every bind stage, in place of the
// non-retryable 422 SETUP_COMMAND_FAILED or SLOT_FAILED envelope it would
// otherwise take, and files no setup_command_failed audit row or metric for a
// request that ran no setup command. A genuine non-zero setup exit, which
// carries FailedPrecondition with neither refusal, and a plain
// FailedPrecondition start failure keep their 422 envelopes: those control
// rows fail a check keyed on the gRPC code rather than on the sentinels.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.1 (REST API); §5.2 (pool
// configuration and execution modes)
func TestSlotBindRefusalAnswersRetryableFallback_spec_4_7_1(t *testing.T) {
	setupExit := func() error { return status.Error(codes.FailedPrecondition, "run setup commands: exit 1") }
	concurrentSetup := func(cause error) error {
		return classifySlotBindFailure(&podsession.SlotBindError{
			Pod: "pod-b", SlotID: "sess-1", Stage: "setup",
			Err: &podsession.SetupCommandFailure{Pod: "pod-b", Cause: cause},
		}, req("pool-x", 4))
	}
	exhaustedSuperseded := func(t *testing.T) error {
		t.Helper()
		credErr := func() error {
			return &podsession.SlotBindError{
				Pod: "pod-b", SlotID: "sess-1", Stage: "credential_assignment",
				Err: fmt.Errorf("podsession: assign slot credentials on pod %s: %w", "pod-b", supersededRefusal()),
			}
		}
		binder := &fakeSlotBinder{errs: []error{credErr(), credErr()}}
		_, err := applySlotRetryPolicy(context.Background(), binder, slothealth.New(), slotstate.NewRegistry(), nil, nil, req("pool-x", 4))
		var sf *podsession.SlotFailedError
		if !errors.As(err, &sf) || sf.Category != string(podsession.SlotReasonTransient) {
			t.Fatalf("exhausted superseded retry = %v, want a transient *SlotFailedError", err)
		}
		return err
	}
	cases := []struct {
		name      string
		err       func(t *testing.T) error
		fallback  string
		wantCode  string
		wantHTTP  int
		wantRetry bool
		// setupStage marks a row whose error carries a *SetupCommandFailure;
		// wantAudit is whether it files the setup_command_failed audit row.
		setupStage bool
		wantAudit  bool
	}{
		{
			name: "session-mode setup stage, create",
			err: func(*testing.T) error {
				return &podsession.SetupCommandFailure{Pod: "pod-b", Cause: startedRefusal()}
			},
			fallback: "SESSION_CREATION_FAILED", wantCode: "SESSION_CREATION_FAILED",
			wantHTTP: 503, wantRetry: true, setupStage: true,
		},
		{
			name: "session-mode setup stage, snapshotless resume",
			err: func(*testing.T) error {
				return &podsession.SetupCommandFailure{Pod: "pod-b", Cause: startedRefusal()}
			},
			fallback: "RESUME_FAILED", wantCode: "RESUME_FAILED",
			wantHTTP: 503, wantRetry: true, setupStage: true,
		},
		{
			name:     "concurrent-slot setup stage",
			err:      func(*testing.T) error { return concurrentSetup(startedRefusal()) },
			fallback: "STARTING_FAILED", wantCode: "STARTING_FAILED",
			wantHTTP: 503, wantRetry: true, setupStage: true,
		},
		{
			name: "concurrent-slot non-setup stage",
			err: func(*testing.T) error {
				return classifySlotBindFailure(&podsession.SlotBindError{
					Pod: "pod-b", SlotID: "sess-1", Stage: "session_start",
					Err: fmt.Errorf("start session: %w", startedRefusal()),
				}, req("pool-x", 4))
			},
			fallback: "STARTING_FAILED", wantCode: "STARTING_FAILED",
			wantHTTP: 503, wantRetry: true,
		},
		{
			name:     "concurrent-slot retry exhausted on the superseded refusal",
			err:      exhaustedSuperseded,
			fallback: "SESSION_CREATION_FAILED", wantCode: "SESSION_CREATION_FAILED",
			wantHTTP: 503, wantRetry: true,
		},
		{
			name: "control: genuine setup exit, bare",
			err: func(*testing.T) error {
				return &podsession.SetupCommandFailure{Pod: "pod-b", Cause: setupExit()}
			},
			fallback: "SESSION_CREATION_FAILED", wantCode: "SETUP_COMMAND_FAILED",
			wantHTTP: 422, setupStage: true, wantAudit: true,
		},
		{
			name:     "control: genuine setup exit, concurrent-slot setup wrap",
			err:      func(*testing.T) error { return concurrentSetup(setupExit()) },
			fallback: "STARTING_FAILED", wantCode: "SETUP_COMMAND_FAILED",
			wantHTTP: 422, setupStage: true, wantAudit: true,
		},
		{
			name: "control: plain FailedPrecondition start failure",
			err: func(*testing.T) error {
				return classifySlotBindFailure(&podsession.SlotBindError{
					Pod: "pod-b", SlotID: "sess-1", Stage: "session_start",
					Err: fmt.Errorf("start session: %w", status.Error(codes.FailedPrecondition, "workspace root mismatch")),
				}, req("pool-x", 4))
			},
			fallback: "STARTING_FAILED", wantCode: "SLOT_FAILED",
			wantHTTP: 422,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(memstore.New(), Options{})
			var sink captureSetupAudit
			var warmupFailures []string
			s.lifecycleAudit = &sink
			s.incWarmpoolWarmupFailure = func(et string) { warmupFailures = append(warmupFailures, et) }

			w := httptest.NewRecorder()
			s.writePodClaimError(w, tc.err(t), tc.fallback, "could not start the session")
			if w.Code != tc.wantHTTP {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tc.wantHTTP, w.Body.String())
			}
			body := decodeErrorBody(t, w.Body.Bytes())
			if body["code"] != tc.wantCode {
				t.Errorf("code = %v, want %s", body["code"], tc.wantCode)
			}
			if got := w.Header().Get("Retry-After") != ""; got != tc.wantRetry {
				t.Errorf("Retry-After present = %v, want %v", got, tc.wantRetry)
			}
			if !tc.setupStage {
				return
			}
			wantEvents := 0
			if tc.wantAudit {
				wantEvents = 1
			}
			if len(sink.events) != wantEvents {
				t.Errorf("setup_command_failed audit events = %d, want %d", len(sink.events), wantEvents)
			}
			if len(warmupFailures) != wantEvents {
				t.Errorf("warmup-failure increments = %v, want %d", warmupFailures, wantEvents)
			}
		})
	}
}
