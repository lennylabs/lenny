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

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/eventbus"
)

// spec: §25.9 line 3663 — POST /v1/admin/audit-events/{seq}/republish.

// fakePublishLog is an admin.AuditLog that also tracks the §12.3.7
// eventbus_publish_state per row, so the republish endpoint's eligibility
// branches are exercisable without Postgres.
type fakePublishLog struct {
	rows  []audit.Row
	state map[uint64]eventbus.PublishState
	retry map[uint64]int
	set   map[uint64]eventbus.PublishState // captures SetPublishState calls
	setRC map[uint64]int
}

func (f *fakePublishLog) Append(context.Context, string, string, json.RawMessage, time.Time) (audit.Row, error) {
	return audit.Row{}, nil
}

func (f *fakePublishLog) Rows(_ context.Context, _ string) ([]audit.Row, error) {
	return f.rows, nil
}

func (f *fakePublishLog) Verify(context.Context, string) (audit.VerifyResult, error) {
	return audit.VerifyResult{Integrity: audit.ChainVerified}, nil
}

func (f *fakePublishLog) PublishState(_ context.Context, _ string, seq uint64) (eventbus.PublishState, int, error) {
	st, ok := f.state[seq]
	if !ok {
		st = eventbus.PublishPublished
	}
	return st, f.retry[seq], nil
}

func (f *fakePublishLog) SetPublishState(_ context.Context, _ string, seq uint64, state eventbus.PublishState, retryCount int) error {
	if f.set == nil {
		f.set = map[uint64]eventbus.PublishState{}
		f.setRC = map[uint64]int{}
	}
	f.set[seq] = state
	f.setRC[seq] = retryCount
	return nil
}

func newFakePublishRouter(fake *fakePublishLog) *admin.Router {
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithAuditLog(fake)
}

// TestRepublishFailedRow confirms a terminally-failed row is reset to
// pending with retry_count cleared.
func TestRepublishFailedRow_spec_25_9_3663(t *testing.T) {
	fake := &fakePublishLog{
		rows:  []audit.Row{{Seq: 1, TenantID: "platform", EventType: "admin.tenant.created"}},
		state: map[uint64]eventbus.PublishState{1: eventbus.PublishFailed},
		retry: map[uint64]int{1: 5},
	}
	router := newFakePublishRouter(fake)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-events/1/republish?tenantId=platform", nil),
		"tools:audit:republish"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Seq                  uint64 `json:"seq"`
		EventbusPublishState string `json:"eventbusPublishState"`
		PriorState           string `json:"priorState"`
		PriorRetryCount      int    `json:"priorRetryCount"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.EventbusPublishState != "pending" {
		t.Errorf("state = %q, want pending", resp.EventbusPublishState)
	}
	if resp.PriorState != "failed" || resp.PriorRetryCount != 5 {
		t.Errorf("prior = %q/%d, want failed/5", resp.PriorState, resp.PriorRetryCount)
	}
	if fake.set[1] != eventbus.PublishPending || fake.setRC[1] != 0 {
		t.Errorf("SetPublishState = %v/%v, want pending/0", fake.set[1], fake.setRC[1])
	}
}

// TestRepublishAlreadyPublished confirms a published row returns 409
// ALREADY_PUBLISHED with details.currentState and never transitions.
func TestRepublishAlreadyPublished_spec_25_9_3663(t *testing.T) {
	fake := &fakePublishLog{
		rows:  []audit.Row{{Seq: 1, TenantID: "platform", EventType: "admin.tenant.created"}},
		state: map[uint64]eventbus.PublishState{1: eventbus.PublishPublished},
	}
	router := newFakePublishRouter(fake)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-events/1/republish?tenantId=platform", nil),
		"tools:audit:republish"))
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: %d, want 409, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "ALREADY_PUBLISHED") || !strings.Contains(rr.Body.String(), "published") {
		t.Errorf("missing ALREADY_PUBLISHED / currentState: %s", rr.Body.String())
	}
	if len(fake.set) != 0 {
		t.Errorf("a published row must not transition: %v", fake.set)
	}
}

// TestRepublishInFlight confirms a row still in flight (pending or
// retry_pending) returns 409 with the in-flight currentState so an
// operator can distinguish it from completed.
func TestRepublishInFlight_spec_25_9_3663(t *testing.T) {
	for _, st := range []eventbus.PublishState{eventbus.PublishPending, eventbus.PublishRetryPending} {
		fake := &fakePublishLog{
			rows:  []audit.Row{{Seq: 1, TenantID: "platform", EventType: "admin.tenant.created"}},
			state: map[uint64]eventbus.PublishState{1: st},
		}
		router := newFakePublishRouter(fake)
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
			httptest.NewRequest(http.MethodPost, "/v1/admin/audit-events/1/republish?tenantId=platform", nil),
			"tools:audit:republish"))
		if rr.Code != http.StatusConflict {
			t.Fatalf("state %s: status %d, want 409, body=%s", st, rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), string(st)) {
			t.Errorf("state %s: response missing currentState: %s", st, rr.Body.String())
		}
	}
}

// TestRepublishNotFound confirms a missing seq returns 404 NOT_FOUND.
func TestRepublishNotFound_spec_25_9_3663(t *testing.T) {
	fake := &fakePublishLog{state: map[uint64]eventbus.PublishState{}}
	router := newFakePublishRouter(fake)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-events/99/republish?tenantId=platform", nil),
		"tools:audit:republish"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: %d, want 404, body=%s", rr.Code, rr.Body.String())
	}
}

// TestRepublishForbiddenWithoutScope confirms a token carrying a scope
// claim lacking audit:republish is rejected with 403.
func TestRepublishForbiddenWithoutScope_spec_25_9_3663(t *testing.T) {
	fake := &fakePublishLog{
		rows:  []audit.Row{{Seq: 1, TenantID: "platform", EventType: "admin.tenant.created"}},
		state: map[uint64]eventbus.PublishState{1: eventbus.PublishFailed},
	}
	router := newFakePublishRouter(fake)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-events/1/republish?tenantId=platform", nil),
		"tools:audit:read"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: %d, want 403, body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.set) != 0 {
		t.Errorf("a row must not transition without the scope: %v", fake.set)
	}
}

// TestRepublishInMemoryNotEligible confirms a backend without
// publish-state tracking reports every row published (409); its rows
// never enter the failed state.
func TestRepublishInMemoryNotEligible_spec_25_9_3663(t *testing.T) {
	router, _ := newAuditQueryRouter(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", strings.NewReader(string(body))),
	))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-events/1/republish?tenantId=platform", nil),
		"tools:audit:republish"))
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: %d, want 409, body=%s", rr.Code, rr.Body.String())
	}
}
