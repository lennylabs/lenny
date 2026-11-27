// SPDX-License-Identifier: MIT

//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/uploadhandler"
	"github.com/lennylabs/lenny/pkg/idempotency/idempotencymw"
	idempotencypg "github.com/lennylabs/lenny/pkg/idempotency/idempotencymw/pgstore"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/signingkeys"
	signingkeyspg "github.com/lennylabs/lenny/pkg/signingkeys/pgstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
	"github.com/lennylabs/lenny/tests/testinfra/uploadwire"
)

// twoReplicaTenant is the tenant the two-replica upload test runs under.
const twoReplicaTenant = "acme"

// uploadTokenReplica builds one gateway replica as the gateway process
// wires it: its own signing-key ring over platform_signing_keys, its own
// session server and upload handler over the shared Postgres session rows,
// and the idempotency middleware over the shared Postgres records, with a
// redactor over its own ring.
func uploadTokenReplica(t *testing.T, pg *containers.Postgres, blobs blobstore.Store, master []byte) http.Handler {
	t.Helper()
	provider, err := kms.NewLocal(master)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := signingkeys.NewEnvelopeSealer(provider, signingkeys.AliasUploadTokenSigning)
	if err != nil {
		t.Fatal(err)
	}
	keyStore, err := signingkeyspg.New(pg.Pool)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := signingkeys.Open(context.Background(),
		signingkeys.UploadTokenPolicy(uploadtoken.DefaultRotationInterval, uploadtoken.DefaultOverlapWindow, uploadtoken.DefaultTTL),
		keyStore, sealer, signingkeys.Options{StaleAfter: uploadtoken.DefaultOverlapWindow})
	if err != nil {
		t.Fatalf("open the signing-key ring: %v", err)
	}
	keys := uploadtoken.NewSharedKeys(ring)
	srv := sessionserver.New(pgstore.New(pg.Pool), sessionserver.Options{
		DevTenantHeader:     true,
		UploadTokenIssuer:   uploadtoken.NewIssuer(keys, nil),
		UploadTokenVerifier: uploadtoken.NewVerifier(keys, nil, nil),
	})
	uploadwire.Attach(t, srv, uploadhandler.Options{Blobs: blobs})
	return ownerprincipal.Wrap(idempotencymw.Wrap(apifront.Handler(srv),
		idempotencymw.WithCredentials(idempotencypg.New(pg.Pool), uploadtoken.NewRedactor(keys)),
		idempotencymw.Options{PrincipalFromRequest: ownerprincipal.Scope}), ownerprincipal.Alice)
}

// replicaRequest sends one request for the test tenant to h.
func replicaRequest(h http.Handler, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", twoReplicaTenant)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// spec: §7.1 (Normal Flow), §11.5 (Idempotency)
// diagnosis: Two gateway replicas over one Postgres do not share the
// uploadToken. A token minted on replica a was refused by replica b's
// upload handler (b's ring does not hold a's key: platform_signing_keys
// is not shared or not unwrapped), the replay of the create on replica b
// returned something other than the original token (b's redactor holds
// another key), a finalize on replica a after uploads on replica b failed,
// or an upload on replica b after the finalize was not refused with 410
// UPLOAD_TOKEN_CONSUMED (the consumed mark is not read from the Postgres
// session row).
func TestUploadTokenWorksAcrossTwoReplicasOverPostgres(t *testing.T) {
	pg := sharedDatabase(t)
	seedTenant(t, pg, twoReplicaTenant)
	blobs := blobstore.NewMemoryStore(nil)
	master := bytes.Repeat([]byte{0x61}, 32)
	a := uploadTokenReplica(t, pg, blobs, master)
	b := uploadTokenReplica(t, pg, blobs, master)

	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo"})
	jsonHeaders := map[string]string{"Content-Type": "application/json", idempotencymw.HeaderName: "two-replicas"}
	rr := replicaRequest(a, http.MethodPost, "/v1/sessions", createBody, jsonHeaders)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create on replica a: %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		ID          string `json:"id"`
		UploadToken string `json:"uploadToken"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil || created.ID == "" || created.UploadToken == "" {
		t.Fatalf("create response %s (err %v), want an id and an uploadToken", rr.Body.String(), err)
	}
	tokenHeader := map[string]string{"Content-Type": "text/markdown", "X-Lenny-Upload-Token": created.UploadToken}

	if rr := replicaRequest(b, http.MethodPost, "/v1/sessions/"+created.ID+"/upload", []byte("# notes"), tokenHeader); rr.Code != http.StatusCreated {
		t.Fatalf("upload on replica b with replica a's token: %d %s", rr.Code, rr.Body.String())
	}
	rr = replicaRequest(b, http.MethodPost, "/v1/sessions", createBody, jsonHeaders)
	if rr.Code != http.StatusCreated {
		t.Fatalf("replay of the create on replica b: %d %s", rr.Code, rr.Body.String())
	}
	var replayed struct {
		UploadToken string `json:"uploadToken"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &replayed)
	if replayed.UploadToken != created.UploadToken {
		t.Fatalf("replay on replica b returned uploadToken %q, want the original", replayed.UploadToken)
	}

	finalizeHeader := map[string]string{"X-Lenny-Upload-Token": created.UploadToken}
	if rr := replicaRequest(a, http.MethodPost, "/v1/sessions/"+created.ID+"/finalize", nil, finalizeHeader); rr.Code != http.StatusOK {
		t.Fatalf("finalize on replica a: %d %s", rr.Code, rr.Body.String())
	}
	rr = replicaRequest(b, http.MethodPost, "/v1/sessions/"+created.ID+"/upload", []byte("# late"), tokenHeader)
	if rr.Code != http.StatusGone || !bytes.Contains(rr.Body.Bytes(), []byte("UPLOAD_TOKEN_CONSUMED")) {
		t.Errorf("upload on replica b after the finalize on replica a: %d %s, want 410 UPLOAD_TOKEN_CONSUMED", rr.Code, rr.Body.String())
	}
}
