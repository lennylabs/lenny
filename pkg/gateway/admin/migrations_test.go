// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/schemamigrate"
)

// fakeMigrationManager is a test double for the §15.1 schema-migration
// seam.
type fakeMigrationManager struct {
	status     schemamigrate.StatusReport
	statusErr  error
	down       schemamigrate.DownResult
	downErr    error
	downCalled uint // records the version passed to Down
}

func (f *fakeMigrationManager) Status(context.Context) (schemamigrate.StatusReport, error) {
	return f.status, f.statusErr
}

func (f *fakeMigrationManager) Down(_ context.Context, version uint) (schemamigrate.DownResult, error) {
	f.downCalled = version
	return f.down, f.downErr
}

func newMigrationAdmin(t *testing.T, mgr admin.MigrationManager) (*admin.Router, *recordingAudit) {
	t.Helper()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithMigrationManager(mgr)
	return router, audit
}

func migrateReq(t *testing.T, h http.Handler, method, path string, body any, auth func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	return doAdminReq(t, h, method, path, body, auth)
}

// spec: §15.1 line 891 — GET …/status returns the per-migration phase
// projection.
func TestMigrationStatus_spec_15_1_891(t *testing.T) {
	mgr := &fakeMigrationManager{status: schemamigrate.StatusReport{
		CurrentVersion: 3,
		Dirty:          true,
		Migrations: []schemamigrate.MigrationStatus{
			{Version: 1, Phase: "complete", GateCheckResult: "not_run"},
			{Version: 3, Phase: "complete", GateCheckResult: "not_run", Dirty: true},
		},
	}}
	router, _ := newMigrationAdmin(t, mgr)
	rr := migrateReq(t, router.Handler(), http.MethodGet, "/v1/admin/schema/migrations/status", nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var out schemamigrate.StatusReport
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.CurrentVersion != 3 || !out.Dirty || len(out.Migrations) != 2 {
		t.Fatalf("unexpected report: %+v", out)
	}
}

// spec: §10.2 — both endpoints require platform-admin.
func TestMigrationStatusRejectsNonAdmin_spec_10_2(t *testing.T) {
	router, _ := newMigrationAdmin(t, &fakeMigrationManager{})
	rr := migrateReq(t, router.Handler(), http.MethodGet, "/v1/admin/schema/migrations/status", nil, withTenantAdminPrincipal)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin status: got %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §15.1 line 892 — a down call without confirm is rejected with
// 422 CONFIRMATION_REQUIRED and Down is never invoked.
func TestMigrationDownRequiresConfirm_spec_15_1_892(t *testing.T) {
	mgr := &fakeMigrationManager{}
	router, audit := newMigrationAdmin(t, mgr)
	for _, body := range []any{nil, map[string]any{"confirm": false}} {
		rr := migrateReq(t, router.Handler(), http.MethodPost, "/v1/admin/schema/migrations/3/down", body, withAdminPrincipal)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("confirm guard: got %d, want 422; body=%s", rr.Code, rr.Body.String())
		}
		if code := errCode(rr.Body.Bytes()); code != "CONFIRMATION_REQUIRED" {
			t.Fatalf("error code: %q", code)
		}
	}
	if mgr.downCalled != 0 {
		t.Fatalf("Down invoked despite missing confirm: version=%d", mgr.downCalled)
	}
	if len(audit.snapshot()) != 0 {
		t.Fatalf("audit emitted on rejected rollback: %+v", audit.snapshot())
	}
}

// spec: §15.1 line 892 / §16.6 — a confirmed down rolls the version back
// and writes the platform.schema_migration_rolled_back audit row.
func TestMigrationDownHappyPathAudits_spec_15_1_892(t *testing.T) {
	mgr := &fakeMigrationManager{down: schemamigrate.DownResult{
		Version: 3, DirtyFlagCleared: true, AdvisoryLocksReleased: true,
	}}
	router, audit := newMigrationAdmin(t, mgr)
	rr := migrateReq(t, router.Handler(), http.MethodPost, "/v1/admin/schema/migrations/3/down",
		map[string]any{"confirm": true, "reason": "view dependency blocked DROP"}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("down: got %d body=%s", rr.Code, rr.Body.String())
	}
	if mgr.downCalled != 3 {
		t.Fatalf("Down version: got %d, want 3", mgr.downCalled)
	}
	ev := findAudit(t, audit, "platform.schema_migration_rolled_back")
	if ev.Detail["version"].(uint) != 3 {
		t.Errorf("audit version: %v", ev.Detail["version"])
	}
	if ev.Detail["rollback_reason"] != "view dependency blocked DROP" {
		t.Errorf("audit reason: %v", ev.Detail["rollback_reason"])
	}
	if ev.Detail["dirty_flag_cleared"] != true || ev.Detail["advisory_locks_released"] != true {
		t.Errorf("audit flags: %+v", ev.Detail)
	}
}

// spec: §24.13 line 151 — a version that is not the current migration
// returns 409 MIGRATION_VERSION_MISMATCH.
func TestMigrationDownVersionMismatch_spec_24_13_151(t *testing.T) {
	mgr := &fakeMigrationManager{downErr: schemamigrate.ErrVersionMismatch}
	router, audit := newMigrationAdmin(t, mgr)
	rr := migrateReq(t, router.Handler(), http.MethodPost, "/v1/admin/schema/migrations/2/down",
		map[string]any{"confirm": true}, withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Fatalf("mismatch: got %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if code := errCode(rr.Body.Bytes()); code != "MIGRATION_VERSION_MISMATCH" {
		t.Fatalf("error code: %q", code)
	}
	if len(audit.snapshot()) != 0 {
		t.Fatalf("audit emitted on failed rollback: %+v", audit.snapshot())
	}
}

// A non-numeric version path segment is a client error, not a 500.
func TestMigrationDownInvalidVersion(t *testing.T) {
	router, _ := newMigrationAdmin(t, &fakeMigrationManager{})
	rr := migrateReq(t, router.Handler(), http.MethodPost, "/v1/admin/schema/migrations/latest/down",
		map[string]any{"confirm": true}, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid version: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// errCode pulls the §15.1 error envelope code from a response body.
func errCode(raw []byte) string {
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	return env.Error.Code
}
