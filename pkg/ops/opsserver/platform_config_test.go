// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/configservice"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// stubGateway is a configservice.GatewayConfig over a fixed running
// config that records applies.
type stubGateway struct {
	running map[string]any
	restart bool
	applied map[string]any
}

func (g *stubGateway) GetConfig(context.Context) (map[string]any, error) {
	return g.running, nil
}

func (g *stubGateway) ApplyConfig(_ context.Context, desired map[string]any) (bool, error) {
	g.applied = desired
	return g.restart, nil
}

type stubValidator struct {
	errs []configservice.ValidationError
}

func (v stubValidator) Validate(map[string]any) []configservice.ValidationError { return v.errs }

func configServer(t *testing.T, gw *stubGateway, validator configservice.Validator) *opsserver.Server {
	t.Helper()
	svc := configservice.New(configservice.Options{Gateway: gw, Validator: validator})
	return opsserver.New(opsserver.Options{PlatformConfig: svc})
}

// TestConfigDiffReturnsChanges_spec_25_8 covers GET
// /v1/admin/platform/config/diff returning the field diff.
func TestConfigDiffReturnsChanges_spec_25_8(t *testing.T) {
	srv := configServer(t, &stubGateway{running: map[string]any{"a": "1"}}, nil)
	rec, body := doJSON(t, srv, http.MethodGet, "/v1/admin/platform/config/diff", nil,
		map[string]any{"desired": map[string]any{"a": "2"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	changes, _ := body["changes"].([]any)
	if len(changes) != 1 {
		t.Fatalf("changes = %v, want 1", body["changes"])
	}
	if body["inSync"] != false {
		t.Errorf("inSync = %v, want false", body["inSync"])
	}
}

// TestConfigApplyDryRun_spec_25_8 covers PUT /v1/admin/platform/config
// without confirm: a dry-run preview that does not proxy the apply.
func TestConfigApplyDryRun_spec_25_8(t *testing.T) {
	gw := &stubGateway{running: map[string]any{"a": "1"}}
	srv := configServer(t, gw, nil)
	rec, body := doJSON(t, srv, http.MethodPut, "/v1/admin/platform/config", nil,
		map[string]any{"desired": map[string]any{"a": "2"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["dryRun"] != true || body["applied"] != false {
		t.Fatalf("expected dry-run, got %v", body)
	}
	if gw.applied != nil {
		t.Fatalf("dry-run must not proxy apply")
	}
}

// TestConfigApplyConfirm_spec_25_8 covers a confirmed apply proxying to
// the gateway.
func TestConfigApplyConfirm_spec_25_8(t *testing.T) {
	gw := &stubGateway{running: map[string]any{"a": "1"}}
	srv := configServer(t, gw, nil)
	rec, body := doJSON(t, srv, http.MethodPut, "/v1/admin/platform/config", nil,
		map[string]any{"desired": map[string]any{"a": "2"}, "confirm": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if body["applied"] != true {
		t.Fatalf("expected applied, got %v", body)
	}
	if gw.applied["a"] != "2" {
		t.Fatalf("apply not proxied: %v", gw.applied)
	}
}

// TestConfigApplyValidationFailed_spec_25_8 covers 422
// CONFIG_VALIDATION_FAILED with details.errors.
func TestConfigApplyValidationFailed_spec_25_8(t *testing.T) {
	gw := &stubGateway{running: map[string]any{}}
	srv := configServer(t, gw, stubValidator{errs: []configservice.ValidationError{{Field: "x", Message: "unknown key"}}})
	rec, body := doJSON(t, srv, http.MethodPut, "/v1/admin/platform/config", nil,
		map[string]any{"desired": map[string]any{"x": 1}, "confirm": true})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%v", rec.Code, body)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != configservice.CodeValidationFailed {
		t.Fatalf("code = %v, want %s", errObj["code"], configservice.CodeValidationFailed)
	}
	details, _ := errObj["details"].(map[string]any)
	if errs, _ := details["errors"].([]any); len(errs) != 1 {
		t.Fatalf("details.errors = %v, want 1", details["errors"])
	}
}

// TestConfigApplyRestartRequired_spec_25_8 covers 422
// CONFIG_RESTART_REQUIRED when a confirmed apply needs a gateway restart.
func TestConfigApplyRestartRequired_spec_25_8(t *testing.T) {
	gw := &stubGateway{running: map[string]any{"a": "1"}, restart: true}
	srv := configServer(t, gw, nil)
	rec, body := doJSON(t, srv, http.MethodPut, "/v1/admin/platform/config", nil,
		map[string]any{"desired": map[string]any{"a": "2"}, "confirm": true})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%v", rec.Code, body)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != configservice.CodeRestartRequired {
		t.Fatalf("code = %v, want %s", errObj["code"], configservice.CodeRestartRequired)
	}
	if gw.applied == nil {
		t.Fatalf("restart-required apply must still proxy the change")
	}
}

// TestConfigRoutesUnmappedWhenUnconfigured_spec_25_8 covers the
// cold-start posture: no config service leaves the routes unmapped (404).
func TestConfigRoutesUnmappedWhenUnconfigured_spec_25_8(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	rec, _ := doJSON(t, srv, http.MethodPut, "/v1/admin/platform/config", nil,
		map[string]any{"desired": map[string]any{"a": "2"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when unconfigured", rec.Code)
	}
}
