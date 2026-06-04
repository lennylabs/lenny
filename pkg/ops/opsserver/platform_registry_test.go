// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/registryservice"
)

func newRegistryServer(withStore bool) *opsserver.Server {
	opts := registryservice.Options{
		Base: registryservice.EffectiveConfig{URL: "ghcr.io/lennylabs", PullSecretName: "lenny-pull"},
	}
	if withStore {
		opts.Store = registryservice.NewMemoryStore()
	}
	return opsserver.New(opsserver.Options{Registry: registryservice.New(opts)})
}

// spec: §25.8 line 3362 — GET returns the effective registry config with
// the pull-secret name.
func TestRegistryGet_ReturnsEffective_spec_25_8(t *testing.T) {
	s := newRegistryServer(true)
	w := do(s, http.MethodGet, "/v1/admin/platform/registry", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET registry = %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["url"] != "ghcr.io/lennylabs" || body["pullSecretName"] != "lenny-pull" {
		t.Fatalf("body = %v", body)
	}
	if body["source"] != "helm" {
		t.Errorf("source = %v, want helm", body["source"])
	}
}

// spec: §25.8 line 3362 — PUT persists a runtime override and the next GET
// returns it.
func TestRegistryPut_PersistsOverride_spec_25_8(t *testing.T) {
	s := newRegistryServer(true)
	w := do(s, http.MethodPut, "/v1/admin/platform/registry",
		`{"url":"mirror.internal/lenny","requireDigest":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT registry = %d body=%s", w.Code, w.Body.String())
	}
	w = do(s, http.MethodGet, "/v1/admin/platform/registry", "")
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["url"] != "mirror.internal/lenny" || body["source"] != "postgres" || body["requireDigest"] != true {
		t.Fatalf("post-put GET body = %v", body)
	}
}

// spec: §25.8 — PUT with neither url nor overrides is a 422.
func TestRegistryPut_RejectsEmpty_spec_25_8(t *testing.T) {
	s := newRegistryServer(true)
	w := do(s, http.MethodPut, "/v1/admin/platform/registry", `{}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("PUT empty = %d body=%s", w.Code, w.Body.String())
	}
}

// spec: §25.8 — without a runtime store PUT reports the registry read-only.
func TestRegistryPut_ReadOnlyWithoutStore_spec_25_8(t *testing.T) {
	s := newRegistryServer(false)
	w := do(s, http.MethodPut, "/v1/admin/platform/registry", `{"url":"x/y"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("read-only PUT = %d body=%s", w.Code, w.Body.String())
	}
}

// spec: §25.8 — the registry routes are unmapped (404) without a service.
func TestRegistry_UnmappedWithoutService_spec_25_8(t *testing.T) {
	s := opsserver.New(opsserver.Options{})
	if w := do(s, http.MethodGet, "/v1/admin/platform/registry", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unmapped GET = %d", w.Code)
	}
}
