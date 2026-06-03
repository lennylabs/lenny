// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// fakeSource is an operations.Source stub returning a fixed slice or error.
type fakeSource struct {
	kinds []operations.Kind
	ops   []operations.Operation
	err   error
}

func (f fakeSource) Kinds() []operations.Kind { return f.kinds }
func (f fakeSource) List(context.Context, operations.Filter) ([]operations.Operation, error) {
	return f.ops, f.err
}

// captureAudit records the §25.4 audit events the handlers emit.
type captureAudit struct {
	events []string
	fields []map[string]any
}

func (c *captureAudit) RecordOpsAudit(_ context.Context, event string, fields map[string]any) {
	c.events = append(c.events, event)
	c.fields = append(c.fields, fields)
}

func (c *captureAudit) has(event string) bool {
	for _, e := range c.events {
		if e == event {
			return true
		}
	}
	return false
}

// platformAdmin / tenantAdmin build the test principals.
func platformAdmin(sub string) authmw.Principal {
	return authmw.Principal{Subject: sub, CallerType: "service", Roles: []auth.Role{auth.RolePlatformAdmin}}
}

func tenantAdmin(sub, tenant string) authmw.Principal {
	return authmw.Principal{Subject: sub, TenantID: tenant, CallerType: "agent", Roles: []auth.Role{auth.RoleTenantAdmin}}
}

// getAuthed issues a GET with the principal injected on the request
// context (the test path has no Auth middleware to attach it).
func getAuthed(t *testing.T, srv *opsserver.Server, url string, p *authmw.Principal) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if p != nil {
		req = req.WithContext(authmw.WithPrincipal(req.Context(), *p))
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

// metricFamilyTotal sums every sample of a metric family by name on the
// default gatherer (proving the §25.4 metric is registered and emitted).
func metricFamilyTotal(t *testing.T, name string) float64 {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var total float64
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if c := m.GetCounter(); c != nil {
				total += c.GetValue()
			}
			if h := m.GetHistogram(); h != nil {
				total += float64(h.GetSampleCount())
			}
		}
	}
	return total
}

func opOf(id string, kind operations.Kind, status operations.Status, startedBy, tenant string) operations.Operation {
	return operations.Operation{
		OperationID: id, Kind: kind, Status: status,
		StartedBy: startedBy, TenantID: tenant, Resources: map[string]string{},
	}
}

func operationsServer(src operations.Source) (*opsserver.Server, *captureAudit) {
	audit := &captureAudit{}
	srv := opsserver.New(opsserver.Options{
		Inventory: operations.New(src),
		Audit:     audit,
	})
	return srv, audit
}

// spec §25.4 line 1740: platform-admin with actor=* sees every operation;
// the metric and audit event fire.
func TestListOperationsPlatformAdminActorAll(t *testing.T) {
	src := fakeSource{
		kinds: []operations.Kind{operations.KindRemediationLock},
		ops: []operations.Operation{
			opOf("lock-1", operations.KindRemediationLock, operations.StatusHeld, "sa-a", ""),
			opOf("lock-2", operations.KindRemediationLock, operations.StatusHeld, "sa-b", ""),
		},
	}
	srv, audit := operationsServer(src)
	before := metricFamilyTotal(t, "lenny_ops_operations_inventory_requests_total")
	p := platformAdmin("sa-admin")
	rec, body := getAuthed(t, srv, "/v1/admin/operations?actor=*", &p)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ops, _ := body["operations"].([]any)
	if len(ops) != 2 {
		t.Fatalf("operations = %d, want 2", len(ops))
	}
	if !audit.has("operations.inventory_queried") {
		t.Error("operations.inventory_queried audit event not recorded")
	}
	if after := metricFamilyTotal(t, "lenny_ops_operations_inventory_requests_total"); after <= before {
		t.Errorf("inventory requests metric did not increment: %v -> %v", before, after)
	}
}

// spec §25.4 line 1736: actor=me (the default) restricts to the caller's
// own started operations.
func TestListOperationsActorMeFiltersToCaller(t *testing.T) {
	src := fakeSource{
		kinds: []operations.Kind{operations.KindRemediationLock},
		ops: []operations.Operation{
			opOf("lock-1", operations.KindRemediationLock, operations.StatusHeld, "sa-admin", ""),
			opOf("lock-2", operations.KindRemediationLock, operations.StatusHeld, "sa-other", ""),
		},
	}
	srv, _ := operationsServer(src)
	p := platformAdmin("sa-admin")
	_, body := getAuthed(t, srv, "/v1/admin/operations", &p)
	ops, _ := body["operations"].([]any)
	if len(ops) != 1 {
		t.Fatalf("operations = %d, want 1 (own only)", len(ops))
	}
	first := ops[0].(map[string]any)
	if first["operationId"] != "lock-1" {
		t.Errorf("operationId = %v, want lock-1", first["operationId"])
	}
}

// spec §25.4 line 1736: a tenant-admin sees their own operations and
// their tenant's, never another principal's platform-scoped operation,
// and is auto-restricted away from actor=*.
func TestListOperationsTenantAdminVisibility(t *testing.T) {
	src := fakeSource{
		kinds: []operations.Kind{operations.KindRemediationLock},
		ops: []operations.Operation{
			opOf("lock-own", operations.KindRemediationLock, operations.StatusHeld, "ta-1", ""),
			opOf("lock-tenant", operations.KindRemediationLock, operations.StatusHeld, "someone", "t1"),
			opOf("lock-other-tenant", operations.KindRemediationLock, operations.StatusHeld, "x", "t2"),
			opOf("lock-platform", operations.KindRemediationLock, operations.StatusHeld, "other", ""),
		},
	}
	srv, _ := operationsServer(src)
	p := tenantAdmin("ta-1", "t1")
	// Even requesting actor=* the tenant-admin is auto-restricted.
	_, body := getAuthed(t, srv, "/v1/admin/operations?actor=*", &p)
	ops, _ := body["operations"].([]any)
	got := map[string]bool{}
	for _, o := range ops {
		got[o.(map[string]any)["operationId"].(string)] = true
	}
	if !got["lock-own"] || !got["lock-tenant"] {
		t.Errorf("tenant-admin must see own + own-tenant ops, got %v", got)
	}
	if got["lock-other-tenant"] || got["lock-platform"] {
		t.Errorf("tenant-admin must not see other-tenant or other-principal platform ops, got %v", got)
	}
}

// spec §25.4 line 1769: a source whose store is unreachable yields a 207
// with the degradation envelope.
func TestListOperationsPartialReturns207(t *testing.T) {
	src := fakeSource{
		kinds: []operations.Kind{operations.KindRemediationLock},
		err:   errors.New("postgres unreachable"),
	}
	srv, _ := operationsServer(src)
	p := platformAdmin("sa-admin")
	rec, body := getAuthed(t, srv, "/v1/admin/operations?actor=*", &p)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", rec.Code)
	}
	if _, ok := body["degradation"]; !ok {
		t.Error("207 response must carry a degradation envelope")
	}
}

// spec §25.4: GET /v1/admin/operations/{id} returns the operation, and a
// missing id returns OPERATION_NOT_FOUND.
func TestGetOperation(t *testing.T) {
	src := fakeSource{
		kinds: []operations.Kind{operations.KindRemediationLock},
		ops:   []operations.Operation{opOf("lock-1", operations.KindRemediationLock, operations.StatusHeld, "sa-admin", "")},
	}
	srv, _ := operationsServer(src)
	p := platformAdmin("sa-admin")

	rec, body := getAuthed(t, srv, "/v1/admin/operations/lock-1", &p)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	op, _ := body["operation"].(map[string]any)
	if op["operationId"] != "lock-1" {
		t.Errorf("operationId = %v, want lock-1", op["operationId"])
	}

	rec, body = getAuthed(t, srv, "/v1/admin/operations/lock-missing", &p)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "OPERATION_NOT_FOUND" {
		t.Errorf("error code = %v, want OPERATION_NOT_FOUND", errObj["code"])
	}
}

// spec §25.4 line 1745: a tenant-admin requesting an operation outside its
// visibility gets a not-found, not a forbidden.
func TestGetOperationHiddenIsNotFound(t *testing.T) {
	src := fakeSource{
		kinds: []operations.Kind{operations.KindRemediationLock},
		ops:   []operations.Operation{opOf("lock-1", operations.KindRemediationLock, operations.StatusHeld, "other", "")},
	}
	srv, _ := operationsServer(src)
	p := tenantAdmin("ta-1", "t1")
	rec, _ := getAuthed(t, srv, "/v1/admin/operations/lock-1", &p)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a hidden operation", rec.Code)
	}
}

// spec §25.4: the operations endpoints are unmapped when no inventory is
// wired (the cold-start posture).
func TestOperationsUnmappedWithoutInventory(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	p := platformAdmin("sa-admin")
	rec, _ := getAuthed(t, srv, "/v1/admin/operations", &p)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when inventory unwired", rec.Code)
	}
}
