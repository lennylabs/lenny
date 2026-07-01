// SPDX-License-Identifier: MIT

package podsession

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SlotFailureReason classifies a §5.2 concurrent-workspace slot failure
// for the slot retry policy. The three categories the spec names as
// non-retryable are returned to the client immediately without a retry;
// any other reason is transient and eligible for one retry on a fresh
// slot.
//
// spec: §5.2 "Concurrent-workspace slot retry policy" — non-retryable
// failure categories (oom, workspace_validation, policy_rejection).
type SlotFailureReason string

const (
	// SlotReasonOOM is a pod-level OOM kill: the same input is likely to
	// OOM again on an identically-sized slot, so it is not retried.
	SlotReasonOOM SlotFailureReason = "oom"
	// SlotReasonWorkspaceValidation is a structurally-invalid workspace
	// plan that will fail on any slot, so it is not retried.
	SlotReasonWorkspaceValidation SlotFailureReason = "workspace_validation"
	// SlotReasonPolicyRejection is a task rejected by admission policy,
	// which will be rejected identically on any slot.
	SlotReasonPolicyRejection SlotFailureReason = "policy_rejection"
	// SlotReasonTransient is any other slot failure (transient adapter
	// error, dial failure, runtime hiccup). The §5.2 policy retries it
	// once on a fresh slot.
	SlotReasonTransient SlotFailureReason = "transient"
)

// NonRetryable reports whether the reason is one of the §5.2 categories
// returned to the client immediately without a retry.
func (r SlotFailureReason) NonRetryable() bool {
	switch r {
	case SlotReasonOOM, SlotReasonWorkspaceValidation, SlotReasonPolicyRejection:
		return true
	default:
		return false
	}
}

// SlotBindError wraps a §5.2 concurrent-workspace slot failure that
// occurred after a slot was reserved on a pod. It carries the pod
// (SandboxName) and the SlotID the reservation belongs to so the gateway
// can release the failed slot, record it against the pod's rolling
// fail/leak window, and decide whether to retry. Err is the underlying
// stage error; SlotBindError unwraps to it so the existing typed-error
// handlers (SetupCommandFailure, CredentialAssignmentError) still match
// through the chain.
type SlotBindError struct {
	// Pod is the Sandbox name the slot was reserved on.
	Pod string
	// SlotID is the failed slot's identifier (the session id).
	SlotID string
	// Stage names the bind stage that failed (the lenny_slot_failure_total
	// error_type label), used to refine the reason classification.
	Stage string
	// Err is the underlying adapter/stage error.
	Err error
}

func (e *SlotBindError) Error() string {
	return fmt.Sprintf("podsession: slot %s on pod %s failed at %s: %v", e.SlotID, e.Pod, e.Stage, e.Err)
}

func (e *SlotBindError) Unwrap() error { return e.Err }

// Reason classifies the failure for the retry policy. A workspace-stage
// failure carrying a gRPC InvalidArgument is a structurally-invalid plan
// (workspace_validation); a ResourceExhausted is an OOM/resource cap; a
// PermissionDenied is a policy rejection. Everything else is transient and
// retried once. The classifier defaults to transient because a needless
// retry costs only one extra attempt, whereas misclassifying a transient
// failure as non-retryable would skip a valid retry.
//
// spec: §5.2 non-retryable categories.
func (e *SlotBindError) Reason() SlotFailureReason {
	switch slotErrCode(e.Err) {
	case codes.InvalidArgument:
		return SlotReasonWorkspaceValidation
	case codes.ResourceExhausted:
		return SlotReasonOOM
	case codes.PermissionDenied, codes.FailedPrecondition:
		// A FailedPrecondition outside the workspace stages is a policy or
		// state rejection. The workspace stages, however, surface ordinary
		// materialization failures as FailedPrecondition; those stay
		// transient so a fresh-workspace retry can clear a flaky upload.
		if e.Stage == slotFailureWorkspacePrep {
			return SlotReasonTransient
		}
		return SlotReasonPolicyRejection
	default:
		return SlotReasonTransient
	}
}

// slotErrCode walks err's chain for a gRPC status and returns its code, or
// codes.Unknown when err carries no status. It uses errors.As so a status
// wrapped with fmt.Errorf("...: %w", st) is still found.
func slotErrCode(err error) codes.Code {
	type grpcStatuser interface{ GRPCStatus() *status.Status }
	var s grpcStatuser
	if errors.As(err, &s) {
		return s.GRPCStatus().Code()
	}
	return codes.Unknown
}

// SlotFailedError is the §5.2 structured client error returned when a
// concurrent-workspace slot failure is not (or no longer) retried: either
// the failure reason is non-retryable or the single retry was exhausted.
// Category is the failure reason (error.category), Retryable is always
// false from the platform's perspective (the client may resubmit a new
// request), and SlotID identifies the failed slot (error.slotId).
//
// spec: §5.2 "Client error on exhaustion".
type SlotFailedError struct {
	Category string
	SlotID   string
	Pool     string
	Err      error
}

func (e *SlotFailedError) Error() string {
	return fmt.Sprintf("podsession: concurrent slot %s failed (%s) with no further retry: %v",
		e.SlotID, e.Category, e.Err)
}

func (e *SlotFailedError) Unwrap() error { return e.Err }
