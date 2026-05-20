// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

func newServerWithSubs(t *testing.T) (*opsserver.Server, *eventsubscription.Service) {
	t.Helper()
	store := eventsubscription.NewMemoryStore()
	svc := eventsubscription.NewService(store)
	return opsserver.New(opsserver.Options{EventSubscriptions: svc}), svc
}

func doJSONReq(t *testing.T, s *opsserver.Server, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	var out map[string]any
	if rr.Body.Len() > 0 {
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
	}
	return rr, out
}

// spec: §25.5 (subscription create + read)
// diagnosis: POST /v1/admin/event-subscriptions creates a row and
// returns 201 with the allocated id; the same row is reachable via
// GET /v1/admin/event-subscriptions/{id}.
func TestEventSubscriptionCreateAndGet(t *testing.T) {
	srv, _ := newServerWithSubs(t)
	rr, body := doJSONReq(t, srv, http.MethodPost, "/v1/admin/event-subscriptions", map[string]any{
		"callbackUrl": "https://acme.example/webhook",
		"types":       []string{"dev.lenny.alert_fired"},
		"secret":      "secret-bytes",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body = %v", rr.Code, body)
	}
	id, _ := body["id"].(string)
	if !strings.HasPrefix(id, "sub_") {
		t.Errorf("id = %q, want sub_ prefix", id)
	}

	rr, body = doJSONReq(t, srv, http.MethodGet, "/v1/admin/event-subscriptions/"+id, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body = %v", rr.Code, body)
	}
	if body["callbackUrl"] != "https://acme.example/webhook" {
		t.Errorf("callbackUrl = %v, want https://acme.example/webhook", body["callbackUrl"])
	}
}

// spec: §25.5 (validation)
// diagnosis: a missing callbackUrl is rejected with 400
// VALIDATION_ERROR; a non-http scheme is rejected likewise.
func TestEventSubscriptionRejectsInvalidPayloads(t *testing.T) {
	srv, _ := newServerWithSubs(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing_callback", map[string]any{}},
		{"bad_scheme", map[string]any{"callbackUrl": "ftp://acme.example/webhook"}},
		{"empty_type", map[string]any{"callbackUrl": "https://acme.example/webhook", "types": []string{""}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr, body := doJSONReq(t, srv, http.MethodPost, "/v1/admin/event-subscriptions", c.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %v", rr.Code, body)
			}
			errObj, _ := body["error"].(map[string]any)
			if errObj == nil {
				t.Fatalf("body has no error envelope: %v", body)
			}
			if errObj["code"] != "VALIDATION_ERROR" {
				t.Errorf("error.code = %v, want VALIDATION_ERROR", errObj["code"])
			}
		})
	}
}

// spec: §25.5 (list + delete)
// diagnosis: GET /v1/admin/event-subscriptions returns every
// subscription; DELETE /v1/admin/event-subscriptions/{id} removes it
// and a subsequent GET returns 404 RESOURCE_NOT_FOUND.
func TestEventSubscriptionListAndDelete(t *testing.T) {
	srv, _ := newServerWithSubs(t)
	for i := 0; i < 3; i++ {
		rr, _ := doJSONReq(t, srv, http.MethodPost, "/v1/admin/event-subscriptions", map[string]any{
			"callbackUrl": "https://acme.example/webhook",
		})
		if rr.Code != http.StatusCreated {
			t.Fatalf("create #%d status = %d", i, rr.Code)
		}
	}
	rr, body := doJSONReq(t, srv, http.MethodGet, "/v1/admin/event-subscriptions", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d", rr.Code)
	}
	subs, _ := body["subscriptions"].([]any)
	if len(subs) != 3 {
		t.Fatalf("list returned %d subscriptions, want 3", len(subs))
	}

	first, _ := subs[0].(map[string]any)
	id, _ := first["id"].(string)
	rr, _ = doJSONReq(t, srv, http.MethodDelete, "/v1/admin/event-subscriptions/"+id, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", rr.Code)
	}
	rr, body = doJSONReq(t, srv, http.MethodGet, "/v1/admin/event-subscriptions/"+id, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("post-delete get status = %d, want 404; body = %v", rr.Code, body)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj == nil || errObj["code"] != "RESOURCE_NOT_FOUND" {
		t.Errorf("error envelope = %v, want code=RESOURCE_NOT_FOUND", errObj)
	}
}

// spec: §25.5 (routes are gated on the service being wired)
// diagnosis: when the Server is constructed without an
// EventSubscriptions service, the CRUD routes are unmapped (404),
// matching the documented degradation pattern for the optional
// surfaces.
func TestEventSubscriptionRoutesAbsentWithoutService(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/event-subscriptions", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no service is wired", rr.Code)
	}
}
