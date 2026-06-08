// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// spec: §12.9 line 1048 / §15.1 line 1078 — an upload for a T4 tenant
// against a store not configured for envelope encryption is rejected
// with 422 CLASSIFICATION_CONTROL_VIOLATION carrying
// details.reason="tier_store_mismatch".
func TestHandleUploadRejectsTierStoreMismatch_spec_12_9_1048(t *testing.T) {
	store := memstore.New()
	blobs := blobstore.NewMemoryStore(nil)
	// The in-memory store cannot envelope-encrypt; classify "restricted"
	// as a T4 tenant so its write is rejected at the storage boundary.
	blobs.SetTierGuard(func(tenantID string) (bool, error) { return tenantID == "restricted", nil })

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return t0 }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("upload-secret")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	verifier := uploadtoken.NewVerifier(ring, uploadtoken.NewMemoryTracker(), clock)
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:               clock,
		IDFunc:              func() string { return "sess_t4" },
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: verifier,
		Blobs:               blobs,
	})

	row := sessionstore.Session{
		ID:        "sess_t4",
		TenantID:  "restricted",
		State:     session.StateCreated,
		CreatedAt: t0,
		UpdatedAt: t0,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tok, err := issuer.Issue("sess_t4", uploadtoken.DefaultTTL)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	rr := doUpload(t, srv.Handler(), "sess_t4", "restricted", tok, "restricted-bytes")
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d (body=%s)", rr.Code, http.StatusUnprocessableEntity, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code != "CLASSIFICATION_CONTROL_VIOLATION" {
		t.Fatalf("error.code = %q, want CLASSIFICATION_CONTROL_VIOLATION", env.Error.Code)
	}
	if env.Error.Details.Reason != "tier_store_mismatch" {
		t.Fatalf("details.reason = %q, want tier_store_mismatch", env.Error.Details.Reason)
	}
}
