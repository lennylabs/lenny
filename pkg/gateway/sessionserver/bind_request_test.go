// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// spec: §7.1 (Derive restore semantics) — both composers carry the restore
// closure for a derived or replayed row and leave it nil for a row without
// a starting snapshot. A composer that drops the closure starts the derived
// session with only the plan's sources.
//
// diagnosis: a nil RestoreChunks on the derived row means the composer is
// not wired to workspaceRestore, so the bind promotes without the snapshot.
func TestBindRequestComposersCarryRestore_spec_7_1(t *testing.T) {
	ctx := context.Background()
	res := &keyRecordingResolver{grants: []adapterclient.ChunkGrant{{Index: 0, URL: "u0"}}}
	s := &Server{resumeChunkResolver: res}
	derived := derivedRow("acme", "sess_derived", "ck_derived")
	ordinary := sessionstore.Session{ID: "sess_plain", TenantID: "acme"}

	composers := map[string]func(sessionstore.Session) func(context.Context) ([]adapterclient.ChunkGrant, error){
		"exclusive": func(row sessionstore.Session) func(context.Context) ([]adapterclient.ChunkGrant, error) {
			return s.exclusiveBindRequest(ctx, row, podsession.PoolMatch{}, workspaceplan.Plan{}, nil, nil, nil, "").RestoreChunks
		},
		"slot": func(row sessionstore.Session) func(context.Context) ([]adapterclient.ChunkGrant, error) {
			return s.slotBindRequest(ctx, row, podsession.PoolMatch{}, workspaceplan.Plan{}, nil, nil, nil, "").RestoreChunks
		},
	}
	for name, compose := range composers {
		t.Run(name, func(t *testing.T) {
			if compose(ordinary) != nil {
				t.Fatal("RestoreChunks set for a row without a starting snapshot")
			}
			restore := compose(derived)
			if restore == nil {
				t.Fatal("RestoreChunks nil for a derived row")
			}
			before := res.calls
			grants, err := restore(ctx)
			if err != nil {
				t.Fatalf("restore: %v", err)
			}
			if res.calls != before+1 || res.session != "sess_derived" || len(grants) != 1 {
				t.Fatalf("restore resolved session %q (%d calls), grants %d; want the derived row resolved once with one grant", res.session, res.calls-before, len(grants))
			}
		})
	}
}
