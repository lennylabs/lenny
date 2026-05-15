// SPDX-License-Identifier: MIT

package ctl_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/ctl"
)

func TestDoSendsBearer(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "yes"})
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL, Bearer: "tok-123"})
	var out map[string]string
	if err := c.Do(context.Background(), http.MethodGet, "/v1/admin/tenants", nil, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization: %q", gotAuth)
	}
	if out["ok"] != "yes" {
		t.Errorf("response not decoded: %+v", out)
	}
}

func TestDoSendsDevHeaders(t *testing.T) {
	var tenant, roles string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant = r.Header.Get("X-Lenny-Tenant-ID")
		roles = r.Header.Get("X-Lenny-Roles")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL, DevTenant: "platform", DevRoles: "platform-admin"})
	if err := c.Do(context.Background(), http.MethodGet, "/v1/admin/tenants", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if tenant != "platform" || roles != "platform-admin" {
		t.Errorf("dev headers: tenant=%q roles=%q", tenant, roles)
	}
}

func TestDoBearerWinsOverDevHeaders(t *testing.T) {
	var gotAuth, gotRoles string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotRoles = r.Header.Get("X-Lenny-Roles")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL, Bearer: "tok", DevTenant: "x", DevRoles: "platform-admin"})
	_ = c.Do(context.Background(), http.MethodGet, "/x", nil, nil)
	if gotAuth != "Bearer tok" {
		t.Errorf("bearer not sent: %q", gotAuth)
	}
	if gotRoles != "" {
		t.Errorf("dev roles should not be sent when bearer is set: %q", gotRoles)
	}
}

func TestDoDecodesAPIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "FORBIDDEN", "message": "nope"},
		})
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL})
	err := c.Do(context.Background(), http.MethodGet, "/v1/admin/tenants", nil, nil)
	var apiErr *ctl.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.Status != http.StatusForbidden || apiErr.Code != "FORBIDDEN" {
		t.Errorf("APIError: %+v", apiErr)
	}
}

func TestDoSendsJSONBody(t *testing.T) {
	var gotBody map[string]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type: %q", ct)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL})
	err := c.Do(context.Background(), http.MethodPost, "/v1/admin/tenants",
		map[string]string{"id": "acme"}, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotBody["id"] != "acme" {
		t.Errorf("body not sent: %+v", gotBody)
	}
}
