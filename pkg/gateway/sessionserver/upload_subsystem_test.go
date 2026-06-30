// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// newUploadServerWithSubsystem builds a sessionserver wired with the
// supplied §4.1 Upload Handler subsystem so the tests can drive
// limiter saturation and breaker trips deterministically.
func newUploadServerWithSubsystem(t *testing.T, sub *subsystem.Subsystem) (*sessionserver.Server, *uploadtoken.Issuer, blobstore.Store, sessionstore.Store, time.Time) {
	t.Helper()
	store := memstore.New()
	blobs := blobstore.NewMemoryStore(nil)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return t0 }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("upload-secret")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	tracker := uploadtoken.NewMemoryTracker()
	verifier := uploadtoken.NewVerifier(ring, tracker, clock)
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:               clock,
		IDFunc:              func() string { return "sess_subsystem" },
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: verifier,
		Blobs:               blobs,
		UploadSubsystem:     sub,
	})
	return srv, issuer, blobs, store, t0
}

func seedAndMintUploadSubsystem(t *testing.T, store sessionstore.Store, issuer *uploadtoken.Issuer, id, tenant string) string {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	row := sessionstore.Session{
		ID:        id,
		TenantID:  tenant,
		State:     session.StateCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tok, err := issuer.Issue(id, uploadtoken.DefaultTTL)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}

func doUpload(t *testing.T, h http.Handler, id, tenant, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/upload", strings.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	req.Header.Set("X-Lenny-Upload-Token", token)
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// spec: §4.1 — when the Upload Handler subsystem breaker is open,
// new uploads return 503 SUBSYSTEM_UNAVAILABLE while other
// subsystems continue serving.
func TestHandleUploadReturns503WhenBreakerOpen(t *testing.T) {
	clk := time.Now()
	br := &subsystem.Breaker{
		FailureThreshold: 1,
		Cooldown:         time.Hour,
		Now:              func() time.Time { return clk },
	}
	// Force the breaker open before the request arrives.
	br.Allow()
	br.RecordFailure()

	sub := &subsystem.Subsystem{
		Name:    "upload_handler",
		Breaker: br,
	}
	srv, issuer, _, store, _ := newUploadServerWithSubsystem(t, sub)
	tok := seedAndMintUploadSubsystem(t, store, issuer, "sess_subsystem", "default")

	rr := doUpload(t, srv.Handler(), "sess_subsystem", "default", tok, "payload")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "SUBSYSTEM_UNAVAILABLE" {
		t.Fatalf("error.code = %q, want SUBSYSTEM_UNAVAILABLE", env.Error.Code)
	}
}

// spec: §4.1 — a saturated Upload Handler limiter returns 503
// SUBSYSTEM_UNAVAILABLE for new uploads and does NOT trip the
// breaker (limiter rejection is back-pressure, not a downstream
// failure).
func TestHandleUploadReturns503WhenLimiterSaturated(t *testing.T) {
	sub := &subsystem.Subsystem{
		Name:    "upload_handler",
		Breaker: &subsystem.Breaker{FailureThreshold: 5},
		Limiter: &subsystem.Limiter{MaxConcurrent: 1},
	}
	srv, issuer, _, store, _ := newUploadServerWithSubsystem(t, sub)
	tok := seedAndMintUploadSubsystem(t, store, issuer, "sess_subsystem", "default")

	// Pre-take the single slot directly via the limiter so the next
	// HTTP request finds it saturated. The TryAcquire path mirrors
	// what acquireUploadSlot uses internally.
	r, ok := sub.Limiter.TryAcquire()
	if !ok {
		t.Fatal("expected to acquire the only slot in test setup")
	}
	defer r()

	rr := doUpload(t, srv.Handler(), "sess_subsystem", "default", tok, "payload")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
	if sub.State() != subsystem.StateClosed {
		t.Fatalf("breaker state = %q, want %q after limiter rejection", sub.State(), subsystem.StateClosed)
	}
}

// spec: §4.1 — a healthy subsystem passes uploads through; the
// breaker stays closed and the limiter accounts the in-flight slot.
func TestHandleUploadPassesThroughHealthySubsystem(t *testing.T) {
	sub := &subsystem.Subsystem{
		Name:    "upload_handler",
		Breaker: &subsystem.Breaker{FailureThreshold: 5},
		Limiter: &subsystem.Limiter{MaxConcurrent: 4},
	}
	srv, issuer, _, store, _ := newUploadServerWithSubsystem(t, sub)
	tok := seedAndMintUploadSubsystem(t, store, issuer, "sess_subsystem", "default")

	rr := doUpload(t, srv.Handler(), "sess_subsystem", "default", tok, "payload")
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%s)", rr.Code, http.StatusCreated, rr.Body.String())
	}
	if sub.Limiter.InFlight() != 0 {
		t.Fatalf("InFlight() = %d after upload, want 0", sub.Limiter.InFlight())
	}
	if sub.State() != subsystem.StateClosed {
		t.Fatalf("breaker state = %q, want %q", sub.State(), subsystem.StateClosed)
	}
}
