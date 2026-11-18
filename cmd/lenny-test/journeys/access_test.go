// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// These tests exercise contributor tooling and carry no spec annotation.
// A fake kubectl answers the secret read and the port-forward, which
// points at an httptest gateway, so ClusterAccess runs end to end without
// a cluster.

const fakeKubectlScript = `#!/bin/sh
case "$*" in
  *port-forward*)
    echo "Forwarding from 127.0.0.1:${FAKE_GW_PORT} -> 8080"
    exec sleep 30 ;;
  *"get secret lenny-admin-token"*)
    printf '%s' "${FAKE_TOKEN_B64}" ;;
  *"get fail"*)
    echo "Error from server (NotFound)" >&2
    exit 1 ;;
  *)
    echo "args: $*" ;;
esac
`

type gatewayFake struct {
	mu       sync.Mutex
	requests []string
	policy   string
}

func (g *gatewayFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	g.requests = append(g.requests, r.Method+" "+r.URL.RequestURI()+" tenant="+r.Header.Get("X-Lenny-Tenant-ID")+
		" roles="+r.Header.Get("X-Lenny-Roles")+" if-match="+r.Header.Get("If-Match")+" body="+string(body))
	switch {
	case r.URL.Path == "/v1/admin/bootstrap":
		w.WriteHeader(http.StatusMultiStatus)
	case r.URL.Path == "/v1/admin/tenants/default/rbac-config" && r.Method == http.MethodGet:
		w.Header().Set("ETag", `"v1"`)
		_ = json.NewEncoder(w).Encode(map[string]any{"noEnvironmentPolicy": g.policy, "other": 1})
	case r.URL.Path == "/v1/admin/tenants/default/rbac-config" && r.Method == http.MethodPut:
		var p map[string]any
		_ = json.Unmarshal(body, &p)
		g.policy, _ = p["noEnvironmentPolicy"].(string)
		w.WriteHeader(http.StatusOK)
	case r.URL.Path == "/v1/admin/tenants/x":
		_, _ = w.Write([]byte(`{"id":"x"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func accessWithFakeKubectl(t *testing.T, g *gatewayFake) *ClusterAccess {
	t.Helper()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_GW_PORT", u.Port())
	t.Setenv("FAKE_TOKEN_B64", "YWRtaW4tdG9rZW4=") // admin-token
	bin := filepath.Join(t.TempDir(), "kubectl")
	if err := os.WriteFile(bin, []byte(fakeKubectlScript), 0o755); err != nil {
		t.Fatal(err)
	}
	a := NewClusterAccess("/trial/kubeconfig")
	a.KubectlBin = bin
	t.Cleanup(a.Close)
	return a
}

func TestClusterAccessRunsKubectlWithTheJourneyKubeconfig(t *testing.T) {
	a := accessWithFakeKubectl(t, &gatewayFake{})
	out, err := a.Kubectl(context.Background(), []string{"get", "pods"})
	if err != nil || !strings.Contains(out, "--kubeconfig /trial/kubeconfig get pods") {
		t.Fatalf("out = %q, %v", out, err)
	}
	if _, err := a.Kubectl(context.Background(), []string{"get", "fail"}); err == nil || !strings.Contains(err.Error(), "NotFound") {
		t.Errorf("a failing kubectl: %v", err)
	}
}

func TestClusterAccessSendsAPIRequestsThroughTheForwardWithDevIdentity(t *testing.T) {
	g := &gatewayFake{}
	a := accessWithFakeKubectl(t, g)
	status, body, err := a.API(context.Background(), APIRequest{Method: "GET", Path: "/v1/admin/tenants/x", As: "platform-admin"})
	if err != nil || status != 200 || !strings.Contains(body, `"id":"x"`) {
		t.Fatalf("API = %d %q %v", status, body, err)
	}
	if _, _, err := a.API(context.Background(), APIRequest{Method: "GET", Path: "/v1/missing", As: "acme/alice/user"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.API(context.Background(), APIRequest{Method: "GET", Path: "/x", As: "nobody"}); err == nil {
		t.Error("an unparseable identity was sent")
	}
	if len(g.requests) != 2 || !strings.Contains(g.requests[0], "tenant=platform roles=platform-admin") || !strings.Contains(g.requests[1], "tenant=acme roles=user") {
		t.Errorf("requests = %q", g.requests)
	}
}

func TestClusterAccessPrepareSeedsTheTenantAndAllowsSessions(t *testing.T) {
	g := &gatewayFake{policy: "deny-all"}
	a := accessWithFakeKubectl(t, g)
	if err := a.Prepare(context.Background(), "sessions-allowed:default"); err != nil {
		t.Fatal(err)
	}
	if g.policy != "allow-all" {
		t.Fatalf("policy = %q after preparing", g.policy)
	}
	put := g.requests[len(g.requests)-1]
	if !strings.Contains(put, `PUT /v1/admin/tenants/default/rbac-config`) || !strings.Contains(put, `if-match="v1"`) || !strings.Contains(put, `"other":1`) {
		t.Errorf("the PUT does not carry the ETag and the other fields: %s", put)
	}
	n := len(g.requests)
	if err := a.Prepare(context.Background(), "sessions-allowed:default"); err != nil {
		t.Fatal(err)
	}
	if len(g.requests) != n+2 {
		t.Errorf("a tenant already on allow-all is left alone; requests: %q", g.requests[n:])
	}
	if err := a.Prepare(context.Background(), "sessions-allowed:acme"); err == nil {
		t.Error("a tenant whose rbac-config does not answer was prepared")
	}
}

func TestClusterAccessReadsTheAdminToken(t *testing.T) {
	a := accessWithFakeKubectl(t, &gatewayFake{})
	tok, err := a.AdminToken(context.Background())
	if err != nil || tok != "admin-token" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	t.Setenv("FAKE_TOKEN_B64", "")
	if _, err := a.AdminToken(context.Background()); err == nil {
		t.Error("an empty token was accepted")
	}
	t.Setenv("FAKE_TOKEN_B64", "!!!")
	if _, err := a.AdminToken(context.Background()); err == nil {
		t.Error("a token that is not base64 was accepted")
	}
}
