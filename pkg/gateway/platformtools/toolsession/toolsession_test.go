// SPDX-License-Identifier: MIT

package toolsession

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// TestRecordCarriesTheRowAcrossTheSeam checks that the record of a row
// carries the row with every field, and that a record carrying no row is
// read as the row its fields describe.
// spec: GWSEAM-AfterTerminalWrite
func TestRecordCarriesTheRowAcrossTheSeam(t *testing.T) {
	row := sessionstore.Session{ID: "s1", TenantID: "acme", UserID: "alice", State: session.StateRunning, PoolRef: "p", PodAssignment: "pod-1", LastSeq: 3}
	rec := Record(row)
	if rec.ID != "s1" || rec.Pool != "p" || rec.LastSeq != 3 {
		t.Fatalf("record = %+v", rec)
	}
	if got := Row(rec); got.PodAssignment != "pod-1" {
		t.Fatalf("row lost its pod assignment: %+v", got)
	}
	got := Row(seam.Session{ID: "s2", TenantID: "acme", Pool: "q", LastSeq: 4, State: "suspended"})
	if got.ID != "s2" || got.PoolRef != "q" || got.LastSeq != 4 || got.State != session.StateSuspended || got.PodAssignment != "" {
		t.Fatalf("row of a bare record = %+v", got)
	}
}
