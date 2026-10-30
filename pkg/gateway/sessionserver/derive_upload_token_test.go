// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// tokenRoute is one handler that creates a session from a source session's
// snapshot, with the call that drives it.
type tokenRoute struct {
	name     string
	sourceID string
	seed     func(t *testing.T, store sessionstore.Store)
	call     func(t *testing.T, h http.Handler) *httptest.ResponseRecorder
}

// tokenRoutes returns derive and both replay modes.
func tokenRoutes() []tokenRoute {
	replay := func(mode sessionserver.ReplayMode) func(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
		return func(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
			return replayReq(t, h, sessionserver.ReplayRequest{ReplayMode: mode, TargetRuntime: "gemini-cli"})
		}
	}
	return []tokenRoute{
		{
			name: "derive", sourceID: "sess_source",
			seed: func(t *testing.T, store sessionstore.Store) { newSourceSession(t, store) },
			call: func(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
				return deriveRequest(t, h, sessionserver.DeriveRequest{})
			},
		},
		{
			name: "replay workspace_derive", sourceID: "sess_src",
			seed: func(t *testing.T, store sessionstore.Store) { seedTerminalSource(t, store) },
			call: replay(sessionserver.ReplayWorkspaceDerive),
		},
		{
			name: "replay prompt_history", sourceID: "sess_src",
			seed: func(t *testing.T, store sessionstore.Store) { seedTerminalSource(t, store) },
			call: replay(sessionserver.ReplayPromptHistory),
		},
	}
}

// TestDeriveAndReplayMintAnUploadToken_spec_15_1 pins that derive and both
// replay modes return an uploadToken, as POST /v1/sessions does, store its
// digest and expiry on the new row, and that the token authorizes an upload
// into the new session before finalize.
//
// spec: 15.1 (REST API) Session Replay Semantics ("identical to POST
// /v1/sessions", "upload → finalize → start → run"), 7.1 (Normal Flow) step
// 8 and derive rule 2, 7.4 (Upload Safety) Upload authorization.
func TestDeriveAndReplayMintAnUploadToken_spec_15_1(t *testing.T) {
	t.Parallel()
	for _, rt := range tokenRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()
			store := memstore.New()
			rt.seed(t, store)
			t0 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
			clock := func() time.Time { return t0 }
			ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("upload-secret")})
			verifier := uploadtoken.NewVerifier(ring, uploadtoken.NewMemoryTracker(), clock)
			srv := sessionserver.New(store, snapshotFor(t, rt.sourceID).wire(sessionserver.Options{
				DevTenantHeader:     true,
				Clock:               clock,
				IDFunc:              func() string { return "sess_new" },
				UploadTokenIssuer:   uploadtoken.NewIssuer(ring, clock),
				UploadTokenVerifier: verifier,
				UploadTokenTTL:      300 * time.Second,
			}))
			h := srv.Handler()

			rr := rt.call(t, h)
			if rr.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
			}
			var resp struct {
				UploadToken string `json:"uploadToken"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.UploadToken == "" {
				t.Fatalf("response carries no uploadToken; body=%s", rr.Body.String())
			}
			parsed, err := verifier.Verify(resp.UploadToken, "sess_new")
			if err != nil {
				t.Fatalf("the returned token does not verify for the new session: %v", err)
			}
			row, err := store.Get(context.Background(), "acme", "sess_new")
			if err != nil {
				t.Fatalf("get new row: %v", err)
			}
			if row.UploadTokenDigest == "" || row.UploadTokenDigest != parsed.Digest {
				t.Errorf("stored digest = %q, want the returned token's %q", row.UploadTokenDigest, parsed.Digest)
			}
			if want := t0.Add(300 * time.Second); !row.UploadTokenExpiry.Equal(want) {
				t.Errorf("stored expiry = %v, want %v (creation + maxCreatedStateTimeoutSeconds)", row.UploadTokenExpiry, want)
			}

			up := uploadRequest(t, h, "sess_new", "acme", resp.UploadToken, []byte("hello"), "text/plain")
			if up.Code != http.StatusCreated {
				t.Errorf("upload with the returned token: status %d, want 201; body=%s", up.Code, up.Body.String())
			}
		})
	}
}

// TestDeriveAndReplayMintFailureLeavesNoRowAndNoCopy_spec_7_1 pins the §7.1
// atomicity of the mint on derive and replay: a token the issuer refuses
// answers 503 SESSION_CREATION_FAILED with reason
// upload_token_issuance_failed, persists no row, and deletes the copy.
//
// spec: 7.1 (Normal Flow) step 8 and Atomicity of session creation (steps
// 2–8).
func TestDeriveAndReplayMintFailureLeavesNoRowAndNoCopy_spec_7_1(t *testing.T) {
	t.Parallel()
	for _, rt := range tokenRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()
			store := memstore.New()
			rt.seed(t, store)
			snap := snapshotFor(t, rt.sourceID)
			// A dot in the session id makes the issuer refuse the mint.
			const badID = "sess.bad-id"
			srv := sessionserver.New(store, snap.wire(sessionserver.Options{
				DevTenantHeader: true,
				IDFunc:          func() string { return badID },
			}))
			rr := rt.call(t, srv.Handler())
			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
			}
			code, _, details := decodeError(t, rr)
			if code != "SESSION_CREATION_FAILED" || details["reason"] != "upload_token_issuance_failed" {
				t.Errorf("error = %s %v, want SESSION_CREATION_FAILED reason upload_token_issuance_failed", code, details)
			}
			if _, err := store.Get(context.Background(), "acme", badID); err == nil {
				t.Error("a row was persisted although the mint failed")
			}
			if _, _, err := snap.blobs.Get(derivedChunk(badID, badID)); err == nil {
				t.Error("the snapshot copy survived the failed mint")
			}
		})
	}
}
