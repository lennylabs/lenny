// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// fakeReleaser is an in-memory admin.EscrowReleaser recording the release
// calls and returning a configured count.
type fakeReleaser struct {
	sessionReleases  int
	artifactReleases int
	calls            []string
}

func (f *fakeReleaser) ReleaseForSession(_ context.Context, tenantID, sessionID, by string) (int, error) {
	f.calls = append(f.calls, "session:"+tenantID+":"+sessionID+":"+by)
	return f.sessionReleases, nil
}

func (f *fakeReleaser) ReleaseForArtifact(_ context.Context, tenantID, artifactURI, by string) (int, error) {
	f.calls = append(f.calls, "artifact:"+tenantID+":"+artifactURI+":"+by)
	return f.artifactReleases, nil
}

// spec: §12.8 line 884 — clearing a live session hold runs the escrow-GC
// release and reports the count of released objects.
func TestClearSessionHold_releasesEscrow_spec_12_8_line884(t *testing.T) {
	router, sessions, _ := newLegalHoldAdmin(t)
	rel := &fakeReleaser{sessionReleases: 2}
	router = router.WithEscrowReleaser(rel)
	seedSession(t, sessions, sessionstore.Session{ID: "sess_1", TenantID: "acme", UserID: "alice@acme.com", LegalHold: true})

	rr := setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", SessionID: "sess_1", Hold: false}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear hold: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["escrowReleased"] != float64(2) {
		t.Errorf("escrowReleased = %v, want 2", resp["escrowReleased"])
	}
	if len(rel.calls) != 1 || rel.calls[0] != "session:acme:sess_1:admin@acme.com" {
		t.Errorf("releaser calls = %v, want one session:acme:sess_1:<admin>", rel.calls)
	}
	got, _ := sessions.Get(context.Background(), "acme", "sess_1")
	if got.LegalHold {
		t.Error("clear must also lift the live session hold flag")
	}
}

// spec: §12.8 line 884 — the clear is accepted on a tombstoned tenant
// (the session row is gone) for the express purpose of releasing escrow.
func TestClearSessionHold_tombstonedTenant_releasesEscrow(t *testing.T) {
	router, _, _ := newLegalHoldAdmin(t)
	rel := &fakeReleaser{sessionReleases: 1}
	router = router.WithEscrowReleaser(rel)

	// No session seeded: the Update returns ErrNotFound (Phase 4 deleted it).
	rr := setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", SessionID: "gone", Hold: false}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("tombstoned clear: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["escrowReleased"] != float64(1) {
		t.Errorf("escrowReleased = %v, want 1", resp["escrowReleased"])
	}
}

// A tombstoned-tenant clear with no escrow records (or no releaser wired)
// is a 404 — there is nothing to clear and nothing to release.
func TestClearSessionHold_tombstonedTenant_noEscrow_is404(t *testing.T) {
	router, _, _ := newLegalHoldAdmin(t)
	router = router.WithEscrowReleaser(&fakeReleaser{sessionReleases: 0})
	rr := setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", SessionID: "gone", Hold: false}, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("tombstoned clear with no escrow: status %d, want 404", rr.Code)
	}
}

// A clear with no escrow records still lifts the flag and omits the
// escrowReleased field.
func TestClearSessionHold_noEscrowRecords_clearsFlagOnly(t *testing.T) {
	router, sessions, _ := newLegalHoldAdmin(t)
	router = router.WithEscrowReleaser(&fakeReleaser{sessionReleases: 0})
	seedSession(t, sessions, sessionstore.Session{ID: "sess_1", TenantID: "acme", UserID: "alice@acme.com", LegalHold: true})
	rr := setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", SessionID: "sess_1", Hold: false}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear: status %d", rr.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if _, ok := resp["escrowReleased"]; ok {
		t.Errorf("escrowReleased present with 0 releases: %v", resp)
	}
	got, _ := sessions.Get(context.Background(), "acme", "sess_1")
	if got.LegalHold {
		t.Error("flag must be cleared")
	}
}
