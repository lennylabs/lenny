// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken"
)

// fakeProvisioner is a test double for admin.AdminTokenProvisioner. It
// records calls and returns a scripted Created result.
type fakeProvisioner struct {
	created      bool
	provisionErr error
	provisions   int
	rotations    int
}

func (f *fakeProvisioner) Provision(context.Context) (admintoken.Result, error) {
	f.provisions++
	if f.provisionErr != nil {
		return admintoken.Result{}, f.provisionErr
	}
	return admintoken.Result{Created: f.created}, nil
}

func (f *fakeProvisioner) Rotate(context.Context) (admintoken.Result, error) {
	f.rotations++
	return admintoken.Result{Created: true}, nil
}

func (f *fakeProvisioner) Username() string            { return "lenny-admin" }
func (f *fakeProvisioner) SecretRef() (string, string) { return "lenny-system", "lenny-admin-token" }

func newAdminTokenRouter(t *testing.T, prov admin.AdminTokenProvisioner) *admin.Router {
	t.Helper()
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC) },
	}).WithAdminTokenProvisioner(prov)
}

func postBootstrapAT(t *testing.T, router *admin.Router, query string) admin.BootstrapResponse {
	t.Helper()
	buf, _ := json.Marshal(admin.BootstrapRequest{})
	url := "/v1/admin/bootstrap"
	if query != "" {
		url += "?" + query
	}
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, url, bytes.NewReader(buf)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.BootstrapResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// spec: §17.6 line 473 — a first run reports SecretCreated=true so the
// CLI prints the first-use prompt. F-17.6.3 / F-24.1.7.
func TestBootstrapProvisionsAdminTokenOnFirstRun_spec_17_6_473(t *testing.T) {
	prov := &fakeProvisioner{created: true}
	resp := postBootstrapAT(t, newAdminTokenRouter(t, prov), "")
	if prov.provisions != 1 {
		t.Fatalf("Provision called %d times, want 1", prov.provisions)
	}
	if resp.AdminToken == nil || !resp.AdminToken.SecretCreated {
		t.Fatalf("adminToken section = %+v, want secretCreated=true", resp.AdminToken)
	}
	if resp.AdminToken.SecretName != "lenny-admin-token" || resp.AdminToken.SecretNamespace != "lenny-system" {
		t.Errorf("secret ref = %s/%s", resp.AdminToken.SecretNamespace, resp.AdminToken.SecretName)
	}
}

// spec: §17.6 line 459 — a re-run reports SecretCreated=false so the CLI
// prints the "already exists" message. F-24.1.7.
func TestBootstrapAdminTokenReRunReportsExisting_spec_17_6_459(t *testing.T) {
	prov := &fakeProvisioner{created: false}
	resp := postBootstrapAT(t, newAdminTokenRouter(t, prov), "")
	if resp.AdminToken == nil || resp.AdminToken.SecretCreated {
		t.Fatalf("adminToken section = %+v, want secretCreated=false", resp.AdminToken)
	}
}

// A dry-run must not provision the credential. spec: §15.1 line 1140.
func TestBootstrapDryRunSkipsAdminToken(t *testing.T) {
	prov := &fakeProvisioner{created: true}
	resp := postBootstrapAT(t, newAdminTokenRouter(t, prov), "dryRun=true")
	if prov.provisions != 0 {
		t.Errorf("dry-run called Provision %d times, want 0", prov.provisions)
	}
	if resp.AdminToken != nil {
		t.Errorf("dry-run adminToken section = %+v, want nil", resp.AdminToken)
	}
}

// spec: §17.6 — the rotate-token route rotates the managed admin
// user and rejects any other user with 404. F-17.6.3.
func TestRotateTokenRoute_spec_17_6(t *testing.T) {
	prov := &fakeProvisioner{}
	router := newAdminTokenRouter(t, prov)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/users/lenny-admin/rotate-token", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if prov.rotations != 1 {
		t.Errorf("Rotate called %d times, want 1", prov.rotations)
	}

	// A non-managed user is not rotatable.
	req = withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/users/alice/rotate-token", nil))
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("rotate other user status = %d, want 404", rr.Code)
	}
}
