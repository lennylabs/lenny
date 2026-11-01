// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// adminProbeWire is an admin Router whose Secret probe is the production
// prober over the production Token Service gRPC server, with pools recording
// what a write committed.
type adminProbeWire struct {
	handler http.Handler
	pools   credentialpoolstore.Store
}

// newAdminProbeWire wires the production prober to the admin Router as the
// composition root does, over a Token Service reading cs.
func newAdminProbeWire(t *testing.T, cs *fake.Clientset) adminProbeWire {
	t.Helper()
	pools := credentialpoolstore.NewMemory()
	r := admin.NewRouter(tenantstore.NewMemory(), admin.Options{CredentialsNamespace: testCredNS}).
		WithCredentialPools(pools).
		WithSecretAccessProber(newTokenServiceSecretProber(dialVCSWireTokenService(t, cs), testCredNS))
	return adminProbeWire{handler: r.Handler(), pools: pools}
}

// createPoolWithRef posts a pool holding one credential whose secretRef is
// ref, as a platform-admin.
func (w adminProbeWire) createPoolWithRef(t *testing.T, ref string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(admin.CredentialPoolPayload{
		TenantID: "acme", Name: "wire-pool", Provider: "anthropic_direct",
		AssignmentStrategy: "least-loaded", MaxConcurrentSessions: 10,
		Credentials: []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: ref}},
	})
	if err != nil {
		t.Fatalf("marshal pool: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credential-pools", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		TenantID: "platform", Subject: "admin@acme.com",
		Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, req)
	return rec
}

// errorCode returns the error code of a response body.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

// spec: §4.9 (Credential Storage Topology: admin-time Secret probe), §15.1
// (CREDENTIAL_PROBE_UNAVAILABLE, CREDENTIAL_SECRET_RBAC_MISSING) — the admin
// handler, the production prober, and the production Token Service agree over
// gRPC. A Secret get that fails transiently crosses the wire as Unavailable
// and reaches the client as 503 CREDENTIAL_PROBE_UNAVAILABLE with no row
// committed, a definitive miss is 422, and a present Secret commits the row.
func TestAdminSecretProbeOverTokenServiceWire(t *testing.T) {
	t.Run("transient Secret get is 503 CREDENTIAL_PROBE_UNAVAILABLE", func(t *testing.T) {
		cs := vcsWireClientset()
		cs.PrependReactor("get", "secrets", func(ktesting.Action) (bool, runtime.Object, error) {
			return true, nil, k8serrors.NewServiceUnavailable("etcd leader change")
		})
		w := newAdminProbeWire(t, cs)
		rec := w.createPoolWithRef(t, "gh-token")
		if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "CREDENTIAL_PROBE_UNAVAILABLE" {
			t.Fatalf("status %d body %s, want 503 CREDENTIAL_PROBE_UNAVAILABLE", rec.Code, rec.Body)
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Fatalf("503 carries no Retry-After; headers %v", rec.Header())
		}
		if _, err := w.pools.Get(context.Background(), "acme", "wire-pool"); err == nil {
			t.Fatal("a pool row committed after a probe with no verdict")
		}
	})
	t.Run("absent Secret is 422 CREDENTIAL_SECRET_RBAC_MISSING", func(t *testing.T) {
		rec := newAdminProbeWire(t, vcsWireClientset()).createPoolWithRef(t, "absent")
		if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "CREDENTIAL_SECRET_RBAC_MISSING" {
			t.Fatalf("status %d body %s, want 422 CREDENTIAL_SECRET_RBAC_MISSING", rec.Code, rec.Body)
		}
	})
	t.Run("present Secret commits the pool", func(t *testing.T) {
		rec := newAdminProbeWire(t, vcsWireClientset()).createPoolWithRef(t, "gh-token")
		if rec.Code != http.StatusCreated {
			t.Fatalf("status %d body %s, want 201", rec.Code, rec.Body)
		}
	})
}
