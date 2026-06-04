// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// dialRealAdapter starts a real adapter.Server (which actually materializes
// the workspace on disk) on an in-process listener and returns a connected
// adapterclient.Client. F-7.4.6.
func dialRealAdapter(t *testing.T, srv *adapter.Server) *adapterclient.Client {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	g := grpc.NewServer()
	adapterv1.RegisterAdapterServer(g, srv)
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(func() { g.GracefulStop() })

	cl, err := adapterclient.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { cl.Close() })
	return cl
}

// midSessionFixture wires a running session bound to a real adapter pod
// whose workspace lives under root, with a runtime registry declaring the
// capability per `capability` and the deployer policy per `policyEnabled`.
func midSessionFixture(t *testing.T, capability, policyEnabled bool, withBinding bool) (http.Handler, string) {
	t.Helper()
	store := memstore.New()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_mid", TenantID: "acme", UserID: "alice", RuntimeRef: "rt-mid",
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "rt-mid", Type: runtimestore.TypeAgent,
		Capabilities: &runtimestore.RuntimeCapabilities{MidSessionUpload: capability},
	}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := podsession.NewRegistry()
	if withBinding {
		ad := dialRealAdapter(t, &adapter.Server{WorkspaceRoot: root, StagingDir: t.TempDir()})
		reg.Put(&podsession.BindResult{SessionID: "sess_mid", TenantID: "acme", Adapter: ad})
	}

	srv := sessionserver.New(store, sessionserver.Options{
		PodRegistry:             reg,
		Runtimes:                runtimes,
		MidSessionUploadEnabled: policyEnabled,
	})
	return srv.Handler(), root
}

func uploadToSession(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_mid/upload-to-session", strings.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// TestUploadToSessionHappyPath overlays a file into a running session's
// workspace when both the runtime capability and the deployer policy admit
// it. spec: §7.4 line 433 — F-7.4.6.
func TestUploadToSessionHappyPath_spec_7_4_433(t *testing.T) {
	h, root := midSessionFixture(t, true, true, true)
	body := `{"files":[{"path":"docs/new.md","content":"` + b64("fresh") + `","mode":"644"}]}`
	rr := uploadToSession(t, h, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.UploadToSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "filesUpdated" || resp.Files != 1 {
		t.Errorf("response = %+v, want {filesUpdated 1}", resp)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "docs", "new.md")); string(b) != "fresh" {
		t.Errorf("overlaid file = %q, want fresh", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "existing.txt")); string(b) != "keep" {
		t.Errorf("mid-session upload clobbered the existing workspace file: %q", b)
	}
}

// TestUploadToSessionRejectedWhenPolicyDisabled asserts the deployer policy
// gates the surface: with the runtime declaring the capability but the
// policy off, a running-session upload is rejected by the precondition.
// spec: §7.4 line 433 — F-7.4.6.
func TestUploadToSessionRejectedWhenPolicyDisabled_spec_7_4_433(t *testing.T) {
	h, _ := midSessionFixture(t, true, false, true)
	rr := uploadToSession(t, h, `{"files":[{"path":"a.txt","content":"`+b64("x")+`"}]}`)
	if rr.Code == http.StatusOK {
		t.Fatalf("status = 200, want a precondition rejection; body=%s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "INVALID_STATE_TRANSITION") {
		t.Errorf("expected INVALID_STATE_TRANSITION, got %s", rr.Body.String())
	}
}

// TestUploadToSessionRejectedWhenRuntimeLacksCapability asserts the runtime
// capability is required even when the deployer policy is on. spec: §7.4
// line 433 — F-7.4.6.
func TestUploadToSessionRejectedWhenRuntimeLacksCapability_spec_7_4_433(t *testing.T) {
	h, _ := midSessionFixture(t, false, true, true)
	rr := uploadToSession(t, h, `{"files":[{"path":"a.txt","content":"`+b64("x")+`"}]}`)
	if rr.Code == http.StatusOK {
		t.Fatalf("status = 200, want a precondition rejection; body=%s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "INVALID_STATE_TRANSITION") {
		t.Errorf("expected INVALID_STATE_TRANSITION, got %s", rr.Body.String())
	}
}

// TestUploadToSessionNoLiveBinding asserts a session with no live pod
// binding on this replica is rejected with TARGET_NOT_READY (the capability
// and policy both admit it). spec: §7.4 line 433 — F-7.4.6.
func TestUploadToSessionNoLiveBinding_spec_7_4_433(t *testing.T) {
	h, _ := midSessionFixture(t, true, true, false)
	rr := uploadToSession(t, h, `{"files":[{"path":"a.txt","content":"`+b64("x")+`"}]}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "TARGET_NOT_READY") {
		t.Errorf("expected TARGET_NOT_READY, got %s", rr.Body.String())
	}
}

// TestUploadToSessionValidation covers the §7.4 enforcement-rule rejections
// the gateway pre-checks before any pod round-trip. F-7.4.6.
func TestUploadToSessionValidation_spec_7_4(t *testing.T) {
	cases := []struct {
		name, body, wantCode string
		status               int
	}{
		{"empty files", `{"files":[]}`, "VALIDATION_ERROR", http.StatusBadRequest},
		{"bad base64", `{"files":[{"path":"a.txt","content":"!!!notb64"}]}`, "VALIDATION_ERROR", http.StatusBadRequest},
		{"traversal", `{"files":[{"path":"../escape","content":"` + b64("x") + `"}]}`, "VALIDATION_ERROR", http.StatusBadRequest},
		{"absolute path", `{"files":[{"path":"/etc/x","content":"` + b64("x") + `"}]}`, "VALIDATION_ERROR", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := midSessionFixture(t, true, true, true)
			rr := uploadToSession(t, h, tc.body)
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.status, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tc.wantCode) {
				t.Errorf("body = %s, want code %s", rr.Body.String(), tc.wantCode)
			}
		})
	}
}

// TestRuntimeDiscoveryExposesMidSessionUpload asserts the §7.4 line 433
// footnote: clients discover the capability via GET /v1/runtimes. F-7.4.6.
func TestRuntimeDiscoveryExposesMidSessionUpload_spec_7_4_433(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "rt-yes", Type: runtimestore.TypeAgent,
		Capabilities: &runtimestore.RuntimeCapabilities{MidSessionUpload: true},
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "rt-no", Type: runtimestore.TypeAgent,
	})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Runtimes: runtimes})

	req := httptest.NewRequest(http.MethodGet, "/v1/runtimes", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var doc struct {
		Runtimes []sessionserver.RuntimeDiscoveryEntry `json:"runtimes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[string]*sessionserver.RuntimeDiscoveryCapabilities{}
	for _, e := range doc.Runtimes {
		got[e.Name] = e.Capabilities
	}
	if c := got["rt-yes"]; c == nil || !c.MidSessionUpload {
		t.Errorf("rt-yes capabilities = %+v, want midSessionUpload true", c)
	}
	if c := got["rt-no"]; c != nil {
		t.Errorf("rt-no capabilities = %+v, want omitted", c)
	}
}
