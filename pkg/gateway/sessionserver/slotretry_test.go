// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/slothealth"
)

// fakeSlotBinder records the calls applySlotRetryPolicy makes and replays a
// scripted sequence of BindSlot outcomes (one per attempt).
type fakeSlotBinder struct {
	results  []*podsession.BindResult
	errs     []error
	bindCall int

	released [][2]string // (pod, slotID)
	drained  []string
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

func (f *fakeSlotBinder) ReleaseSlotReservation(_ context.Context, pod, slotID string) error {
	f.released = append(f.released, [2]string{pod, slotID})
	return nil
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

func req(pool string, maxConcurrent int32) podsession.SlotBindRequest {
	return podsession.SlotBindRequest{Pool: pool, SessionID: "sess-1", MaxConcurrent: maxConcurrent}
}

// spec: §5.2 — a transient slot failure is retried once on a fresh slot;
// the retry succeeding yields the bound result and no client error.
func TestSlotRetryTransientThenSuccess_spec_5_2(t *testing.T) {
	binder := &fakeSlotBinder{
		results: []*podsession.BindResult{nil, {SessionID: "sess-1", SandboxName: "pod-a", SlotID: "sess-1"}},
		errs:    []error{slotBindErr("pod-a", "sess-1", "session_start", codes.Unavailable), nil},
	}
	health := slothealth.New()
	res, err := applySlotRetryPolicy(context.Background(), binder, health, nil, req("pool-x", 4))
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
	if len(binder.released) != 1 || binder.released[0] != [2]string{"pod-a", "sess-1"} {
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
	_, err := applySlotRetryPolicy(context.Background(), binder, health, nil, req("pool-x", 4))
	var sf *podsession.SlotFailedError
	if !errors.As(err, &sf) {
		t.Fatalf("expected *SlotFailedError, got %v", err)
	}
	if sf.Category != string(podsession.SlotReasonWorkspaceValidation) {
		t.Errorf("category = %q, want workspace_validation", sf.Category)
	}
	if sf.SlotID != "sess-1" {
		t.Errorf("slotId = %q, want sess-1", sf.SlotID)
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
	_, err := applySlotRetryPolicy(context.Background(), binder, health, nil, req("pool-x", 8))
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

// spec: §5.2 / §6.2 line 165 — when a pod crosses the ceil(maxConcurrent/2)
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

	_, err := applySlotRetryPolicy(context.Background(), binder, health, repl, req("pool-x", 2))
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

// spec: §5.2 line 519 — a reservation-exhaustion sentinel is not a slot
// failure: it is returned unchanged (no release, no record, no retry) so
// the handler maps it to WARM_POOL_EXHAUSTED.
func TestSlotRetryPassesThroughExhaustionSentinel_spec_5_2(t *testing.T) {
	binder := &fakeSlotBinder{errs: []error{podclaim.ErrNoConcurrentSlot}}
	health := slothealth.New()
	_, err := applySlotRetryPolicy(context.Background(), binder, health, nil, req("pool-x", 4))
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
