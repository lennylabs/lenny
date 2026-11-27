// SPDX-License-Identifier: MIT

//go:build security

package security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/signingkeys"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// keyHolderGateway is a gateway session surface over a shared key ring,
// with the secret of the ring's active key in the test's hands, as an
// attacker who read gateway memory or unwrapped a stored key holds it.
type keyHolderGateway struct {
	h      http.Handler
	ring   *signingkeys.Ring
	clock  *keysClock
	secret []byte
}

func newKeyHolderGateway(t *testing.T) keyHolderGateway {
	t.Helper()
	provider, err := kms.NewLocal(bytes.Repeat([]byte{0x61}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := signingkeys.NewEnvelopeSealer(provider, signingkeys.AliasUploadTokenSigning)
	if err != nil {
		t.Fatal(err)
	}
	clock := &keysClock{now: time.Now().UTC().Truncate(time.Second)}
	ring, err := signingkeys.Open(context.Background(),
		signingkeys.UploadTokenPolicy(uploadtoken.DefaultRotationInterval, uploadtoken.DefaultOverlapWindow, uploadtoken.DefaultTTL),
		signingkeys.NewMemoryStore(), sealer, signingkeys.Options{StaleAfter: uploadtoken.DefaultOverlapWindow, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	active, err := ring.SigningKey()
	if err != nil {
		t.Fatal(err)
	}
	keys := uploadtoken.NewSharedKeys(ring)
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader:     true,
		Clock:               clock.Now,
		UploadTokenIssuer:   uploadtoken.NewIssuer(keys, clock.Now),
		UploadTokenVerifier: uploadtoken.NewVerifier(keys, nil, clock.Now),
	})
	return keyHolderGateway{
		h: ownerprincipal.Wrap(apifront.Handler(srv), ownerprincipal.Alice), ring: ring, clock: clock,
		secret: append([]byte(nil), active.Secret...),
	}
}

func (g keyHolderGateway) do(t *testing.T, path, tok string) (int, string, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"runtimeRef":"claude-code"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	if tok != "" {
		req.Header.Set("X-Lenny-Upload-Token", tok)
	}
	rr := httptest.NewRecorder()
	g.h.ServeHTTP(rr, req)
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	code := ""
	if e, ok := body["error"].(map[string]any); ok {
		code, _ = e["code"].(string)
	}
	return rr.Code, code, body
}

// spec: §7.1 (uploadToken format and security properties), §7.4 (Upload
// Safety)
// diagnosis: a holder of an upload-token key's secret got a token accepted
// that the gateway did not issue: a token signed for a live session with an
// expiry days ahead, or a token for a create-and-start session, which
// leaves the upload phase at creation. The verifier does not bind a token
// to the digest the session row records, or create-and-start does not
// consume its token, so a leaked key lets an attacker write into any
// session's workspace for as long as the key verifies.
func TestAHolderOfAKeySecretGainsNoTokenTheGatewayDidNotIssue(t *testing.T) {
	g := newKeyHolderGateway(t)
	code, _, created := g.do(t, "/v1/sessions", "")
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, created)
	}
	id, _ := created["id"].(string)
	issued, _ := created["uploadToken"].(string)

	forged := forgeToken(g.secret, id, g.clock.Now().Add(72*time.Hour))
	if code, errCode, _ := g.do(t, "/v1/sessions/"+id+"/finalize", forged); code != http.StatusUnauthorized || errCode != "UPLOAD_TOKEN_INVALID" {
		t.Errorf("forged token for a live session: %d %s, want 401 UPLOAD_TOKEN_INVALID", code, errCode)
	}

	code, _, started := g.do(t, "/v1/sessions/start", "")
	if code != http.StatusCreated {
		t.Fatalf("create-and-start: %d %v", code, started)
	}
	startedID, _ := started["id"].(string)
	startedTok, _ := started["uploadToken"].(string)
	if code, errCode, _ := g.do(t, "/v1/sessions/"+startedID+"/finalize", startedTok); code != http.StatusGone || errCode != "UPLOAD_TOKEN_CONSUMED" {
		t.Errorf("create-and-start token after start: %d %s, want 410 UPLOAD_TOKEN_CONSUMED", code, errCode)
	}
	reforged := forgeToken(g.secret, startedID, g.clock.Now().Add(time.Hour))
	if code, errCode, _ := g.do(t, "/v1/sessions/"+startedID+"/finalize", reforged); code != http.StatusUnauthorized || errCode != "UPLOAD_TOKEN_INVALID" {
		t.Errorf("forged token for a create-and-start session: %d %s, want 401 UPLOAD_TOKEN_INVALID", code, errCode)
	}

	if code, errCode, _ := g.do(t, "/v1/sessions/"+id+"/finalize", issued); code != http.StatusOK {
		t.Errorf("the issued token: %d %s, want 200", code, errCode)
	}
}

// spec: §7.1 (uploadToken format and security properties)
// diagnosis: after a forced rotation, a key holder's reproduction of a
// session's issued token outlived the replaced key's verification time, or
// the rotation refused a legitimate token before that time ended. The
// replaced key is not retired at the new key's activation, or it stays in
// the ring past its verification time.
func TestAForcedRotationEndsAnExposedKeyAfterItsVerificationTime(t *testing.T) {
	ctx := context.Background()
	g := newKeyHolderGateway(t)
	code, _, created := g.do(t, "/v1/sessions", "")
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, created)
	}
	id, _ := created["id"].(string)
	issued, _ := created["uploadToken"].(string)

	for i := 0; i < 2; i++ {
		if _, err := g.ring.RotateNow(ctx); err != nil {
			t.Fatalf("RotateNow: %v", err)
		}
	}
	g.clock.Advance(signingkeys.DefaultPublishLead)
	if err := g.ring.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if k, _ := g.ring.SigningKey(); bytes.Equal(k.Secret, g.secret) {
		t.Fatal("the exposed key still signs after two forced rotations")
	}
	verify := signingkeys.UploadTokenVerificationTime(uploadtoken.DefaultOverlapWindow, uploadtoken.DefaultTTL)
	g.clock.Advance(verify + time.Second)
	if err := g.ring.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	for _, k := range g.ring.VerificationKeys() {
		if bytes.Equal(k.Secret, g.secret) {
			t.Fatal("the exposed key still verifies after its verification time")
		}
	}
	if code, errCode, _ := g.do(t, "/v1/sessions/"+id+"/finalize", issued); code == http.StatusOK {
		t.Errorf("a token of the exposed key after its verification time: %d %s, want a refusal", code, errCode)
	}
}
