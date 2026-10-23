// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// TestCheckerHoldsDeriveManifestInsideErasureReach_spec_12_8 pins the
// erasure-reach check over a derive's manifest row. The user path wires the
// checkpoint_manifests entry over every listed session and reservation, so a
// manifest row under a derive in the erasure's reach is residue whether the
// derive was admitted, refused with its reservation released, or refused
// with its reservation kept.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence)
func TestCheckerHoldsDeriveManifestInsideErasureReach_spec_12_8(t *testing.T) {
	refusal := &sessionstore.CreationRefusal{Code: "USER_ERASURE_IN_PROGRESS", Message: "erasure in progress"}
	cases := []struct {
		name    string
		outcome CreateOutcome
	}{
		{"admitted derive", CreateOutcome{CommittedSeq: 1}},
		{"refused derive whose undo left its manifest row", CreateOutcome{Err: refusal, Reserved: true, ReservedSeq: 1}},
		{"refused derive whose undo kept the reservation", CreateOutcome{Err: refusal, Reserved: true, ReservedSeq: 1, ReservationKept: true}},
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
			erasedAt := map[string]int64{o.Owner: 2}
			if msg := w.checkOutcome(ctx, o, erasedAt, map[string]bool{}); !strings.Contains(msg, "outlived its owner's erasure") {
				t.Fatalf("checkOutcome = %q, want the manifest row reported as outliving the erasure", msg)
			}
		})
	}
}

// erasedAlice builds an in-memory World with one live session per user and
// runs alice's erasure to completion.
func erasedAlice(t *testing.T) *World {
	t.Helper()
	ctx := context.Background()
	w := NewWorld(t, MemoryStores(), 1)
	e := w.NewErasure()
	j, err := e.Start(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.RunUnfaulted(ctx, j.ID, 5); err != nil {
		t.Fatal(err)
	}
	return w
}

// TestUserErasureErasesTokenStoreRowsAndSourceCheckpoint_spec_12_8 runs a
// user erasure over the harness and asserts it follows the §12.8 DeleteByUser
// dependency-ordered deletion sequence: the erased user holds no TokenStore row and no seeded
// source checkpoint, object, transcript, or session row, and a bystander
// keeps every TokenStore row, lease, and seeded source it held.
//
// spec: 12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence), 12.8 (User-level erasure invocation)
func TestUserErasureErasesTokenStoreRowsAndSourceCheckpoint_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w := erasedAlice(t)
	src := w.sources["alice"]
	if n := w.Tokens.CountUser(w.Tenant, "alice"); n != 0 {
		t.Errorf("alice holds %d TokenStore rows after her erasure, want 0", n)
	}
	if n := w.Manifests.SessionTotal(w.Tenant, src); n != 0 {
		t.Errorf("alice's source holds %d checkpoint manifests after her erasure, want 0", n)
	}
	if n := w.Tokens.CountUser(w.Tenant, "bob"); n != tokensPerUser {
		t.Errorf("bob holds %d TokenStore rows after alice's erasure, want %d", n, tokensPerUser)
	}
	if n := w.Manifests.SessionTotal(w.Tenant, w.sources["bob"]); n != 1 {
		t.Errorf("bob's source holds %d checkpoint manifests after alice's erasure, want 1", n)
	}
	if got := w.erasedUserResidue(ctx, "alice"); got != "" {
		t.Errorf("erasedUserResidue(alice) = %q, want none", got)
	}
	w.Check(t)
}

// TestCheckerCatchesErasedUserAndBystanderResidue_spec_12_8 plants a defect
// on each side of the user half of the residue invariant and asserts Check
// reports it: a TokenStore row of the erased user, a manifest of the erased
// user's source, a TokenStore row the bystander lost, and a lease the
// bystander lost.
//
// spec: 12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence), 11.4 (User Invalidation)
func TestCheckerCatchesErasedUserAndBystanderResidue_spec_12_8(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		plant func(w *World)
		want  string
	}{
		{"erased user token", func(w *World) { w.Tokens.Seed(w.Tenant, "alice", 1) }, "user alice"},
		{"erased user manifest", func(w *World) {
			w.Manifests.seedParent(partialmanifeststore.Record{TenantID: w.Tenant, CheckpointID: uuid.NewString(), SessionID: w.sources["alice"]})
		}, "user alice"},
		{"bystander token", func(w *World) { _, _ = w.Tokens.DeleteByUser(ctx, w.Tenant, "bob") }, "bystander bob"},
		{"bystander lease", func(w *World) { w.Leases.RevokeUserLeases(w.Tenant, "bob", w.SeededLive("bob")) }, "bystander bob"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := erasedAlice(t)
			tc.plant(w)
			rec := &recordingTB{TB: t}
			w.Check(rec)
			if len(rec.errs) != 1 || !strings.Contains(rec.errs[0], tc.want) {
				t.Fatalf("Check errors = %v, want one naming %q", rec.errs, tc.want)
			}
		})
	}
}
