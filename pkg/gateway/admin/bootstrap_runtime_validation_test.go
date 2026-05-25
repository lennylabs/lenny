// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// spec: §5.1 lines 132-158 — the bootstrap upsert path applies the same
// derived-runtime registration rules as POST /v1/admin/runtimes. A seed
// referencing a missing base, setting a prohibited field, or chaining a
// derivation is reported as a per-entry error instead of silently
// entering the registry (F-5.1.18).
func TestBootstrapRejectsInvalidDerivedRuntime(t *testing.T) {
	cases := []struct {
		name    string
		payload admin.RuntimePayload
	}{
		{"missing-base", admin.RuntimePayload{Name: "d1", BaseRuntime: "no-such-base"}},
		{"prohibited-image", admin.RuntimePayload{Name: "d2", BaseRuntime: "base-rt", Image: "x@sha256:a"}},
		{"prohibited-integration-level", admin.RuntimePayload{Name: "d3", BaseRuntime: "base-rt", IntegrationLevel: "full"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, _, runtimes, _, _ := newBootstrapRouter(t)
			_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "base-rt", Image: "lenny/base@sha256:abc"})

			body := admin.BootstrapRequest{Runtimes: []admin.RuntimePayload{tc.payload}}
			buf, _ := json.Marshal(body)
			req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(buf)))
			rr := httptest.NewRecorder()
			router.Handler().ServeHTTP(rr, req)

			var resp admin.BootstrapResponse
			_ = json.Unmarshal(rr.Body.Bytes(), &resp)
			if len(resp.Runtimes.Errors) != 1 {
				t.Fatalf("expected 1 runtime error, got %+v (status %d, body %s)", resp.Runtimes, rr.Code, rr.Body.String())
			}
			// The invalid runtime must not have been persisted.
			if _, err := runtimes.Get(context.Background(), tc.payload.Name); err == nil {
				t.Errorf("invalid derived runtime %q was persisted", tc.payload.Name)
			}
		})
	}
}

// spec: §5.1 line 199 — the bootstrap path also enforces the derived
// runtimeOptionsSchema property-subset rule.
func TestBootstrapRejectsForbiddenOptionsSchemaProperty(t *testing.T) {
	router, _, runtimes, _, _ := newBootstrapRouter(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name:                 "base-rt",
		Image:                "lenny/base@sha256:abc",
		RuntimeOptionsSchema: json.RawMessage(`{"properties":{"model":{}}}`),
	})
	body := admin.BootstrapRequest{Runtimes: []admin.RuntimePayload{{
		Name: "derived", BaseRuntime: "base-rt",
		RuntimeOptionsSchema: json.RawMessage(`{"properties":{"temperature":{}}}`),
	}}}
	buf, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(buf)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)

	var resp admin.BootstrapResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Runtimes.Errors) != 1 || !strings.Contains(resp.Runtimes.Errors[0].Message, "forbidden property") {
		t.Errorf("expected forbidden-property error, got %+v", resp.Runtimes)
	}
}
