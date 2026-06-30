// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §10.7 admission-time tenant-floor advisory check.

// newExperimentTenantFloorAdmin builds an experiment admin router with
// tenant `acme` carrying the given minIsolationProfile floor, a
// `sandboxed` base runtime, and pools at `sandboxed` and `microvm`.
func newExperimentTenantFloorAdmin(t *testing.T, tenantFloor string) (*admin.Router, *recordingAudit, *eventbuffer.Emitter) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{
		ID: "acme", MinIsolationProfile: tenantFloor,
	}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "claude-worker", IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name: "cw-sandboxed", RuntimeRef: "claude-worker", IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("seed sandboxed pool: %v", err)
	}
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name: "cw-microvm", RuntimeRef: "claude-worker", IsolationProfile: isolation.ProfileMicrovm,
	}); err != nil {
		t.Fatalf("seed microvm pool: %v", err)
	}
	audit := &recordingAudit{}
	emitter := eventbuffer.NewEmitter(eventbuffer.NewEventBuffer(0), "replica-test")
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithRuntimes(runtimes).WithPools(pools).
		WithExperiments(experimentstore.NewMemory()).WithEventEmitter(emitter)
	return router, audit, emitter
}

func emittedTenantFloorAdvisory(audit *recordingAudit) bool {
	for _, ev := range audit.snapshot() {
		if ev.Type == "experiment.variant_weaker_than_tenant_floor" {
			return true
		}
	}
	return false
}

func TestCreateExperimentEmitsTenantFloorAdvisory(t *testing.T) {
	router, audit, _ := newExperimentTenantFloorAdmin(t, "microvm")
	body := validExperimentPayload("exp_tf")
	// cw-sandboxed meets the sandboxed base runtime (the hard check
	// passes) but is below the microvm tenant floor.
	body.Variants[0].Pool = "cw-sandboxed"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, want 201 (the tenant-floor check is advisory); body %s",
			rr.Code, rr.Body.String())
	}
	if !emittedTenantFloorAdvisory(audit) {
		t.Error("a variant pool below the tenant floor must emit experiment.variant_weaker_than_tenant_floor")
	}
}

func TestCreateExperimentNoAdvisoryWhenAtTenantFloor(t *testing.T) {
	router, audit, _ := newExperimentTenantFloorAdmin(t, "microvm")
	body := validExperimentPayload("exp_ok")
	body.Variants[0].Pool = "cw-microvm" // meets the microvm tenant floor
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d", rr.Code)
	}
	if emittedTenantFloorAdvisory(audit) {
		t.Error("a variant pool meeting the tenant floor must not emit the advisory event")
	}
}

func TestCreateExperimentNoAdvisoryWithoutTenantFloor(t *testing.T) {
	// A tenant with no minIsolationProfile floor configured.
	router, audit, _ := newExperimentTenantFloorAdmin(t, "")
	body := validExperimentPayload("exp_nofloor")
	body.Variants[0].Pool = "cw-sandboxed"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d", rr.Code)
	}
	if emittedTenantFloorAdvisory(audit) {
		t.Error("no tenant floor configured — the advisory event must not fire")
	}
}

func TestCreateExperimentTenantFloorAdvisoryReachesEventBuffer(t *testing.T) {
	// §16.6: the tenant-floor advisory is also an operational event —
	// it must reach the §25.3 event buffer, not only the audit log.
	router, _, emitter := newExperimentTenantFloorAdmin(t, "microvm")
	body := validExperimentPayload("exp_tf_ops")
	body.Variants[0].Pool = "cw-sandboxed" // below the microvm tenant floor
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/experiments", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}

	page := emitter.Buffer().Query(0, events.EventFilter{
		EventType: string(events.EventExperimentVariantWeakerThanFloor),
	}, 0)
	if len(page.Events) != 1 {
		t.Fatalf("event buffer holds %d variant_weaker_than_tenant_floor events, want 1", len(page.Events))
	}
	ev := page.Events[0].Event
	if ev.Type != "dev.lenny.experiment.variant_weaker_than_tenant_floor" {
		t.Errorf("event type = %q", ev.Type)
	}
	if ev.Severity != "warning" {
		t.Errorf("severity = %q, want warning", ev.Severity)
	}
	var data struct {
		TenantID       string `json:"tenant_id"`
		ExperimentID   string `json:"experiment_id"`
		VariantID      string `json:"variant_id"`
		VariantPoolIso string `json:"variant_pool_isolation"`
		TenantFloor    string `json:"tenant_floor"`
		ActorSub       string `json:"actor_sub"`
		EmittedAt      string `json:"emitted_at"`
	}
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatalf("event data: %v", err)
	}
	if data.TenantID != "acme" || data.ExperimentID != "exp_tf_ops" {
		t.Errorf("tenant/experiment = %q/%q, want acme/exp_tf_ops", data.TenantID, data.ExperimentID)
	}
	if data.TenantFloor != string(isolation.ProfileMicrovm) ||
		data.VariantPoolIso != string(isolation.ProfileSandboxed) {
		t.Errorf("floor/variant isolation = %q/%q, want %s/%s",
			data.TenantFloor, data.VariantPoolIso,
			isolation.ProfileMicrovm, isolation.ProfileSandboxed)
	}
	if data.ActorSub == "" || data.EmittedAt == "" {
		t.Errorf("actor_sub and emitted_at must be populated: %q / %q", data.ActorSub, data.EmittedAt)
	}
}
