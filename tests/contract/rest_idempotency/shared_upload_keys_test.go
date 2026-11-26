// SPDX-License-Identifier: MIT

//go:build contract

package rest_idempotency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/idempotency/idempotencymw"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/signingkeys"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// sharedState is what gateway replicas share in production: the session
// rows, the idempotency records, the signing-key table, and the KMS master
// key. Each replica builds its own ring, issuer, verifier, and redactor.
type sharedState struct {
	sessions sessionstore.Store
	idem     idempotencymw.Store
	keys     signingkeys.Store
	master   []byte
}

// replica starts one gateway replica's REST surface over the shared state.
func (s sharedState) replica(t *testing.T) *httptest.Server {
	t.Helper()
	provider, err := kms.NewLocal(s.master)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := signingkeys.NewEnvelopeSealer(provider, signingkeys.AliasUploadTokenSigning)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := signingkeys.Open(context.Background(),
		signingkeys.UploadTokenPolicy(uploadtoken.DefaultRotationInterval, uploadtoken.DefaultOverlapWindow),
		s.keys, sealer, signingkeys.Options{StaleAfter: uploadtoken.DefaultOverlapWindow})
	if err != nil {
		t.Fatal(err)
	}
	keys := uploadtoken.NewSharedKeys(ring)
	srv := sessionserver.New(s.sessions, sessionserver.Options{
		DevTenantHeader:     true,
		UploadTokenIssuer:   uploadtoken.NewIssuer(keys, nil),
		UploadTokenVerifier: uploadtoken.NewVerifier(keys, nil, nil),
	})
	ts := httptest.NewServer(idempotencymw.Wrap(ownerprincipal.Wrap(apifront.Handler(srv), ownerprincipal.Alice),
		idempotencymw.WithCredentials(s.idem, uploadtoken.NewRedactor(keys)), idempotencymw.Options{}))
	t.Cleanup(ts.Close)
	return ts
}

// finalizeWithToken posts POST /v1/sessions/{id}/finalize with the upload
// token and returns the status and the error code, if any.
func finalizeWithToken(t *testing.T, ts *httptest.Server, id, tok string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/sessions/"+id+"/finalize", bytes.NewReader(nil))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-Upload-Token", tok)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	return resp.StatusCode, env.Error.Code
}

// spec: §7.1 (Normal Flow), §11.5 (Idempotency)
// diagnosis: Two gateway replicas over the same stores do not agree on an
// uploadToken. Either the replay of the create on the second replica did not
// return the token the first replica minted (its redactor holds another
// key), the second replica refused the token at finalize (its ring holds
// another key), or the first replica admitted a finalize after the second
// one consumed the token (the consumed mark is not read from the session
// row). See pkg/signingkeys, pkg/uploadtoken/shared.go, and
// verifyUploadTokenValue in pkg/gateway/core/session/sessionserver.
func TestAnUploadTokenWorksOnEveryReplicaAndIsConsumedOnAll(t *testing.T) {
	state := sharedState{
		sessions: memstore.New(),
		idem:     idempotencymw.NewMemoryStore(),
		keys:     signingkeys.NewMemoryStore(),
		master:   bytes.Repeat([]byte{0x21}, 32),
	}
	a := state.replica(t)
	b := state.replica(t)

	body := map[string]any{"runtimeRef": "claude-code"}
	resp, created := post(t, a, "/v1/sessions", "key-replicas", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create on replica a: status %d", resp.StatusCode)
	}
	id, _ := created["id"].(string)
	tok, _ := created["uploadToken"].(string)
	if id == "" || tok == "" {
		t.Fatalf("create: id=%q uploadToken=%q, want both", id, tok)
	}

	resp, replayed := post(t, b, "/v1/sessions", "key-replicas", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("replay on replica b: status %d", resp.StatusCode)
	}
	if got, _ := replayed["uploadToken"].(string); got != tok {
		t.Fatalf("replay on replica b returned uploadToken %q, want the original %q", got, tok)
	}

	if code, errCode := finalizeWithToken(t, b, id, tok); code != http.StatusOK {
		t.Fatalf("finalize on replica b: status %d %s, want 200", code, errCode)
	}
	if code, errCode := finalizeWithToken(t, a, id, tok); code != http.StatusGone || errCode != "UPLOAD_TOKEN_CONSUMED" {
		t.Fatalf("finalize on replica a after replica b consumed the token: %d %s, want 410 UPLOAD_TOKEN_CONSUMED", code, errCode)
	}
}
