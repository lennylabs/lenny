// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// TestRuntimeDiscoveryExposesMidSessionUpload asserts the §7.4
// footnote: clients discover the capability via GET /v1/runtimes. F-7.4.6.
func TestRuntimeDiscoveryExposesMidSessionUpload(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "rt-yes", Type: runtimestore.TypeAgent,
		Capabilities: &runtimestore.RuntimeCapabilities{MidSessionUpload: true},
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "rt-no", Type: runtimestore.TypeAgent,
	})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true, Runtimes: runtimes})

	req := httptest.NewRequest(http.MethodGet, "/v1/runtimes", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	asOwner(srv.Handler()).ServeHTTP(rr, req)
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
