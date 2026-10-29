// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for runtime workspaceDefaults: the plan bound at
// finalize carries the runtime's default files ahead of the client's sources,
// and the finalize barrier materializes both into the session's slot through
// the real adapter, so the §14.1 path collision rule lets a client upload
// replace a default file at the same path.

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime)
// diagnosis: a missing defaults.txt in the slot means a binding request
// dropped the runtime's workspaceDefaults instead of composing them into the
// bound plan. Default content in CLAUDE.md means the defaults were placed
// after the client's upload source rather than ahead of it, so the §14.1
// path collision rule let the default win.
func TestRuntimeDefaultsMaterializeAtFinalize_spec_14_1(t *testing.T) {
	t.Parallel()
	rt := &eagerRuntime{}
	wsRoot := t.TempDir()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceBase = wsRoot
	adapterSrv.CredentialsDir = t.TempDir()
	adapterSrv.Runtime = rt

	cluster := eagerCluster(t)
	blobs := blobstore.NewMemoryStore(nil)
	binder := &podsession.Binder{
		Client:           cluster,
		Namespace:        eagerNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      eagerAdapterDialer(t, adapterSrv),
		Blobs:            blobs,
		Credentials:      &recordingAssigner{},
	}

	tenants, runtimes, credPools := eagerCredentialStores(t)
	if _, err := runtimes.Update(context.Background(), "echo", func(r *runtimestore.Runtime) error {
		r.WorkspaceDefaults = &runtimestore.WorkspaceDefaults{Files: []runtimestore.WorkspaceFile{
			{Path: "CLAUDE.md", Content: "default instructions"},
			{Path: "defaults.txt", Content: "default content"},
		}}
		return nil
	}); err != nil {
		t.Fatalf("declare workspaceDefaults: %v", err)
	}

	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader:         true,
		IDFunc:                  func() string { return "sess-defaults" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          eagerNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
		Blobs:                   blobs,
	})
	h := srv.Handler()
	post := func(path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	if rr := post("/v1/sessions", createBody, nil); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	rr := post("/v1/sessions/sess-defaults/upload", []byte("# uploaded"), map[string]string{"Content-Type": "text/markdown"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var up sessionserver.UploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &up); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	finalizeBody, _ := json.Marshal(map[string]any{
		"workspacePlan": map[string]any{
			"schemaVersion": 1,
			"sources":       []map[string]any{{"type": "uploadFile", "path": "CLAUDE.md", "uploadRef": up.UploadRef}},
		},
	})
	if rr := post("/v1/sessions/sess-defaults/finalize", finalizeBody, map[string]string{"Content-Type": "application/json"}); rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, body=%s", rr.Code, rr.Body.String())
	}

	current := filepath.Join(wsRoot, "slots", "sess-defaults", "current")
	got, err := os.ReadFile(filepath.Join(current, "defaults.txt"))
	if err != nil {
		t.Fatalf("defaults.txt was not materialized: %v", err)
	}
	if string(got) != "default content" {
		t.Errorf("defaults.txt = %q, want the default content", got)
	}
	got, err = os.ReadFile(filepath.Join(current, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("CLAUDE.md was not materialized: %v", err)
	}
	if string(got) != "# uploaded" {
		t.Errorf("CLAUDE.md = %q, want the uploaded content to win over the default", got)
	}
}
