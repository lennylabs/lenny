// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// fakeSource is the in-test §25.4 Source the inventory uses.
type fakeOpsSource struct {
	kinds []operations.Kind
	ops   []operations.Operation
	err   error
}

func (s *fakeOpsSource) Kinds() []operations.Kind { return s.kinds }
func (s *fakeOpsSource) List(context.Context, operations.Filter) ([]operations.Operation, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.ops, nil
}

func newOperationsAdmin(t *testing.T, sources ...operations.Source) *admin.Router {
	t.Helper()
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithOperationsInventory(operations.New(sources...))
}

// spec §4.0, §25.4: GET /v1/admin/operations returns the unified
// inventory filtered by status/kind, with the canonical pagination
// envelope.
func TestOperationsListEndpoint(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	src := &fakeOpsSource{
		kinds: []operations.Kind{operations.KindRemediationLock},
		ops: []operations.Operation{
			{
				OperationID: "lock-a", Kind: operations.KindRemediationLock,
				Status: operations.StatusHeld, StartedAt: now, Resources: map[string]string{},
			},
		},
	}
	router := newOperationsAdmin(t, src)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/operations", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var page operations.Page
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Operations) != 1 || page.Operations[0].OperationID != "lock-a" {
		t.Errorf("got %+v, want one lock-a", page.Operations)
	}
	if page.Pagination.Limit == 0 {
		t.Error("response did not include the canonical pagination envelope")
	}
}

// spec §25.4: GET /v1/admin/operations/{id} returns the single
// operation when it exists, and OPERATION_NOT_FOUND when it does not.
func TestOperationsGetEndpoint(t *testing.T) {
	now := time.Now()
	src := &fakeOpsSource{
		kinds: []operations.Kind{operations.KindPlatformUpgrade},
		ops: []operations.Operation{
			{
				OperationID: "upgrade-abc", Kind: operations.KindPlatformUpgrade,
				Status: operations.StatusInProgress, StartedAt: now, Resources: map[string]string{},
			},
		},
	}
	router := newOperationsAdmin(t, src)

	t.Run("found", func(t *testing.T) {
		req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/operations/upgrade-abc", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
		}
		var resp struct {
			Operation operations.Operation `json:"operation"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Operation.OperationID != "upgrade-abc" {
			t.Errorf("got %s, want upgrade-abc", resp.Operation.OperationID)
		}
	})

	t.Run("not found", func(t *testing.T) {
		req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/operations/upgrade-missing", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})
}

// spec §25.4: a source error surfaces as an OPERATIONS_INVENTORY_PARTIAL
// degradation envelope in the response.
func TestOperationsListPartialOnSourceError(t *testing.T) {
	broken := &fakeOpsSource{
		kinds: []operations.Kind{operations.KindPlatformUpgrade},
		err:   errors.New("Postgres unreachable"),
	}
	router := newOperationsAdmin(t, broken)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/operations", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with degraded envelope", rr.Code)
	}
	var page operations.Page
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if page.Degradation == nil {
		t.Fatal("expected degradation envelope when a source errored")
	}
	if len(page.Degradation.Warnings) != 1 {
		t.Fatalf("got %d warnings, want 1", len(page.Degradation.Warnings))
	}
}

// spec §25.4: the ?kind= and ?status= filters narrow the result set.
func TestOperationsListFilters(t *testing.T) {
	now := time.Now()
	src := &fakeOpsSource{
		kinds: []operations.Kind{operations.KindRemediationLock, operations.KindBackup},
		ops: []operations.Operation{
			{
				OperationID: "lock-a", Kind: operations.KindRemediationLock,
				Status: operations.StatusHeld, StartedAt: now, Resources: map[string]string{},
			},
			{
				OperationID: "backup-1", Kind: operations.KindBackup,
				Status: operations.StatusInProgress, StartedAt: now, Resources: map[string]string{},
			},
		},
	}
	router := newOperationsAdmin(t, src)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/operations?kind=backup&status=in_progress", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	var page operations.Page
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Operations) != 1 || page.Operations[0].OperationID != "backup-1" {
		t.Errorf("got %+v, want one backup-1", page.Operations)
	}
}

// spec §25.4: an unauthenticated caller is rejected with 403 before
// any inventory read.
func TestOperationsRequiresAdmin(t *testing.T) {
	router := newOperationsAdmin(t)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/admin/operations", nil))
	if rr.Code != http.StatusForbidden {
		t.Errorf("unauthenticated caller got status %d, want 403", rr.Code)
	}
}
