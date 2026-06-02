// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/auditretention"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// fakeDropper records the §16.4 force-drop call so the handler's
// argument plumbing (partition id, requester subject) is verifiable
// without a Postgres audit store.
type fakeDropper struct {
	gotTenant string
	gotSub    string
	res       auditretention.ForceDropResult
	err       error
}

func (f *fakeDropper) ForceDrop(_ context.Context, tenantID, sub string, _ time.Time) (auditretention.ForceDropResult, error) {
	f.gotTenant = tenantID
	f.gotSub = sub
	return f.res, f.err
}

func newForceDropRouter(d admin.AuditPartitionDropper) *admin.Router {
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithAuditLog(&fakeTranslationLog{}).WithAuditPruner(d)
}

// spec: §16.4 line 378 / §16.7 line 687 — a force-drop with the
// data-loss acknowledgement deletes the partition and returns the
// §16.7 payload, passing the requester subject through to the pruner.
func TestForceDropAuditPartition_acknowledged(t *testing.T) {
	d := &fakeDropper{res: auditretention.ForceDropResult{
		Partition:            "acme",
		EventsLost:           5,
		RequesterSub:         "admin@acme.com",
		AcknowledgedDataLoss: true,
	}}
	router := newForceDropRouter(d)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-partitions/acme/drop?force=true",
			strings.NewReader(`{"acknowledgeDataLoss":true,"partition":"acme"}`))))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if d.gotTenant != "acme" {
		t.Errorf("ForceDrop tenant = %q, want acme", d.gotTenant)
	}
	if d.gotSub != "admin@acme.com" {
		t.Errorf("ForceDrop subject = %q, want admin@acme.com", d.gotSub)
	}
	var res auditretention.ForceDropResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.EventsLost != 5 || !res.AcknowledgedDataLoss {
		t.Errorf("result = %+v", res)
	}
}

// spec: §16.4 line 378 — the force-drop is rejected without an explicit
// data-loss acknowledgement, so a SIEM-undelivered row is never dropped
// by accident.
func TestForceDropAuditPartition_requiresAcknowledgement(t *testing.T) {
	d := &fakeDropper{}
	router := newForceDropRouter(d)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-partitions/acme/drop?force=true",
			strings.NewReader(`{"acknowledgeDataLoss":false,"partition":"acme"}`))))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d, want 400, body=%s", rr.Code, rr.Body.String())
	}
	if d.gotTenant != "" {
		t.Errorf("ForceDrop must not run without acknowledgement (got tenant %q)", d.gotTenant)
	}
}

// spec: §25.9 line 3664 — the destructive drop requires the ?force=true
// query parameter; without it the request is rejected before the pruner
// is touched.
func TestForceDropAuditPartition_requiresForceQueryParam(t *testing.T) {
	d := &fakeDropper{}
	router := newForceDropRouter(d)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-partitions/acme/drop",
			strings.NewReader(`{"acknowledgeDataLoss":true,"partition":"acme"}`))))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d, want 400, body=%s", rr.Code, rr.Body.String())
	}
	if d.gotTenant != "" {
		t.Errorf("ForceDrop must not run without ?force=true (got tenant %q)", d.gotTenant)
	}
}

// spec: §25.9 line 3664 — the body partition is an anti-footgun
// cross-check; a body partition that does not match the path is rejected
// so a copy-paste error cannot drop the wrong partition.
func TestForceDropAuditPartition_partitionMismatch(t *testing.T) {
	d := &fakeDropper{}
	router := newForceDropRouter(d)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-partitions/acme/drop?force=true",
			strings.NewReader(`{"acknowledgeDataLoss":true,"partition":"globex"}`))))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d, want 400, body=%s", rr.Code, rr.Body.String())
	}
	if d.gotTenant != "" {
		t.Errorf("ForceDrop must not run on a partition mismatch (got tenant %q)", d.gotTenant)
	}
}

// spec: §25.9 line 3664 — a token carrying a scope claim that lacks
// audit:partition:drop is rejected with 403 before the pruner runs.
func TestForceDropAuditPartition_forbiddenWithoutScope(t *testing.T) {
	d := &fakeDropper{}
	router := newForceDropRouter(d)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-partitions/acme/drop?force=true",
			strings.NewReader(`{"acknowledgeDataLoss":true,"partition":"acme"}`)),
		"tools:audit:read"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: %d, want 403, body=%s", rr.Code, rr.Body.String())
	}
	if d.gotTenant != "" {
		t.Errorf("ForceDrop must not run without the audit:partition:drop scope (got tenant %q)", d.gotTenant)
	}
}

// The route is absent when no durable pruner is wired (the in-memory
// gateway has nothing to drop), so a force-drop returns 404.
func TestForceDropAuditPartition_absentWithoutPruner(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithAuditLog(&fakeTranslationLog{})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-partitions/acme/drop",
			strings.NewReader(`{"acknowledgeDataLoss":true}`))))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: %d, want 404 (route unregistered), body=%s", rr.Code, rr.Body.String())
	}
}
