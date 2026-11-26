// SPDX-License-Identifier: MIT

//go:build load_local

package loadlocal_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/signingkeys"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// uploadReplica builds one gateway replica's session surface over the
// shared session store and signing-key store, with its own ring, issuer,
// and verifier, as a gateway process builds them.
func uploadReplica(t *testing.T, sessions sessionstore.Store, keys signingkeys.Store, master []byte) (http.Handler, *uploadtoken.Issuer) {
	t.Helper()
	provider, err := kms.NewLocal(master)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := signingkeys.NewEnvelopeSealer(provider, signingkeys.AliasUploadTokenSigning)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := signingkeys.Open(context.Background(),
		signingkeys.UploadTokenPolicy(uploadtoken.DefaultRotationInterval, uploadtoken.DefaultOverlapWindow),
		keys, sealer, signingkeys.Options{StaleAfter: uploadtoken.DefaultOverlapWindow})
	if err != nil {
		t.Fatal(err)
	}
	shared := uploadtoken.NewSharedKeys(ring)
	issuer := uploadtoken.NewIssuer(shared, nil)
	srv := sessionserver.New(sessions, sessionserver.Options{
		DevTenantHeader:     true,
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: uploadtoken.NewVerifier(shared, nil, nil),
	})
	return ownerprincipal.Wrap(apifront.Handler(srv), ownerprincipal.Alice), issuer
}

// finalizeOutcome posts a finalize with tok to h and returns the status and
// the error code.
func finalizeOutcome(h http.Handler, id, tok string) (int, string) {
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/finalize", bytes.NewReader(nil))
	req.Header.Set("X-Lenny-Tenant-ID", finalizeRaceTenant)
	req.Header.Set("X-Lenny-Upload-Token", tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	return rr.Code, env.Error.Code
}

// spec: §7.1 (Normal Flow), §15.1 (REST API)
// diagnosis: Concurrent finalize calls on two gateway replicas, each
// holding its own ring over one shared signing-key store and one session
// store, consumed the upload token more than once: more than one call
// answered 200, the row moved to `ready` more than once, a loser answered
// something other than 409 or 410, or the ready write did not record the
// consumed mark that refuses the token on both replicas afterwards. Run
// under -race; a data race fails the run.
func TestConcurrentFinalizeOnTwoReplicasConsumesTheTokenOnce(t *testing.T) {
	sessions := &transitionStore{Store: memstore.New()}
	keys := signingkeys.NewMemoryStore()
	master := bytes.Repeat([]byte{0x31}, 32)
	a, issuer := uploadReplica(t, sessions, keys, master)
	b, _ := uploadReplica(t, sessions, keys, master)

	const id = "sess-upload-replicas"
	tok, parsed, err := issuer.IssueDetailed(id, uploadtoken.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: finalizeRaceTenant, UserID: ownerprincipal.Alice, RuntimeRef: "claude-code",
		State: session.StateCreated, UploadTokenDigest: parsed.Digest, UploadTokenExpiry: parsed.Expiry,
	}); err != nil {
		t.Fatal(err)
	}

	const callers = 32
	type outcome struct {
		code    int
		errCode string
	}
	results := make([]outcome, callers)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range callers {
		done.Add(1)
		h := a
		if i%2 == 1 {
			h = b
		}
		go func() {
			defer done.Done()
			start.Wait()
			code, errCode := finalizeOutcome(h, id, tok)
			results[i] = outcome{code, errCode}
		}()
	}
	start.Done()
	done.Wait()

	ok := 0
	for i, r := range results {
		switch {
		case r.code == http.StatusOK:
			ok++
		case r.code == http.StatusGone && r.errCode == "UPLOAD_TOKEN_CONSUMED":
		case r.code == http.StatusConflict && r.errCode == "INVALID_STATE_TRANSITION":
		default:
			t.Errorf("caller %d: %d %s, want 200, 410 UPLOAD_TOKEN_CONSUMED, or 409 INVALID_STATE_TRANSITION", i, r.code, r.errCode)
		}
	}
	if ok != 1 {
		t.Errorf("finalize calls that answered 200 = %d, want 1", ok)
	}
	if n := sessions.count(session.StateFinalizing, session.StateReady); n != 1 {
		t.Errorf("finalizing -> ready writes = %d, want 1", n)
	}
	row, err := sessions.Get(context.Background(), finalizeRaceTenant, id)
	if err != nil || row.UploadTokenConsumedAt.IsZero() {
		t.Fatalf("consumed mark = %v, %v; want set", row.UploadTokenConsumedAt, err)
	}
	for name, h := range map[string]http.Handler{"a": a, "b": b} {
		if code, errCode := finalizeOutcome(h, id, tok); code != http.StatusGone || errCode != "UPLOAD_TOKEN_CONSUMED" {
			t.Errorf("replica %s after the race: %d %s, want 410 UPLOAD_TOKEN_CONSUMED", name, code, errCode)
		}
	}
}
