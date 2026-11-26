// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// F-7.4.17: a successful finalize writes a §16.6 session.finalize_workspace
// audit row with the consumed digest in Detail.
func TestHandleFinalizeEmitsAuditEvent_spec_7_4_17(t *testing.T) {
	store := memstore.New()
	clock := func() time.Time { return time.Date(2026, 5, 26, 9, 0, 0, 0, time.UTC) }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k", Secret: []byte("s")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	tracker := uploadtoken.NewMemoryTracker()
	verifier := uploadtoken.NewVerifier(ring, tracker, clock)
	sink := &captureLifecycleAudit{}
	srv := New(store, Options{
		DevTenantHeader:     true,
		Clock:               clock,
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: verifier,
		LifecycleAuditSink:  sink,
	})

	// Seed a session row mid-flow (state=created) with a known digest.
	tok, parsed, err := issuer.IssueDetailed("sess_fin", uploadtoken.DefaultTTL)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	row := sessionstore.Session{
		ID: "sess_fin", TenantID: "default", UserID: testOwner,
		RuntimeRef: "claude-code", State: session.StateCreated,
		CreatedAt: clock(), UpdatedAt: clock(),
		UploadTokenDigest: parsed.Digest,
		UploadTokenExpiry: parsed.Expiry,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_fin/finalize", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "default")
	req.Header.Set("X-Lenny-Upload-Token", tok)
	rr := httptest.NewRecorder()
	asOwner(srv.Handler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit events: got %d, want 1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.EventType != auditSessionWorkspaceFinalized {
		t.Errorf("eventType = %q, want %q", ev.EventType, auditSessionWorkspaceFinalized)
	}
	if ev.Detail != parsed.Digest {
		t.Errorf("Detail = %q, want digest %q", ev.Detail, parsed.Digest)
	}
	stored, err := store.Get(context.Background(), "default", "sess_fin")
	if err != nil {
		t.Fatal(err)
	}
	if stored.UploadTokenConsumedAt.IsZero() {
		t.Errorf("the ready write should record the upload token consumed")
	}
}
