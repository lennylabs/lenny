// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// fullJob returns an in-progress job with every field set to a non-zero
// value. OverrideReceipt holds JSON-native values, the form any persisted
// job returns.
func fullJob() erasurejob.Job {
	at := jobAt
	return erasurejob.Job{
		ID: "erasure_full", TenantID: "acme", UserID: "alice",
		Phase:              erasurejob.PhasePseudonymizing,
		StartedAt:          at,
		Deadline:           time.Hour,
		CompletedAt:        at.Add(2 * time.Minute),
		Deleted:            map[string]int{"sessions": 3, "memory": 1},
		Total:              4,
		Billing:            erasurejob.BillingErasureOutcome{Disposition: "pseudonymized", Pseudonymized: 5, Verified: true},
		DeadLetterRedacted: 2,
		PhaseLog: []erasurejob.PhaseTransition{
			{Phase: erasurejob.PhaseInitiated, At: at},
			{Phase: erasurejob.PhaseStoreDeleting, At: at.Add(time.Second)},
			{Phase: erasurejob.PhasePseudonymizing, At: at.Add(2 * time.Second)},
		},
		Failure:     "earlier failure",
		InitiatedBy: "bob@acme.com",
		OverrideReceipt: map[string]any{
			"legalHoldOverride": true,
			"overrideBy":        "carol@acme.com",
			"holds":             []any{map[string]any{"resourceType": "session", "resourceId": "s1"}},
		},
		ErasedAt:  at.Add(3 * time.Second),
		ReceiptAt: at.Add(4 * time.Second),
	}
}

// jobsEqual compares two jobs with times compared through Equal.
func jobsEqual(t *testing.T, got, want erasurejob.Job) {
	t.Helper()
	for name, pair := range map[string][2]time.Time{
		"StartedAt": {got.StartedAt, want.StartedAt}, "CompletedAt": {got.CompletedAt, want.CompletedAt},
		"ErasedAt": {got.ErasedAt, want.ErasedAt}, "ReceiptAt": {got.ReceiptAt, want.ReceiptAt},
	} {
		if !pair[0].Equal(pair[1]) {
			t.Errorf("%s = %v, want %v", name, pair[0], pair[1])
		}
	}
	if len(got.PhaseLog) != len(want.PhaseLog) {
		t.Fatalf("PhaseLog = %v, want %v", got.PhaseLog, want.PhaseLog)
	}
	for i := range got.PhaseLog {
		if got.PhaseLog[i].Phase != want.PhaseLog[i].Phase || !got.PhaseLog[i].At.Equal(want.PhaseLog[i].At) {
			t.Errorf("PhaseLog[%d] = %+v, want %+v", i, got.PhaseLog[i], want.PhaseLog[i])
		}
	}
	g, w := got, want
	g.StartedAt, g.CompletedAt, g.ErasedAt, g.ReceiptAt, g.PhaseLog = time.Time{}, time.Time{}, time.Time{}, time.Time{}, nil
	w.StartedAt, w.CompletedAt, w.ErasedAt, w.ReceiptAt, w.PhaseLog = time.Time{}, time.Time{}, time.Time{}, time.Time{}, nil
	if !reflect.DeepEqual(g, w) {
		gj, _ := json.Marshal(g)
		wj, _ := json.Marshal(w)
		t.Errorf("job = %s\nwant  %s", gj, wj)
	}
}

// TestMemorySnapshotRoundTripsEveryField_spec_17_4 pins the Source Mode
// snapshot of the erasure job store: an in-progress job with every field
// set and a completed job read back equal through Get and ListActive after
// ExportState and ImportState into a fresh store, and an empty snapshot
// resets the store.
// spec: 17.4 (Local Development Mode: Source Mode), 12.8 (Compliance Interfaces: Erasure job phase field (crash recovery))
func TestMemorySnapshotRoundTripsEveryField_spec_17_4(t *testing.T) {
	ctx := context.Background()
	src, users := newJobStoreUsers(t)
	full := fullJob()
	startJob(t, src, full)
	done := startJob(t, src, newJob("erasure_done", "acme", "bob"))
	if _, err := src.Complete(ctx, done.ID, jobAt.Add(time.Hour), done); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	completed, _ := src.Get(ctx, erasurejob.AllTenants, done.ID)

	data, err := src.ExportState()
	if err != nil {
		t.Fatalf("ExportState: %v", err)
	}
	dst := erasurejob.NewMemory(erasurejob.WithRestrictionWriter(users))
	if err := dst.ImportState(data); err != nil {
		t.Fatalf("ImportState: %v", err)
	}
	got, err := dst.Get(ctx, erasurejob.AllTenants, full.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	jobsEqual(t, got, full)
	gotDone, err := dst.Get(ctx, "acme", done.ID)
	if err != nil {
		t.Fatalf("Get completed: %v", err)
	}
	jobsEqual(t, gotDone, completed)
	active, err := dst.ListActive(ctx)
	if err != nil || len(active) != 1 {
		t.Fatalf("ListActive = %v (%v), want the in-progress job only", active, err)
	}
	jobsEqual(t, active[0], full)

	if err := dst.ImportState(nil); err != nil {
		t.Fatalf("ImportState(nil): %v", err)
	}
	if active, _ := dst.ListActive(ctx); len(active) != 0 {
		t.Fatalf("an empty snapshot left %d jobs", len(active))
	}
	if err := dst.ImportState([]byte("{not json")); err == nil {
		t.Fatal("ImportState accepted a corrupt snapshot")
	}
}
