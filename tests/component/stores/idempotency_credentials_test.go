//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/idempotency/idempotencymw"
	idempgstore "github.com/lennylabs/lenny/pkg/idempotency/idempotencymw/pgstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// spec: §11.5 (Idempotency), §12.9 (Data Classification)
// diagnosis: The response_body the Postgres idempotency store holds for a
// create response carried the uploadToken, or the replay through the
// Postgres store returned a body other than the original. The store
// WithCredentials returns does not strip on Put or remint on Claim, or the
// redaction reference does not survive the response_body column; see
// pkg/uploadtoken/redact.go and pkg/idempotency/idempotencymw/credentials.go.
func TestPostgresIdempotencyRecordHoldsNoUploadToken(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	store := idempgstore.New(pg.Pool)

	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("component-secret")})
	issuer := uploadtoken.NewIssuer(ring, nil)
	const sessionID = "0f7c2a5e-9d41-4c3b-8a2f-6b1e0d9c7a35"
	var runs atomic.Int32
	create := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		runs.Add(1)
		tok, err := issuer.Issue(sessionID, time.Minute)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		body, _ := json.Marshal(map[string]string{"id": sessionID, "state": "created", "uploadToken": tok})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(body)
	})
	h := idempotencymw.Wrap(create, idempotencymw.WithCredentials(store, uploadtoken.NewRedactor(ring)), idempotencymw.Options{
		PrincipalFromRequest: func(r *http.Request) (string, string, string) {
			return r.Header.Get("X-Lenny-Tenant-ID"), "alice", ""
		},
	})

	call := func() (int, string) {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{"runtimeRef":"echo"}`))
		req.Header.Set(idempotencymw.HeaderName, "create-1")
		req.Header.Set("X-Lenny-Tenant-ID", tenant)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		raw, _ := io.ReadAll(rec.Result().Body)
		return rec.Code, string(raw)
	}

	status1, first := call()
	if status1 != http.StatusCreated {
		t.Fatalf("first call: status %d, body %s", status1, first)
	}
	var created struct{ UploadToken string }
	if err := json.Unmarshal([]byte(first), &created); err != nil || created.UploadToken == "" {
		t.Fatalf("first call: no uploadToken in %s (err %v)", first, err)
	}

	rec, found, err := store.Get(ctx, aliceRecord(tenant, "create-1"))
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	stored := string(rec.Response.Body)
	if strings.Contains(stored, created.UploadToken) {
		t.Fatalf("response_body holds the uploadToken: %s", stored)
	}
	if !strings.Contains(stored, sessionID+".") || !strings.Contains(stored, ".redacted.") {
		t.Fatalf("response_body = %s, want a redaction reference in the token's place", stored)
	}

	status2, replay := call()
	if status2 != http.StatusCreated || replay != first {
		t.Fatalf("replay = %d %s, want 201 %s", status2, replay, first)
	}
	if runs.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", runs.Load())
	}
}
