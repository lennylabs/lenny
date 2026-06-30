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

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/environment/deploymentconfigstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// newDeploymentAdminServer wires a Router with a real in-memory audit
// chain (so the §16.7 deployment-transition events land on per-tenant
// chains with real row IDs) and a deploymentconfigstore baseline.
func newDeploymentAdminServer(t *testing.T) (http.Handler, *tenantstore.Memory, *deploymentconfigstore.Memory, *audit.ChainSet) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	chains := audit.NewChainSet()
	dc := deploymentconfigstore.NewMemory()
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithSIEMConfigured(true).WithPgauditConfigured(true).
		WithAuditChains(chains).
		WithDeploymentConfig(dc)
	return router.Handler(), tenants, dc, chains
}

func postConfigChange(t *testing.T, h http.Handler, body admin.DeploymentConfigChangeRequest, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := as(httptest.NewRequest(http.MethodPost, "/v1/admin/deployment/config-change", bytes.NewReader(b)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// parsedRow is a committed audit row with its `detail` payload extracted.
type parsedRow struct {
	id        string
	eventType string
	detail    map[string]any
}

func rowsFor(t *testing.T, chains *audit.ChainSet, tenant string) []parsedRow {
	t.Helper()
	c := chains.Chain(tenant)
	if c == nil {
		return nil
	}
	var out []parsedRow
	for _, r := range c.Rows() {
		var fields struct {
			Detail map[string]any `json:"detail"`
		}
		_ = json.Unmarshal(r.Payload, &fields)
		out = append(out, parsedRow{id: r.ID, eventType: r.EventType, detail: fields.Detail})
	}
	return out
}

func findRow(rows []parsedRow, eventType string) (parsedRow, bool) {
	for _, r := range rows {
		if r.eventType == eventType {
			return r, true
		}
	}
	return parsedRow{}, false
}

func firstInstallRequest() admin.DeploymentConfigChangeRequest {
	var req admin.DeploymentConfigChangeRequest
	req.ReleaseRevision = 1
	req.CycleDetection.Mode = "enforce"
	req.AllowSelfRecursion.Value = "no"
	req.DefaultMaxDepth = 10
	req.ElicitationFloor = "off"
	return req
}

// spec: §16.7 lines 672, 676 — a first install records every deployment
// transition with a null previous value, off floor raises no tenant.
func TestDeploymentConfigFirstInstallEmitsAllTransitions_spec_16_7(t *testing.T) {
	h, _, dc, chains := newDeploymentAdminServer(t)
	rr := postConfigChange(t, h, firstInstallRequest(), withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("first install: status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	rows := rowsFor(t, chains, "platform")
	for _, want := range []string{
		"gateway.cycle_detection_mode_changed",
		"gateway.allow_self_recursion_changed",
		"gateway.default_max_depth_changed",
		"platform.elicitation_content_integrity_floor_changed",
	} {
		r, ok := findRow(rows, want)
		if !ok {
			t.Fatalf("first install must emit %s; got rows %+v", want, rows)
		}
		if want == "gateway.cycle_detection_mode_changed" {
			if r.detail["previous_mode"] != nil {
				t.Errorf("first install previous_mode = %v, want null", r.detail["previous_mode"])
			}
			if r.detail["new_mode"] != "enforce" || r.detail["changed_by_sub"] != "admin@acme.com" {
				t.Errorf("cycle event detail = %v", r.detail)
			}
		}
		if want == "platform.elicitation_content_integrity_floor_changed" {
			if r.detail["tenants_affected_count"].(float64) != 0 {
				t.Errorf("off floor must affect no tenant; detail = %v", r.detail)
			}
		}
	}
	cfg, found, _ := dc.Get(context.Background())
	if !found || cfg.LastRevision != 1 || cfg.ElicitationFloor != "off" {
		t.Errorf("baseline after first install = %+v found=%v", cfg, found)
	}
}

// spec: §16.7 — a retried hook at the same revision is an idempotent replay.
func TestDeploymentConfigReplayEmitsNothing_spec_16_7(t *testing.T) {
	h, _, _, chains := newDeploymentAdminServer(t)
	if rr := postConfigChange(t, h, firstInstallRequest(), withAdminPrincipal); rr.Code != 200 {
		t.Fatalf("seed install: %d %s", rr.Code, rr.Body.String())
	}
	before := len(rowsFor(t, chains, "platform"))
	rr := postConfigChange(t, h, firstInstallRequest(), withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("replay: status %d", rr.Code)
	}
	var resp admin.DeploymentConfigChangeResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Applied {
		t.Errorf("replay must report applied=false; resp=%+v", resp)
	}
	if after := len(rowsFor(t, chains, "platform")); after != before {
		t.Errorf("replay emitted %d new rows, want 0", after-before)
	}
}

// spec: §16.7 line 676/677 — a floor raise emits one clamp per raised
// tenant, joined to the platform event by paired_platform_event_id, and
// each clamp is written under its own tenant.
func TestDeploymentConfigFloorRaiseFansOutClamps_spec_16_7(t *testing.T) {
	h, tenants, dc, chains := newDeploymentAdminServer(t)
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "t-off", ElicitationContentIntegrity: "off"})
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "t-detect", ElicitationContentIntegrity: "detect-only"})
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "t-enforce", ElicitationContentIntegrity: "enforce"})
	// Pre-seed a baseline so only the floor transition differs.
	_ = dc.Put(context.Background(), deploymentconfigstore.Config{
		CycleDetectionMode: "enforce", AllowSelfRecursion: "no", DefaultMaxDepth: 10,
		ElicitationFloor: "off", LastRevision: 1,
	})

	var req admin.DeploymentConfigChangeRequest
	req.ReleaseRevision = 2
	req.CycleDetection.Mode = "enforce"
	req.AllowSelfRecursion.Value = "no"
	req.DefaultMaxDepth = 10
	req.ElicitationFloor = "enforce"
	rr := postConfigChange(t, h, req, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("floor raise: status %d, body %s", rr.Code, rr.Body.String())
	}

	platform := rowsFor(t, chains, "platform")
	floorRow, ok := findRow(platform, "platform.elicitation_content_integrity_floor_changed")
	if !ok {
		t.Fatal("floor raise must emit platform floor-changed event")
	}
	if floorRow.detail["previous_floor"] != "off" || floorRow.detail["new_floor"] != "enforce" {
		t.Errorf("floor detail = %v", floorRow.detail)
	}
	if floorRow.detail["tenants_affected_count"].(float64) != 2 {
		t.Errorf("tenants_affected_count = %v, want 2", floorRow.detail["tenants_affected_count"])
	}
	// Only enforce, not cycle/asr/depth, transitioned.
	if _, ok := findRow(platform, "gateway.cycle_detection_mode_changed"); ok {
		t.Error("unchanged cycle mode must not emit an event")
	}
	for _, raised := range []string{"t-off", "t-detect"} {
		clampRows := rowsFor(t, chains, raised)
		cr, ok := findRow(clampRows, "tenant.elicitation_content_integrity_floor_clamp")
		if !ok {
			t.Fatalf("raised tenant %s must carry a clamp event; rows %+v", raised, clampRows)
		}
		if cr.detail["paired_platform_event_id"] != floorRow.id {
			t.Errorf("%s clamp paired_platform_event_id = %v, want %s", raised, cr.detail["paired_platform_event_id"], floorRow.id)
		}
		if cr.detail["new_effective_mode"] != "enforce" || cr.detail["new_platform_floor"] != "enforce" {
			t.Errorf("%s clamp detail = %v", raised, cr.detail)
		}
	}
	if rows := rowsFor(t, chains, "t-enforce"); len(rows) != 0 {
		t.Errorf("already-enforce tenant must not be clamped; rows %+v", rows)
	}
	cfg, _, _ := dc.Get(context.Background())
	if cfg.ElicitationFloor != "enforce" || cfg.LastRevision != 2 {
		t.Errorf("baseline after raise = %+v", cfg)
	}
}

// spec: §16.7 line 676 — a floor lowering preserves stored modes and emits
// no per-tenant clamp.
func TestDeploymentConfigFloorLowerEmitsNoClamp_spec_16_7(t *testing.T) {
	h, tenants, dc, chains := newDeploymentAdminServer(t)
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "t-off", ElicitationContentIntegrity: "off"})
	_ = dc.Put(context.Background(), deploymentconfigstore.Config{ElicitationFloor: "enforce", LastRevision: 1})

	var req admin.DeploymentConfigChangeRequest
	req.ReleaseRevision = 2
	req.ElicitationFloor = "detect-only"
	rr := postConfigChange(t, h, req, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("floor lower: status %d, body %s", rr.Code, rr.Body.String())
	}
	floorRow, ok := findRow(rowsFor(t, chains, "platform"), "platform.elicitation_content_integrity_floor_changed")
	if !ok {
		t.Fatal("floor lowering still emits the platform floor-changed event")
	}
	if floorRow.detail["tenants_affected_count"].(float64) != 0 {
		t.Errorf("lowering affects no tenant; detail = %v", floorRow.detail)
	}
	if rows := rowsFor(t, chains, "t-off"); len(rows) != 0 {
		t.Errorf("floor lowering must not clamp any tenant; rows %+v", rows)
	}
}

// spec: §16.7 line 672 — a transition into warn/permissive requires a
// justification; the endpoint rejects an empty one.
func TestDeploymentConfigCycleModeJustificationRequired_spec_16_7(t *testing.T) {
	h, _, dc, chains := newDeploymentAdminServer(t)
	_ = dc.Put(context.Background(), deploymentconfigstore.Config{CycleDetectionMode: "enforce", LastRevision: 1})

	var req admin.DeploymentConfigChangeRequest
	req.ReleaseRevision = 2
	req.CycleDetection.Mode = "permissive"
	if rr := postConfigChange(t, h, req, withAdminPrincipal); rr.Code != http.StatusBadRequest {
		t.Fatalf("permissive without justification: status %d, want 400", rr.Code)
	}
	req.CycleDetection.Justification = "scoped diagnostic rollout"
	rr := postConfigChange(t, h, req, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("permissive with justification: status %d, body %s", rr.Code, rr.Body.String())
	}
	r, ok := findRow(rowsFor(t, chains, "platform"), "gateway.cycle_detection_mode_changed")
	if !ok || r.detail["justification"] != "scoped diagnostic rollout" || r.detail["new_mode"] != "permissive" {
		t.Errorf("cycle permissive event = %+v ok=%v", r.detail, ok)
	}
}

// spec: §16.7 line 672 — a no→yes self-recursion raise requires justification.
func TestDeploymentConfigSelfRecursionRaiseJustificationRequired_spec_16_7(t *testing.T) {
	h, _, dc, _ := newDeploymentAdminServer(t)
	_ = dc.Put(context.Background(), deploymentconfigstore.Config{AllowSelfRecursion: "no", LastRevision: 1})
	var req admin.DeploymentConfigChangeRequest
	req.ReleaseRevision = 2
	req.AllowSelfRecursion.Value = "yes"
	if rr := postConfigChange(t, h, req, withAdminPrincipal); rr.Code != http.StatusBadRequest {
		t.Fatalf("no→yes without justification: status %d, want 400", rr.Code)
	}
}

// spec: §16.7 line 682 — one acknowledged-downgrade event per (flag,
// webhook), with a required justification.
func TestDeploymentConfigDowngradeAck_spec_16_7(t *testing.T) {
	h, _, _, chains := newDeploymentAdminServer(t)
	var req admin.DeploymentConfigChangeRequest
	req.ReleaseRevision = 1
	req.AcknowledgedDowngrades = []admin.DowngradeAck{
		{Flag: "features.compliance", ExpectedWebhook: "lenny-data-residency-validator", Justification: "regulated cohort wind-down"},
		{Flag: "features.compliance", ExpectedWebhook: "lenny-t4-node-isolation", Justification: "regulated cohort wind-down"},
	}
	rr := postConfigChange(t, h, req, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("downgrade ack: status %d, body %s", rr.Code, rr.Body.String())
	}
	var count int
	for _, r := range rowsFor(t, chains, "platform") {
		if r.eventType == "deployment.feature_flag_downgrade_acknowledged" {
			count++
			if r.detail["acknowledged_by_sub"] != "admin@acme.com" || r.detail["acknowledged_by_tenant_id"] != "platform" {
				t.Errorf("ack event detail = %v", r.detail)
			}
		}
	}
	if count != 2 {
		t.Errorf("emitted %d ack events, want 2", count)
	}

	// Missing justification is rejected.
	var bad admin.DeploymentConfigChangeRequest
	bad.ReleaseRevision = 2
	bad.AcknowledgedDowngrades = []admin.DowngradeAck{{Flag: "features.llmProxy", ExpectedWebhook: "lenny-direct-mode-isolation"}}
	if rr := postConfigChange(t, h, bad, withAdminPrincipal); rr.Code != http.StatusBadRequest {
		t.Errorf("ack without justification: status %d, want 400", rr.Code)
	}
}

// spec: §16.7 — input validation: bad revision, mode, and floor are 400.
func TestDeploymentConfigValidation_spec_16_7(t *testing.T) {
	h, _, _, _ := newDeploymentAdminServer(t)
	cases := []admin.DeploymentConfigChangeRequest{
		{ReleaseRevision: 0},
	}
	if rr := postConfigChange(t, h, cases[0], withAdminPrincipal); rr.Code != http.StatusBadRequest {
		t.Errorf("revision 0: status %d, want 400", rr.Code)
	}
	badMode := admin.DeploymentConfigChangeRequest{ReleaseRevision: 1}
	badMode.CycleDetection.Mode = "bogus"
	if rr := postConfigChange(t, h, badMode, withAdminPrincipal); rr.Code != http.StatusBadRequest {
		t.Errorf("bad cycle mode: status %d, want 400", rr.Code)
	}
	badFloor := admin.DeploymentConfigChangeRequest{ReleaseRevision: 1, ElicitationFloor: "loud"}
	if rr := postConfigChange(t, h, badFloor, withAdminPrincipal); rr.Code != http.StatusBadRequest {
		t.Errorf("bad floor: status %d, want 400", rr.Code)
	}
}

// spec: §10.2 — the endpoint requires the platform-admin role.
func TestDeploymentConfigRequiresPlatformAdmin_spec_10_2(t *testing.T) {
	h, _, _, _ := newDeploymentAdminServer(t)
	if rr := postConfigChange(t, h, firstInstallRequest(), withTenantAdminPrincipal); rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin: status %d, want 403", rr.Code)
	}
}
