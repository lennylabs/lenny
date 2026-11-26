// SPDX-License-Identifier: MIT

//go:build security

// The security tier probe for the idempotency store's handling of the
// uploadToken credential. A reader of the idempotency table holds the
// redaction reference of every stored create response. The probe plays
// that reader: it copies the reference of another session's create
// response into the env of a create request of its own, which the create
// response echoes, and replays that request. The replay must not turn the
// copied reference into the other session's token, and the table must hold
// no token at any point.
package security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/pkg/idempotency/idempotencymw"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// createWithKey posts a CreateSession body with an Idempotency-Key and
// returns the decoded response.
func createWithKey(t *testing.T, ts *httptest.Server, key string, body map[string]any) map[string]any {
	t.Helper()
	return createAs(t, ts, ownerprincipal.Alice, key, body)
}

// createAs is createWithKey with the caller's subject named by user.
func createAs(t *testing.T, ts *httptest.Server, user, key string, body map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/sessions", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set(ownerprincipal.UserHeader, user)
	req.Header.Set(idempotencymw.HeaderName, key)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create %s: status %d, body %s", key, resp.StatusCode, out)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("create %s: %v", key, err)
	}
	return decoded
}

// spec: §11.5 (Idempotency), §7.1 (Normal Flow)
// diagnosis: A replayed create response turned a redaction reference that
// the request carried, copied from another session's idempotency record,
// into that session's uploadToken, or an idempotency record held an
// uploadToken. Strip in pkg/uploadtoken/redact.go must give every reference
// it did not write the mark no key reproduces before it strips the tokens.
func TestACopiedRedactionReferenceDoesNotReplayAsTheVictimsUploadToken(t *testing.T) {
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("security-secret")})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader:     true,
		UploadTokenIssuer:   uploadtoken.NewIssuer(ring, nil),
		UploadTokenVerifier: uploadtoken.NewVerifier(ring, uploadtoken.NewMemoryTracker(), nil),
	})
	table := idempotencymw.NewMemoryStore()
	ts := httptest.NewServer(scopedIdempotency(apifront.Handler(srv), table, ring))
	t.Cleanup(ts.Close)

	victim := createWithKey(t, ts, "victim", map[string]any{"runtimeRef": "claude-code"})
	victimToken, _ := victim["uploadToken"].(string)
	if victimToken == "" {
		t.Fatal("victim create returned no uploadToken")
	}
	rec, found, err := table.Get(context.Background(), idempotency.Key{TenantID: "acme", Subject: ownerprincipal.Alice, Value: "victim"})
	if err != nil || !found {
		t.Fatalf("victim record: found=%v err=%v", found, err)
	}
	if strings.Contains(string(rec.Response.Body), victimToken) {
		t.Fatalf("the idempotency table holds the victim's uploadToken: %s", rec.Response.Body)
	}
	var stored struct{ UploadToken string }
	if err := json.Unmarshal(rec.Response.Body, &stored); err != nil || !strings.Contains(stored.UploadToken, ".redacted.") {
		t.Fatalf("victim record holds no reference: %s (err %v)", rec.Response.Body, err)
	}

	attack := map[string]any{"runtimeRef": "claude-code", "env": map[string]string{"COPIED": stored.UploadToken}}
	first := createWithKey(t, ts, "attacker", attack)
	replay := createWithKey(t, ts, "attacker", attack)
	for name, resp := range map[string]map[string]any{"first": first, "replay": replay} {
		raw, _ := json.Marshal(resp)
		if strings.Contains(string(raw), victimToken) {
			t.Fatalf("%s attacker response carries the victim's uploadToken: %s", name, raw)
		}
	}
	env, _ := replay["env"].(map[string]any)
	if copied, _ := env["COPIED"].(string); !strings.Contains(copied, ".redacted.") {
		t.Fatalf("replay env = %v, want the echoed reference: the probe did not reach the echo path", replay["env"])
	}
	if first["uploadToken"] != replay["uploadToken"] {
		t.Fatalf("replay uploadToken = %v, want the attacker's own %v", replay["uploadToken"], first["uploadToken"])
	}
}

// scopedIdempotency mounts h behind the idempotency middleware over table,
// with the stored-credential redactor over ring and the record scope of the
// caller's principal, which ownerprincipal.WrapFromHeader attaches from the
// X-Lenny-User-ID header before the middleware runs.
func scopedIdempotency(h http.Handler, table idempotencymw.Store, ring *uploadtoken.KeyRing) http.Handler {
	return ownerprincipal.WrapFromHeader(idempotencymw.Wrap(h,
		idempotencymw.WithCredentials(table, uploadtoken.NewRedactor(ring)),
		idempotencymw.Options{PrincipalFromRequest: ownerprincipal.Scope}), ownerprincipal.Alice)
}

// spec: §11.5 (Idempotency)
// diagnosis: bob, a user of alice's tenant, sent alice's Idempotency-Key
// and request body and received alice's stored create response, carrying
// her session id or an upload token for her session. The record scope must
// include the principal's subject (idempotency.Key in pkg/idempotency), and
// the middleware must resolve it from the request's principal.
func TestAnotherUserReplayingAUsersKeyAndBodyReceivesNoneOfThatUsersSession(t *testing.T) {
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("security-secret")})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader:     true,
		UploadTokenIssuer:   uploadtoken.NewIssuer(ring, nil),
		UploadTokenVerifier: uploadtoken.NewVerifier(ring, uploadtoken.NewMemoryTracker(), nil),
	})
	ts := httptest.NewServer(scopedIdempotency(apifront.Handler(srv), idempotencymw.NewMemoryStore(), ring))
	t.Cleanup(ts.Close)

	body := map[string]any{"runtimeRef": "claude-code"}
	alice := createAs(t, ts, ownerprincipal.Alice, "shared-key", body)
	aliceID, _ := alice["id"].(string)
	aliceToken, _ := alice["uploadToken"].(string)
	if aliceID == "" || aliceToken == "" {
		t.Fatalf("alice's create: id=%q uploadToken=%q, want both", aliceID, aliceToken)
	}

	bob := createAs(t, ts, "bob@acme.com", "shared-key", body)
	raw, _ := json.Marshal(bob)
	if strings.Contains(string(raw), aliceID) || strings.Contains(string(raw), aliceToken) {
		t.Fatalf("bob's response carries alice's session id or upload token: %s", raw)
	}
	if id, _ := bob["id"].(string); id == "" || id == aliceID {
		t.Fatalf("bob's response id = %v, want a session of his own", bob["id"])
	}
}
