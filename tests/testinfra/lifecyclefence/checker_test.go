// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// TestCheckerHoldsRefusedDeriveManifestInsideErasureReach_spec_12_8 pins the
// manifest-row exemption of the erasure-reach check to the derives whose
// manifest row only the erasure could delete. A derive the fence refused
// after its reservation, whose undo released the reservation, must have
// deleted its manifest row even when the owner's erasure began after the
// reservation committed; an admitted derive and a derive whose undo kept the
// reservation stay exempt, because the checkpoint-manifest eraser is not
// wired to user erasure.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.8 (User-level erasure invocation)
func TestCheckerHoldsRefusedDeriveManifestInsideErasureReach_spec_12_8(t *testing.T) {
	refusal := &sessionstore.CreationRefusal{Code: "USER_ERASURE_IN_PROGRESS", Message: "erasure in progress"}
	cases := []struct {
		name      string
		outcome   CreateOutcome
		wantFault bool
	}{
		{
			name:      "refused derive whose undo left its manifest row",
			outcome:   CreateOutcome{Err: refusal, Reserved: true, ReservedSeq: 1},
			wantFault: true,
		},
		{
			name:    "refused derive whose undo kept the reservation",
			outcome: CreateOutcome{Err: refusal, Reserved: true, ReservedSeq: 1, ReservationKept: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWorld(t, MemoryStores(), 0)
			ctx := context.Background()
			o := tc.outcome
			o.ID, o.Path, o.Owner = uuid.NewString(), PathDerive, "alice"
			if err := w.Manifests.Put(ctx, partialmanifeststore.Record{
				TenantID: w.Tenant, CheckpointID: uuid.NewString(), SessionID: o.ID,
				ChunkEncoding: partialmanifeststore.ChunkEncodingTar, ChunkObjectKeyPrefix: "/" + w.Tenant + "/checkpoints/" + o.ID + "/",
			}); err != nil {
				t.Fatalf("write manifest row: %v", err)
			}
			// The owner's erasure began after the reservation committed and
			// has since removed it, so no reservation is listed.
			reserved := map[string]bool{}
			erasedAt := map[string]int64{o.Owner: 2}
			msg := w.checkOutcome(ctx, o, erasedAt, reserved)
			if got := msg != ""; got != tc.wantFault {
				t.Fatalf("checkOutcome = %q, want a fault %v", msg, tc.wantFault)
			}
		})
	}
}

// TestManifestOutsideErasureOnlyForRowsTheErasureMustDelete_spec_12_8 pins
// which outcomes carry a manifest row outside the erasure's reach.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestManifestOutsideErasureOnlyForRowsTheErasureMustDelete_spec_12_8(t *testing.T) {
	refusal := &sessionstore.CreationRefusal{Code: "TENANT_SUSPENDED"}
	cases := []struct {
		name string
		o    CreateOutcome
		want bool
	}{
		{"admitted", CreateOutcome{}, true},
		{"refused, undo released the reservation", CreateOutcome{Err: refusal}, false},
		{"refused, undo kept the reservation", CreateOutcome{Err: refusal, ReservationKept: true}, true},
	}
	for _, tc := range cases {
		if got := manifestOutsideErasure(tc.o); got != tc.want {
			t.Errorf("%s: manifestOutsideErasure = %v, want %v", tc.name, got, tc.want)
		}
	}
}
