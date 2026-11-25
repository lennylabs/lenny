// SPDX-License-Identifier: MIT

//go:build contract

package rest_idempotency_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/idempotency/idempotencymw"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// spec: §11.5 (Idempotency), §7.1 (Normal Flow)
// diagnosis: A replayed POST /v1/sessions returned an uploadToken other than
// the first response's, or the idempotency record held the token. The store
// WithCredentials returns does not strip on Put or remint on Claim, or its
// redactor is built over a different signing-key ring than the issuer; see
// pkg/uploadtoken/redact.go and pkg/idempotency/idempotencymw/credentials.go.
func TestReplayedCreateSessionCarriesTheOriginalUploadTokenAndTheRecordHoldsNone(t *testing.T) {
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("contract-secret")})
	verifier := uploadtoken.NewVerifier(ring, uploadtoken.NewMemoryTracker(), nil)
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader:     true,
		UploadTokenIssuer:   uploadtoken.NewIssuer(ring, nil),
		UploadTokenVerifier: verifier,
	})
	idem := idempotencymw.NewMemoryStore()
	ts := httptest.NewServer(idempotencymw.Wrap(ownerprincipal.Wrap(apifront.Handler(srv), ownerprincipal.Alice),
		idempotencymw.WithCredentials(idem, uploadtoken.NewRedactor(ring)), idempotencymw.Options{}))
	t.Cleanup(ts.Close)

	body := map[string]any{"runtimeRef": "claude-code"}
	resp1, b1 := post(t, ts, "/v1/sessions", "key-token", body)
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first call: want 201, got %d", resp1.StatusCode)
	}
	id, _ := b1["id"].(string)
	tok, _ := b1["uploadToken"].(string)
	if id == "" || tok == "" {
		t.Fatalf("first call: id=%q uploadToken=%q, want both set", id, tok)
	}

	rec, found, err := idem.Get(context.Background(), "acme", "key-token")
	if err != nil || !found {
		t.Fatalf("idempotency record: found=%v err=%v", found, err)
	}
	if strings.Contains(string(rec.Response.Body), tok) {
		t.Fatalf("idempotency record holds the uploadToken: %s", rec.Response.Body)
	}

	resp2, b2 := post(t, ts, "/v1/sessions", "key-token", body)
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("replay: want 201, got %d", resp2.StatusCode)
	}
	if got, _ := b2["uploadToken"].(string); got != tok {
		t.Fatalf("replay uploadToken = %q, want the original %q", got, tok)
	}
	if got, _ := b2["id"].(string); got != id {
		t.Fatalf("replay id = %q, want %q", got, id)
	}
	if _, err := verifier.Verify(tok, id); err != nil {
		t.Fatalf("replayed uploadToken does not verify: %v", err)
	}
}
