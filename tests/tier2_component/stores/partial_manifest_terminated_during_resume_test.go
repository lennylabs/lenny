//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4.2 / §10.1 partial-checkpoint manifest row a
// mid-resume terminal collapse (resuming → cancelled / resuming →
// completed) must leave behind. Per §4.2 the aborted snapshot replay
// writes a durable manifest row carrying both generation counters plus
// `manifest_reason = 'terminated_during_resume'`, so audit
// reconstruction can distinguish an intentional mid-resume
// cancel/complete from a lost mid-flight.
//
// This test is currently skipped: the producer that writes the row on
// the snapshot-close path is unbuilt, and the store schema does not yet
// carry the `manifest_reason`, `coordination_generation`, or
// `recovery_generation` columns (migration 0062 lands only the §4.4
// cleanup-path subset, and partialmanifeststore.Record has no reason or
// generation-tuple fields). Both belong to the deferred §10.1
// partial-upload pipeline. The skip lifts when that producer and the
// schema columns land.
package stores_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	partialmanifestpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore/pgstore"
)

// spec: §4.2 (Session Manager) — "The partial-checkpoint manifest row
// written by the aborted replay carries both counter values plus
// `manifest_reason = 'terminated_during_resume'` per §10.1
// partial-manifest schema, so audit reconstruction has a consistent
// generation tuple at every terminal transition and crash-recovery can
// distinguish an intentional mid-resume cancel/complete from a lost
// mid-flight."
// spec: §10.1 — `manifest_reason` enum value `terminated_during_resume`
// records "the partial row was written by an aborted snapshot replay
// whose session transitioned resuming → cancelled or resuming →
// completed"; the row also carries `coordination_generation` and
// `recovery_generation`.
// diagnosis: a failure means a mid-resume terminal collapse does not
// leave a durable partial-checkpoint manifest row tagged
// `terminated_during_resume` with its (coordination_generation,
// recovery_generation) tuple, so audit reconstruction cannot tell an
// intentional mid-resume cancel/complete apart from a lost mid-flight.
func TestPartialManifestTerminatedDuringResume(t *testing.T) {
	t.Skip("resuming→terminal snapshot-close manifest producer and the manifest_reason / generation-tuple columns are unbuilt (deferred §10.1 partial-upload pipeline); see the open TEST-GAPS.md finding for §4.2 partial-checkpoint terminated_during_resume")

	t.Parallel()
	_, pg := startStore(t)
	store := partialmanifestpg.New(pg.Pool, nil)
	ctx := context.Background()

	// Once the snapshot-close producer lands, this test drives a
	// resuming → cancelled collapse through the sessionserver against
	// the Postgres-backed store and asserts that the resulting
	// partial-checkpoint manifest row for the session carries
	// manifest_reason = 'terminated_during_resume' together with the
	// (coordination_generation, recovery_generation) tuple frozen at
	// the terminal write. The scaffold below pins the surface that
	// exists today so the harness compiles; the reason/generation
	// assertions attach when partialmanifeststore.Record and migration
	// 0062 gain those fields.
	tenant := freshTenant(t, ctx, pg)
	sessID := newUUID(t)
	if _, err := store.LatestActive(ctx, tenant, sessID); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Fatalf("LatestActive on a fresh session: got %v, want ErrNotFound", err)
	}
}
