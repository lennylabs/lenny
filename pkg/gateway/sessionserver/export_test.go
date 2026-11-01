// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// MintUploadToken issues a token for a seeded row and records its digest and
// expiry on the row, as session creation does, and returns the token. A token
// verifies only against its row, so a fixture that wires a verifier mints
// through this and reuses the returned token for the whole test.
func MintUploadToken(t *testing.T, store sessionstore.Store, issuer *uploadtoken.Issuer, tenantID, id string) string {
	t.Helper()
	tok, parsed, err := issuer.IssueDetailed(id, uploadtoken.DefaultTTL)
	if err != nil {
		t.Fatalf("mint upload token: %v", err)
	}
	if _, err := store.Update(context.Background(), tenantID, id, func(row *sessionstore.Session) error {
		row.UploadTokenDigest = parsed.Digest
		row.UploadTokenExpiry = parsed.Expiry
		return nil
	}); err != nil {
		t.Fatalf("record upload token on the row: %v", err)
	}
	return tok
}

// DriveResumeAndWait drives one resume attempt for the row, as the watchdog's
// resume_pending sweep does, and waits until the attempt returns. It is the
// external tests' handle on the asynchronous resume driver.
func (s *Server) DriveResumeAndWait(ctx context.Context, row sessionstore.Session) {
	s.DriveResume(ctx, row)
	s.WaitResumeIdle(row.ID)
}

// WaitResumeIdle waits until no resume attempt for the session is in flight
// on this replica, or ten seconds pass.
func (s *Server) WaitResumeIdle(sessionID string) {
	deadline := time.Now().Add(10 * time.Second)
	for s.resumeAttempts.inFlight(sessionID) && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
}

// inFlight reports whether an attempt for the session is registered.
func (r *attemptRecord) inFlight(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.cancels[sessionID]
	return ok
}

// OwnedRouteSpy mounts spy at pattern behind the ownership wrapper Handler()
// binds to wrapper in route_table.go ("readID" or "manageID"), inside the
// same tenant gate Handler() applies, so an external test observes directly
// whether a request reaches the handler. It returns nil for any other
// wrapper name.
func (s *Server) OwnedRouteSpy(pattern, wrapper string, spy http.HandlerFunc) http.Handler {
	var wrap func(http.HandlerFunc) http.HandlerFunc
	switch wrapper {
	case "readID":
		wrap = s.readSessionRoute
	case "manageID":
		wrap = s.manageSessionRoute
	default:
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc(pattern, wrap(spy))
	return s.requireTenant(mux)
}
