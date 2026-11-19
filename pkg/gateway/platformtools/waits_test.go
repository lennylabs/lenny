// SPDX-License-Identifier: MIT

package platformtools_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// TestInputWaitsListResolveAndForget checks that the session core sees a
// session's open input requests, answers one, and forgets the rest through
// the registry the platform tools open them in.
//
// spec: GWSEAM-PendingInputWaits, GWSEAM-ResolveInputWait, GWSEAM-ForgetInputWaits
func TestInputWaitsListResolveAndForget(t *testing.T) {
	reg := inputwait.NewRegistry()
	w := platformtools.NewInputWaits(reg)
	ch, err := reg.Register("s1", "r1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Register("s1", "r2", nil); err != nil {
		t.Fatal(err)
	}
	if got := w.PendingInputWaits("s1"); len(got) != 2 {
		t.Fatalf("pending = %v, want two", got)
	}
	if err := w.ResolveInputWait("s1", "r1", "yes"); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-ch:
		if answer != "yes" {
			t.Errorf("answer = %q", answer)
		}
	case <-time.After(time.Second):
		t.Fatal("the answer did not reach the waiter")
	}
	if err := w.ResolveInputWait("s1", "absent", "x"); err == nil {
		t.Error("answering an unknown request succeeded")
	}
	w.ForgetInputWaits("s1")
	if reg.Consumed("s1") != 0 {
		t.Errorf("consumed after forget = %d, want 0", reg.Consumed("s1"))
	}
}

// TestToolApprovalsDeliverADecisionAndCloseOnCancel checks that a decision
// delivered on an approval wait reaches the registered channel with its
// reason, and that a withdrawn wait closes it.
//
// spec: GWSEAM-RegisterApproval, GWSEAM-ResolveApproval, GWSEAM-CancelApproval
func TestToolApprovalsDeliverADecisionAndCloseOnCancel(t *testing.T) {
	a := platformtools.NewToolApprovals(toolapproval.NewRegistry())
	ch, err := a.RegisterApproval("s1", "call-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RegisterApproval("s1", "call-1"); err == nil {
		t.Error("a second wait on the same call registered")
	}
	if err := a.ResolveApproval("s1", "call-1", seam.ApprovalDecision{Approved: false, Reason: "nope"}); err != nil {
		t.Fatal(err)
	}
	select {
	case d, ok := <-ch:
		if !ok || d.Approved || d.Reason != "nope" {
			t.Errorf("decision = %+v ok=%t, want a denial with its reason", d, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("the decision did not arrive")
	}
	if err := a.ResolveApproval("s1", "call-1", seam.ApprovalDecision{Approved: true}); err == nil {
		t.Error("resolving a settled wait succeeded")
	}

	ch2, err := a.RegisterApproval("s1", "call-2")
	if err != nil {
		t.Fatal(err)
	}
	a.CancelApproval("s1", "call-2")
	select {
	case _, ok := <-ch2:
		if ok {
			t.Error("a withdrawn wait delivered a decision")
		}
	case <-time.After(time.Second):
		t.Fatal("a withdrawn wait's channel did not close")
	}
}
