// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §7.1 uploadToken across two real
// cmd/lenny-gateway processes over one Postgres. The session row is the only
// state the processes share, so a token minted by one uploads and finalizes on
// the other, a replay answers 410 on the issuer, and a process started after
// the issuer stops verifies a token the issuer minted. The test uses HTTP
// surfaces only.
package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

const (
	uploadReplicaTenant  = "default"
	uploadReplicaRuntime = "echo"
)

// uploadReplicaCall sends one request as the tenant's session owner and returns
// its status and body. A non-empty token travels in X-Lenny-Upload-Token.
func uploadReplicaCall(t *testing.T, base, method, path, token, contentType, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, base+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request %s: %v", path, err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Lenny-Tenant-ID", uploadReplicaTenant)
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	if token != "" {
		req.Header.Set("X-Lenny-Upload-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// uploadReplicaCreate creates a session on the gateway at base and returns its
// id and uploadToken.
func uploadReplicaCreate(t *testing.T, base string) (string, string) {
	t.Helper()
	body := `{"runtimeRef":"` + uploadReplicaRuntime + `","userId":"alice@acme.com"}`
	code, raw := uploadReplicaCall(t, base, http.MethodPost, "/v1/sessions", "", "application/json", body)
	if code != http.StatusCreated {
		t.Fatalf("create on %s: status %d: %s", base, code, raw)
	}
	var created struct {
		ID          string `json:"id"`
		UploadToken string `json:"uploadToken"`
	}
	if err := json.Unmarshal([]byte(raw), &created); err != nil || created.ID == "" || created.UploadToken == "" {
		t.Fatalf("create on %s: no id or token in %s (%v)", base, raw, err)
	}
	return created.ID, created.UploadToken
}

// uploadReplicaUpload posts one file to the session under token.
func uploadReplicaUpload(t *testing.T, base, id, token string) (int, string) {
	t.Helper()
	return uploadReplicaCall(t, base, http.MethodPost, "/v1/sessions/"+id+"/upload", token, "text/plain", "payload")
}

// uploadReplicaFinalize finalizes the session under token.
func uploadReplicaFinalize(t *testing.T, base, id, token string) (int, string) {
	t.Helper()
	return uploadReplicaCall(t, base, http.MethodPost, "/v1/sessions/"+id+"/finalize", token, "application/json", "")
}

// wantUploadReplicaGone fails unless the call answered 410
// UPLOAD_TOKEN_CONSUMED.
func wantUploadReplicaGone(t *testing.T, what string, code int, body string) {
	t.Helper()
	if code != http.StatusGone || !strings.Contains(body, "UPLOAD_TOKEN_CONSUMED") {
		t.Errorf("%s: status %d: %s, want 410 UPLOAD_TOKEN_CONSUMED", what, code, body)
	}
}

// spec: 7.1 (uploadToken format and security properties, Single-use invalidation), 7.4 (Upload Safety), 10.1.1 (Stateless Replicas and Per-Session Coordination)
// diagnosis: a 401 UPLOAD_TOKEN_INVALID on the peer means the token still
// verifies against a secret held in the issuing process rather than the digest
// on the session row. A success or a 401 on a replay means consumption is not
// derived from the shared row. A refusal on the replacement gateway means the
// same for a restart. A create or a session-row miss on the peer means the
// replicas do not share --postgres-dsn, which the default memory store needs.
func TestUploadTokenCompletesAcrossGatewayProcesses_spec_7_1(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)
	pg := sharedDatabase(t)
	keyFile := filepath.Join(t.TempDir(), "master.key")
	// Create the master key before any replica starts, so concurrent first
	// use cannot write two different keys to one path.
	if _, err := kms.LoadOrCreateMasterKey(keyFile); err != nil {
		t.Fatalf("create master key: %v", err)
	}
	args := keySetReplicaArgs(pg.DSN, keyFile)
	gwA := gateway.StartWith(t, args...)
	gwB := gateway.StartWith(t, args...)
	a, b := gwA.BaseURL(), gwB.BaseURL()

	// Migrations seed the default tenant, so only the runtime needs a row.
	bootstrap, _ := json.Marshal(map[string]any{
		"runtimes": []map[string]any{{
			"name": uploadReplicaRuntime, "image": "lenny/echo@sha256:abc",
			"labels": map[string]string{"tier": "test"},
		}},
	})
	if code, raw := uploadReplicaCall(t, a, http.MethodPost, "/v1/admin/bootstrap", "", "application/json", string(bootstrap)); code != http.StatusOK {
		t.Fatalf("bootstrap: status %d: %s\n%s", code, raw, gwA.Logs())
	}

	t.Run("mint on A, upload and finalize on B, replay on A", func(t *testing.T) {
		id, token := uploadReplicaCreate(t, a)
		if code, body := uploadReplicaUpload(t, b, id, token); code != http.StatusCreated {
			t.Fatalf("upload on replica B: status %d: %s\n%s", code, body, gwB.Logs())
		}
		if code, body := uploadReplicaFinalize(t, b, id, token); code != http.StatusOK {
			t.Fatalf("finalize on replica B: status %d: %s\n%s", code, body, gwB.Logs())
		}
		code, body := uploadReplicaFinalize(t, a, id, token)
		wantUploadReplicaGone(t, "finalize replay on replica A", code, body)
		code, body = uploadReplicaUpload(t, a, id, token)
		wantUploadReplicaGone(t, "upload replay on replica A", code, body)
	})

	t.Run("finalize with no upload on the peer", func(t *testing.T) {
		id, token := uploadReplicaCreate(t, b)
		if code, body := uploadReplicaFinalize(t, a, id, token); code != http.StatusOK {
			t.Fatalf("finalize on replica A: status %d: %s\n%s", code, body, gwA.Logs())
		}
		code, body := uploadReplicaFinalize(t, b, id, token)
		wantUploadReplicaGone(t, "finalize replay on replica B", code, body)
	})

	t.Run("a process started after the issuer stops verifies its token", func(t *testing.T) {
		id, token := uploadReplicaCreate(t, a)
		gwA.Stop(t)
		gwC := gateway.StartWith(t, args...)
		c := gwC.BaseURL()
		if code, body := uploadReplicaUpload(t, c, id, token); code != http.StatusCreated {
			t.Fatalf("upload on the replacement gateway: status %d: %s\n%s", code, body, gwC.Logs())
		}
		if code, body := uploadReplicaFinalize(t, c, id, token); code != http.StatusOK {
			t.Fatalf("finalize on the replacement gateway: status %d: %s\n%s", code, body, gwC.Logs())
		}
		code, body := uploadReplicaFinalize(t, b, id, token)
		wantUploadReplicaGone(t, "finalize replay on replica B", code, body)
	})
}
