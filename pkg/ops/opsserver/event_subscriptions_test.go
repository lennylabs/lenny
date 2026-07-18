// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// stubResolver resolves every host to a fixed public address so the
// §25.5 SSRF validator's DNS step runs deterministically in tests
// without real name resolution.
type stubResolver struct{}

func (stubResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

func newServerWithSubs(t *testing.T) (*opsserver.Server, *eventsubscription.Service) {
	t.Helper()
	store := eventsubscription.NewMemoryStore()
	svc := eventsubscription.NewService(store)
	// Pin the SSRF resolver so create/update validate against a public IP
	// rather than performing real DNS for example hostnames.
	svc.SSRF = eventsubscription.NewSSRFValidator(eventsubscription.SSRFConfig{Resolver: stubResolver{}})
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

// spec: §25.5 lines 2702-2713 (create returns the secret once; reads
// redact it)
// diagnosis: POST /v1/admin/event-subscriptions creates a row, generates
// the secret server-side, returns it once with the rotation warning, and
// the same row is reachable via GET with the secret omitted.
func TestEventSubscriptionCreateRevealsSecretOnceAndRedactsOnRead(t *testing.T) {
	srv, _ := newServerWithSubs(t)
	rr, body := doJSONReq(t, srv, http.MethodPost, "/v1/admin/event-subscriptions", map[string]any{
		"callbackUrl": "https://acme.example/webhook",
		"types":       []string{"dev.lenny.alert_fired"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body = %v", rr.Code, body)
	}
	secret, _ := body["secret"].(string)
	if !strings.HasPrefix(secret, "whsec_") {
		t.Errorf("create did not return a whsec_ secret: %q", secret)
	}
	if _, ok := body["secretRotationWarning"].(string); !ok {
		t.Errorf("create response missing secretRotationWarning")
	}
	fp, _ := body["secretFingerprint"].(string)
	if len(fp) != 8 {
		t.Errorf("secretFingerprint = %q, want 8 hex chars", fp)
	}
	id, _ := body["id"].(string)

	rr, body = doJSONReq(t, srv, http.MethodGet, "/v1/admin/event-subscriptions/"+id, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body = %v", rr.Code, body)
	}
	if _, present := body["secret"]; present {
		t.Errorf("GET leaked the secret field: %v", body["secret"])
	}
	if body["secretFingerprint"] != fp {
		t.Errorf("GET fingerprint = %v, want %s", body["secretFingerprint"], fp)
	}
	if body["tenantFilter"] != "*" {
		t.Errorf("platform-admin tenantFilter = %v, want *", body["tenantFilter"])
	}
}

// spec: §25.5 lines 2795-2802 (canonical error codes)
// diagnosis: a non-http scheme and an SSRF-failing callback are rejected
// with the canonical §25.5 codes (INVALID/422-WEBHOOK_VALIDATION_FAILED),
// and an empty type entry is INVALID_EVENT_FILTER (400).
func TestEventSubscriptionCanonicalErrorCodes(t *testing.T) {
	srv, _ := newServerWithSubs(t)
	cases := []struct {
		name   string
		body   map[string]any
		status int
		code   string
	}{
		{"bad_scheme", map[string]any{"callbackUrl": "ftp://acme.example/webhook"}, http.StatusUnprocessableEntity, "WEBHOOK_VALIDATION_FAILED"},
		{"ip_literal", map[string]any{"callbackUrl": "https://127.0.0.1/webhook"}, http.StatusUnprocessableEntity, "WEBHOOK_VALIDATION_FAILED"},
		{"empty_type", map[string]any{"callbackUrl": "https://acme.example/webhook", "types": []string{""}}, http.StatusBadRequest, "INVALID_EVENT_FILTER"},
		{"bad_severity", map[string]any{"callbackUrl": "https://acme.example/webhook", "severity": []string{"nope"}}, http.StatusBadRequest, "INVALID_EVENT_FILTER"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr, body := doJSONReq(t, srv, http.MethodPost, "/v1/admin/event-subscriptions", c.body)
			if rr.Code != c.status {
				t.Fatalf("status = %d, want %d; body = %v", rr.Code, c.status, body)
			}
			errObj, _ := body["error"].(map[string]any)
			if errObj == nil || errObj["code"] != c.code {
				t.Errorf("error.code = %v, want %s", errObj["code"], c.code)
			}
		})
	}
}

// spec: §25.5 lines 2758-2766 (tenant isolation)
// diagnosis: a tenant-admin caller may only register its own tenant; a
// wildcard or a cross-tenant filter returns 403
// SUBSCRIPTION_TENANT_FORBIDDEN. A platform-admin may register the
// wildcard.
func TestEventSubscriptionTenantIsolation(t *testing.T) {
	srv, _ := newServerWithSubs(t)

	post := func(role, tenant string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/event-subscriptions", bytes.NewReader(b))
		req.Header.Set("X-Lenny-Role", role)
		req.Header.Set("X-Lenny-Tenant-ID", tenant)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		var out map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		return rr, out
	}

	// tenant-admin cannot register the wildcard.
	rr, body := post("tenant-admin", "acme", map[string]any{"callbackUrl": "https://acme.example/w", "tenantFilter": "*"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("tenant-admin wildcard status = %d, want 403; body = %v", rr.Code, body)
	}
	if errObj, _ := body["error"].(map[string]any); errObj == nil || errObj["code"] != "SUBSCRIPTION_TENANT_FORBIDDEN" {
		t.Errorf("error.code = %v, want SUBSCRIPTION_TENANT_FORBIDDEN", body["error"])
	}

	// tenant-admin cannot register a different tenant.
	rr, _ = post("tenant-admin", "acme", map[string]any{"callbackUrl": "https://acme.example/w", "tenantFilter": "globex"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("tenant-admin cross-tenant status = %d, want 403", rr.Code)
	}

	// tenant-admin omitting the filter defaults to its own tenant.
	rr, body = post("tenant-admin", "acme", map[string]any{"callbackUrl": "https://acme.example/w"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("tenant-admin own-tenant status = %d, want 201; body = %v", rr.Code, body)
	}
	if body["tenantFilter"] != "acme" || body["createdByTenantId"] != "acme" {
		t.Errorf("tenant-admin row = filter %v tenant %v, want acme/acme", body["tenantFilter"], body["createdByTenantId"])
	}

	// platform-admin may register the wildcard.
	rr, body = post("platform-admin", "", map[string]any{"callbackUrl": "https://acme.example/w"})
	if rr.Code != http.StatusCreated || body["tenantFilter"] != "*" {
		t.Fatalf("platform-admin status = %d filter = %v, want 201/*", rr.Code, body["tenantFilter"])
	}
}

// spec: §25.5 line 2568 (PUT update) + line 2751 (generation bump)
func TestEventSubscriptionUpdateBumpsGeneration(t *testing.T) {
	srv, _ := newServerWithSubs(t)
	rr, body := doJSONReq(t, srv, http.MethodPost, "/v1/admin/event-subscriptions", map[string]any{
		"callbackUrl": "https://acme.example/webhook",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rr.Code)
	}
	id, _ := body["id"].(string)

	rr, body = doJSONReq(t, srv, http.MethodPut, "/v1/admin/event-subscriptions/"+id, map[string]any{
		"description": "pagerduty bridge",
		"active":      false,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200; body = %v", rr.Code, body)
	}
	if body["description"] != "pagerduty bridge" {
		t.Errorf("description = %v, want pagerduty bridge", body["description"])
	}
	if body["active"] != false {
		t.Errorf("active = %v, want false", body["active"])
	}
	if g, _ := body["generation"].(float64); g != 1 {
		t.Errorf("generation = %v, want 1", body["generation"])
	}
}

// spec: §25.5 lines 2723-2733 (rotate-secret)
func TestEventSubscriptionRotateSecret(t *testing.T) {
	srv, _ := newServerWithSubs(t)
	rr, body := doJSONReq(t, srv, http.MethodPost, "/v1/admin/event-subscriptions", map[string]any{
		"callbackUrl": "https://acme.example/webhook",
	})
	id, _ := body["id"].(string)
	firstSecret, _ := body["secret"].(string)
	firstFP, _ := body["secretFingerprint"].(string)

	rr, body = doJSONReq(t, srv, http.MethodPost, "/v1/admin/event-subscriptions/"+id+"/rotate-secret", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, want 200; body = %v", rr.Code, body)
	}
	newSecret, _ := body["secret"].(string)
	if newSecret == "" || newSecret == firstSecret {
		t.Errorf("rotate did not return a fresh secret: %q (was %q)", newSecret, firstSecret)
	}
	if body["secretFingerprint"] == firstFP {
		t.Errorf("rotate did not change the fingerprint")
	}
	if g, _ := body["generation"].(float64); g != 1 {
		t.Errorf("generation after rotate = %v, want 1", body["generation"])
	}
}

// spec: §25.5 line 2569 (deliveries list)
func TestEventSubscriptionDeliveriesEndpoint(t *testing.T) {
	srv, svc := newServerWithSubs(t)
	rr, body := doJSONReq(t, srv, http.MethodPost, "/v1/admin/event-subscriptions", map[string]any{
		"callbackUrl": "https://acme.example/webhook",
	})
	id, _ := body["id"].(string)

	// Seed a delivery row through the service store so the endpoint has
	// something to return; production recording is the worker's job.
	if _, err := svc.Store.RecordDelivery(context.Background(), eventsubscription.Delivery{
		SubscriptionID: id, EventID: "evt-1", EventType: "dev.lenny.alert_fired",
		Status: eventsubscription.DeliveryFailed, Attempts: 3,
	}); err != nil {
		t.Fatalf("seed delivery: %v", err)
	}

	rr, body = doJSONReq(t, srv, http.MethodGet, "/v1/admin/event-subscriptions/"+id+"/deliveries", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("deliveries status = %d, want 200; body = %v", rr.Code, body)
	}
	deliveries, _ := body["deliveries"].([]any)
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(deliveries))
	}

	// A missing subscription id is a 404, not an empty list.
	rr, body = doJSONReq(t, srv, http.MethodGet, "/v1/admin/event-subscriptions/nope/deliveries", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("deliveries for missing sub = %d, want 404", rr.Code)
	}
}

// spec: §25.5 (list + delete with canonical SUBSCRIPTION_NOT_FOUND)
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
	if errObj == nil || errObj["code"] != "SUBSCRIPTION_NOT_FOUND" {
		t.Errorf("error envelope = %v, want code=SUBSCRIPTION_NOT_FOUND", errObj)
	}
}

// doReqAs issues a request with the dev-fallback identity headers set so
// the handler's subscriptionCaller resolves a specific role and tenant
// without a wired AuthConfig.
func doReqAs(t *testing.T, s *opsserver.Server, method, path, role, tenant string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(nil))
	req.Header.Set("X-Lenny-Role", role)
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	req.Header.Set("X-Lenny-Caller", "bob@"+tenant+".example")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	var out map[string]any
	if rr.Body.Len() > 0 {
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
	}
	return rr, out
}

// spec: §25.5 (tenant isolation — the subscription record carries a
// created_by_tenant_id and event delivery respects tenant boundaries) and
// §25.4 ("a tenant-admin caller is still constrained to its tenant regardless
// of scope. Scopes restrict actions; tenancy restricts resources. Both are
// enforced independently.").
// diagnosis: a tenant-admin must not read, list, inspect the deliveries of,
// rotate the secret of, or delete a subscription created by another tenant,
// and List returns only the caller's own tenant's subscriptions. A failure
// here is a cross-tenant isolation breach on the event-subscription surface:
// tenant B can enumerate, exfiltrate the fingerprint of, invalidate the secret
// of, or delete tenant A's webhook subscriptions.
func TestEventSubscriptionTenantOwnershipIsolation(t *testing.T) {
	srv, svc := newServerWithSubs(t)
	ctx := context.Background()

	// Seed one subscription owned by tenant "acme" and one owned by tenant
	// "globex" directly through the service so ownership is unambiguous.
	acmeSub, err := svc.Create(ctx, eventsubscription.CreateRequest{
		CallbackURL: "https://acme.example/webhook",
	}, eventsubscription.Caller{Subject: "alice@acme.example", TenantID: "acme"})
	if err != nil {
		t.Fatalf("seed acme subscription: %v", err)
	}
	globexSub, err := svc.Create(ctx, eventsubscription.CreateRequest{
		CallbackURL: "https://globex.example/webhook",
	}, eventsubscription.Caller{Subject: "bob@globex.example", TenantID: "globex"})
	if err != nil {
		t.Fatalf("seed globex subscription: %v", err)
	}
	if _, err := svc.Store.RecordDelivery(ctx, eventsubscription.Delivery{
		SubscriptionID: acmeSub.ID, EventID: "evt-1", EventType: "dev.lenny.alert_fired",
		Status: eventsubscription.DeliveryFailed, Attempts: 1,
	}); err != nil {
		t.Fatalf("seed delivery: %v", err)
	}

	base := "/v1/admin/event-subscriptions/" + acmeSub.ID

	// A tenant-admin for globex cannot see acme's subscription by id: the
	// resource is not visible to it, so the read fails closed as 404 rather
	// than disclosing the subscription or its secret fingerprint.
	rr, _ := doReqAs(t, srv, http.MethodGet, base, "tenant-admin", "globex")
	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-tenant GET status = %d, want 404 (not visible)", rr.Code)
	}
	rr, _ = doReqAs(t, srv, http.MethodGet, base+"/deliveries", "tenant-admin", "globex")
	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-tenant GET deliveries status = %d, want 404", rr.Code)
	}
	rr, _ = doReqAs(t, srv, http.MethodPost, base+"/rotate-secret", "tenant-admin", "globex")
	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-tenant rotate-secret status = %d, want 404", rr.Code)
	}
	rr, _ = doReqAs(t, srv, http.MethodDelete, base, "tenant-admin", "globex")
	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-tenant DELETE status = %d, want 404", rr.Code)
	}

	// The cross-tenant attempts left acme's subscription and its secret
	// untouched.
	stillThere, err := svc.Store.Get(ctx, acmeSub.ID)
	if err != nil {
		t.Fatalf("acme subscription must survive cross-tenant delete: %v", err)
	}
	if stillThere.SecretFingerprint != acmeSub.SecretFingerprint {
		t.Errorf("acme secret fingerprint changed by cross-tenant rotate: got %q, want %q",
			stillThere.SecretFingerprint, acmeSub.SecretFingerprint)
	}

	// List for a tenant-admin returns only its own tenant's subscriptions.
	rr, body := doReqAs(t, srv, http.MethodGet, "/v1/admin/event-subscriptions", "tenant-admin", "globex")
	if rr.Code != http.StatusOK {
		t.Fatalf("globex list status = %d, want 200", rr.Code)
	}
	subs, _ := body["subscriptions"].([]any)
	ids := map[string]bool{}
	for _, s := range subs {
		m, _ := s.(map[string]any)
		id, _ := m["id"].(string)
		ids[id] = true
	}
	if !ids[globexSub.ID] {
		t.Errorf("globex list missing its own subscription %q: %v", globexSub.ID, ids)
	}
	if ids[acmeSub.ID] {
		t.Errorf("globex list leaked acme's subscription %q", acmeSub.ID)
	}
}

// spec: §25.5 (routes are gated on the service being wired)
func TestEventSubscriptionRoutesAbsentWithoutService(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/event-subscriptions", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no service is wired", rr.Code)
	}
}
