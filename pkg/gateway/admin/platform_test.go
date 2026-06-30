// SPDX-License-Identifier: MIT

package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

func TestPlatformVersion(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{}).
		WithPlatformInfo(admin.PlatformInfo{
			Version:   "1.2.3",
			GitCommit: "abc123",
			BuildDate: "2026-01-01",
		}, nil)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/platform/version", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp admin.PlatformVersion
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.GatewayVersion != "1.2.3" || resp.GitCommit != "abc123" {
		t.Errorf("version: %+v", resp)
	}
	if resp.GoVersion == "" {
		t.Error("goVersion should be populated")
	}
}

func TestPlatformVersionDefaultsWhenUnset(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{}).
		WithPlatformInfo(admin.PlatformInfo{}, nil)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/platform/version", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	var resp admin.PlatformVersion
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.GatewayVersion != "dev" || resp.GitCommit != "unknown" {
		t.Errorf("defaults: %+v", resp)
	}
}

func TestPlatformConfigRedactsSecrets(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{}).
		WithPlatformInfo(admin.PlatformInfo{}, map[string]string{
			"gateway.addr":      ":8080",
			"jwt.signingSecret": "super-secret-value",
			"redis.password":    "hunter2",
			"upload.tokenKey":   "raw-key-bytes",
			"oauth.clientId":    "public-client-id",
		})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/platform/config", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Config []admin.PlatformConfigEntry `json:"config"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	got := map[string]string{}
	for _, e := range resp.Config {
		got[e.Key] = e.Value
	}
	if got["gateway.addr"] != ":8080" {
		t.Errorf("non-secret should pass through: %q", got["gateway.addr"])
	}
	for _, secretKey := range []string{"jwt.signingSecret", "redis.password", "upload.tokenKey"} {
		if got[secretKey] != "***" {
			t.Errorf("%s should be redacted, got %q", secretKey, got[secretKey])
		}
	}
}

func TestPlatformEndpointsRequirePlatformAdmin(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{}).
		WithPlatformInfo(admin.PlatformInfo{}, nil)
	for _, path := range []string{"/v1/admin/platform/version", "/v1/admin/platform/config"} {
		req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodGet, path, nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: got %d, want 403", path, rr.Code)
		}
	}
}

func TestPlatformEndpointsAbsentWhenNotWired(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/platform/version", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unwired platform endpoint: got %d, want 404", rr.Code)
	}
}
