// SPDX-License-Identifier: MIT

package platformtools_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/platformtools"
)

type fakeDenials struct{ got []string }

func (f *fakeDenials) ClearSubtreeDenial(_ context.Context, tenantID, root, _ string) error {
	f.got = append(f.got, tenantID+"/"+root)
	return nil
}

type fakeFloor string

func (f fakeFloor) Floor() string { return string(f) }

// TestDelegationAdminServesTheAPIFrontsDelegationOperations checks that
// the platform tools seam clears a subtree's denial through its budget
// source and reads the elicitation floor through its provider, answers
// ErrDelegationAdminUnwired for a clear with no budget source, and reads
// the empty floor with no provider.
//
// spec: GWSEAM-ClearSubtreeDenial, GWSEAM-ElicitationFloor
func TestDelegationAdminServesTheAPIFrontsDelegationOperations(t *testing.T) {
	denials := &fakeDenials{}
	d := platformtools.DelegationAdmin{Denials: denials, Floors: fakeFloor("required")}
	if err := d.ClearSubtreeDenial(context.Background(), "acme", "root-1", "sess-1"); err != nil {
		t.Fatalf("ClearSubtreeDenial: %v", err)
	}
	if len(denials.got) != 1 || denials.got[0] != "acme/root-1" {
		t.Errorf("cleared = %v, want acme/root-1", denials.got)
	}
	if got := d.ElicitationFloor(); got != "required" {
		t.Errorf("ElicitationFloor = %q, want required", got)
	}

	var unwired platformtools.DelegationAdmin
	if err := unwired.ClearSubtreeDenial(context.Background(), "acme", "root-1", "sess-1"); !errors.Is(err, platformtools.ErrDelegationAdminUnwired) {
		t.Errorf("unwired ClearSubtreeDenial = %v, want ErrDelegationAdminUnwired", err)
	}
	if got := unwired.ElicitationFloor(); got != "" {
		t.Errorf("unwired ElicitationFloor = %q, want empty", got)
	}
}
