// SPDX-License-Identifier: MIT

package platformtools

import (
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/toolapproval"
)

// InputWaits serves the registry of the input requests a session's runtime
// opens through lenny/request_input.
type InputWaits struct {
	r *inputwait.Registry
}

// NewInputWaits returns the input request operations over r, or nil when r
// is nil, so the session core resolves and forgets no input request
// without a registry.
func NewInputWaits(r *inputwait.Registry) seam.InputWaits {
	if r == nil {
		return nil
	}
	return InputWaits{r: r}
}

// PendingInputWaits returns the identifiers of a session's open input
// requests.
//
// spec: GWSEAM-PendingInputWaits
func (w InputWaits) PendingInputWaits(sessionID string) []string {
	return w.r.PendingForSession(sessionID)
}

// ResolveInputWait answers one open input request.
//
// spec: GWSEAM-ResolveInputWait
func (w InputWaits) ResolveInputWait(sessionID, requestID, answer string) error {
	return w.r.Resolve(sessionID, requestID, answer)
}

// ForgetInputWaits discards every open input request of a session.
//
// spec: GWSEAM-ForgetInputWaits
func (w InputWaits) ForgetInputWaits(sessionID string) {
	w.r.ForgetSession(sessionID)
}

// ToolApprovals serves the registry of the waits for the approval of a
// tool call.
type ToolApprovals struct {
	r *toolapproval.Registry
}

// NewToolApprovals returns the approval wait operations over r, or nil when
// r is nil.
func NewToolApprovals(r *toolapproval.Registry) seam.ToolApprovals {
	if r == nil {
		return nil
	}
	return ToolApprovals{r: r}
}

// RegisterApproval opens an approval wait. The returned channel closes
// when the wait is withdrawn.
//
// spec: GWSEAM-RegisterApproval
func (a ToolApprovals) RegisterApproval(sessionID, toolCallID string) (<-chan seam.ApprovalDecision, error) {
	in, err := a.r.Register(sessionID, toolCallID)
	if err != nil {
		return nil, err
	}
	out := make(chan seam.ApprovalDecision, 1)
	go func() {
		defer close(out)
		if d, ok := <-in; ok {
			out <- seam.ApprovalDecision{Approved: d.Approved, Reason: d.Reason}
		}
	}()
	return out, nil
}

// CancelApproval withdraws an approval wait.
//
// spec: GWSEAM-CancelApproval
func (a ToolApprovals) CancelApproval(sessionID, toolCallID string) {
	a.r.Cancel(sessionID, toolCallID)
}

// ResolveApproval delivers a decision on an approval wait.
//
// spec: GWSEAM-ResolveApproval
func (a ToolApprovals) ResolveApproval(sessionID, toolCallID string, d seam.ApprovalDecision) error {
	return a.r.Resolve(sessionID, toolCallID, toolapproval.Decision{Approved: d.Approved, Reason: d.Reason})
}
