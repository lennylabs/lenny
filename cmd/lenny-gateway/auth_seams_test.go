// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore/tenantstoretest"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// failingTenantStore is a tenantstore.Store whose Get always fails with
// a store error that is not ErrNotFound. Every other method is the nil
// embedded interface and panics if a test reaches it.
type failingTenantStore struct {
	tenantstore.Store
}

func (failingTenantStore) Get(context.Context, string) (tenantstore.Tenant, error) {
	return tenantstore.Tenant{}, errors.New("postgres: connection refused")
}

// registryAdapters returns both request-path tenant registry adapters
// over store, keyed by name.
func registryAdapters(store tenantstore.Store) map[string]auth.TenantRegistry {
	return map[string]auth.TenantRegistry{
		"bearerTenantRegistry":     bearerTenantRegistry{store: store},
		"playgroundTenantRegistry": playgroundTenantRegistry{store: store},
	}
}

// TestTenantRegistryAdaptersApplyRegisteredReading_spec_10_2 runs the
// registered-reading table over the bearer and playground registry
// adapters: only `deleting` and `deleted` rows are refused, a `default`
// row in `deleting` is refused, a missing `default` row is admitted, a
// missing non-default row is refused, and a store error refuses both
// `default` and any other tenant. The `default` store-error and
// `default`-deleting cases fail against an adapter that admits `default`
// without reading the store.
// spec: 10.2 (Authentication: `tenant_id` extraction), 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 27.3.1
func TestTenantRegistryAdaptersApplyRegisteredReading_spec_10_2(t *testing.T) {
	ctx := context.Background()
	store := tenantstore.NewMemory()
	for _, c := range tenantstoretest.RegistrationCases() {
		row, err := c.Seed(ctx, store, "acme-"+c.Name)
		if err != nil {
			t.Fatal(err)
		}
		for name, reg := range registryAdapters(store) {
			got, err := reg.IsRegistered(row.ID)
			if err != nil {
				t.Fatalf("%s.IsRegistered(%s): %v", name, c.Name, err)
			}
			if got != c.Registered {
				t.Errorf("%s.IsRegistered(%s) = %v, want %v", name, c.Name, got, c.Registered)
			}
		}
	}

	for name, reg := range registryAdapters(store) {
		if ok, err := reg.IsRegistered(auth.DefaultTenantID); err != nil || !ok {
			t.Errorf("%s: missing default row = %v, %v; want admitted", name, ok, err)
		}
		if ok, err := reg.IsRegistered("globex"); err != nil || ok {
			t.Errorf("%s: missing non-default row = %v, %v; want refused", name, ok, err)
		}
	}

	deleting, ok := tenantstoretest.RegistrationCase{}, false
	for _, c := range tenantstoretest.RegistrationCases() {
		if c.Name == tenantstore.TenantStateDeleting {
			deleting, ok = c, true
		}
	}
	if !ok {
		t.Fatal("registration fixtures carry no deleting case")
	}
	if _, err := deleting.Seed(ctx, store, auth.DefaultTenantID); err != nil {
		t.Fatal(err)
	}
	for name, reg := range registryAdapters(store) {
		if ok, err := reg.IsRegistered(auth.DefaultTenantID); err != nil || ok {
			t.Errorf("%s: default in deleting = %v, %v; want refused", name, ok, err)
		}
	}

	for name, reg := range registryAdapters(failingTenantStore{}) {
		for _, id := range []string{auth.DefaultTenantID, "acme"} {
			ok, err := reg.IsRegistered(id)
			if err == nil || ok {
				t.Errorf("%s: store error for %q = %v, %v; want an error and not registered", name, id, ok, err)
			}
		}
	}
}

// recordingAuditLog is an admin.AuditLog that records each append.
type recordingAuditLog struct {
	appends []recordedAppend
}

type recordedAppend struct {
	tenantID  string
	eventType string
	payload   map[string]any
}

func (l *recordingAuditLog) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return audit.Row{}, err
	}
	l.appends = append(l.appends, recordedAppend{tenantID: tenantID, eventType: eventType, payload: decoded})
	return audit.Row{}, nil
}

func (l *recordingAuditLog) Rows(context.Context, string) ([]audit.Row, error) { return nil, nil }

func (l *recordingAuditLog) Verify(context.Context, string) (audit.VerifyResult, error) {
	return audit.VerifyResult{}, nil
}

// TestAuthFailureTenantNotFoundOnPlatformChain_spec_11_7 pins the chain
// placement of auth_failure rows. A TENANT_NOT_FOUND rejection is
// appended under the platform tenant with the refused tenant as the
// payload's top-level target_tenant_id, the key the §11.7 cross-tenant
// residency gate reads, and nothing is appended to the refused tenant's
// chain. TENANT_CLAIM_INVALID_FORMAT keeps its row on the inferred
// tenant's chain, and TENANT_CLAIM_MISSING lands on the platform chain
// with no target.
// spec: 11.7 (Audit Logging), 10.2 (Authentication: `tenant_id` extraction), 12.8 (Compliance Interfaces: Tenant deletion lifecycle)
func TestAuthFailureTenantNotFoundOnPlatformChain_spec_11_7(t *testing.T) {
	log := &recordingAuditLog{}
	adapter := authFailureAuditAdapter{sink: admin.NewAuditLogSink(log, time.Now)}
	ctx := context.Background()
	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	adapter.EmitAuthFailure(ctx, authmw.AuthFailureEvent{
		Reason: "TENANT_NOT_FOUND", TenantID: "acme", UserID: "alice@acme.com", JTI: "j1", At: at,
	})
	adapter.EmitAuthFailure(ctx, authmw.AuthFailureEvent{
		Reason: "TENANT_CLAIM_INVALID_FORMAT", TenantID: "acme", UserID: "bob@acme.com", JTI: "j2", At: at,
	})
	adapter.EmitAuthFailure(ctx, authmw.AuthFailureEvent{
		Reason: "TENANT_CLAIM_MISSING", UserID: "carol@acme.com", JTI: "j3", At: at,
	})

	if len(log.appends) != 3 {
		t.Fatalf("appends = %d, want 3", len(log.appends))
	}
	notFound := log.appends[0]
	if notFound.tenantID != "platform" {
		t.Errorf("TENANT_NOT_FOUND chain = %q, want platform", notFound.tenantID)
	}
	if got := notFound.payload["target_tenant_id"]; got != "acme" {
		t.Errorf("TENANT_NOT_FOUND top-level target_tenant_id = %v, want acme", got)
	}
	if got := notFound.payload["actor_tenant_id"]; got != "" {
		t.Errorf("TENANT_NOT_FOUND actor_tenant_id = %v, want empty", got)
	}
	if detail, _ := notFound.payload["detail"].(map[string]any); detail["tenant_id"] != "acme" || detail["reason"] != "TENANT_NOT_FOUND" {
		t.Errorf("TENANT_NOT_FOUND detail = %v", notFound.payload["detail"])
	}

	invalid := log.appends[1]
	if invalid.tenantID != "acme" {
		t.Errorf("TENANT_CLAIM_INVALID_FORMAT chain = %q, want acme", invalid.tenantID)
	}
	if _, ok := invalid.payload["target_tenant_id"]; ok {
		t.Errorf("TENANT_CLAIM_INVALID_FORMAT carries target_tenant_id %v; want none", invalid.payload["target_tenant_id"])
	}

	missing := log.appends[2]
	if missing.tenantID != "platform" {
		t.Errorf("TENANT_CLAIM_MISSING chain = %q, want platform", missing.tenantID)
	}
	if _, ok := missing.payload["target_tenant_id"]; ok {
		t.Errorf("TENANT_CLAIM_MISSING carries target_tenant_id; want none")
	}
	for _, a := range log.appends {
		if a.eventType != authmw.AuthFailureEventType {
			t.Errorf("event type = %q, want %q", a.eventType, authmw.AuthFailureEventType)
		}
	}
}

// hs256 signs payload as an HS256 JWT under secret and kid, so a test
// can carry claims that jwt.Claims has no typed field for.
func hs256(t *testing.T, secret []byte, kid string, payload map[string]any) string {
	t.Helper()
	hb, err := json.Marshal(map[string]any{"alg": "HS256", "typ": "JWT", "kid": kid})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type admitAllRegistry struct{}

func (admitAllRegistry) IsRegistered(string) (bool, error) { return true, nil }

// TestBearerTenantClaimFollowsVerifyingKey_spec_10_2 drives the gateway's
// Embedded Mode verifier composition through authmw.Wrap under a
// non-default --tenant-id-claim. A Token Service user_bearer resolves
// from `tenant_id` and is not refused for lacking the configured claim.
// An embedded-key token resolves from the configured claim even when its
// `tenant_id` names another tenant. An embedded-key token that carries
// `tenant_id` but lacks the configured claim is refused with
// TENANT_CLAIM_MISSING, so an IdP-signed payload cannot pick its tenant
// through `tenant_id`.
// spec: 10.2 (Authentication: `tenant_id` extraction), 17.4
func TestBearerTenantClaimFollowsVerifyingKey_spec_10_2(t *testing.T) {
	const claim = "https://acme.example/tenant"
	primarySecret := []byte("token-service-secret-abcdefghijklmnop")
	embeddedSecret := []byte("embedded-signing-secret-0123456789")
	primary := jwt.NewHMACSigner("token-service-kid", primarySecret)
	embedded := jwt.NewHMACSigner("embedded-kid", embeddedSecret)
	verifier := embeddedBearerVerifier(primary, embedded, claim)
	exp := time.Now().Add(time.Hour).Unix()

	cases := []struct {
		name       string
		token      string
		wantStatus int
		wantTenant string
		wantCode   string
	}{
		{
			name: "token service bearer resolves from tenant_id",
			token: hs256(t, primarySecret, "token-service-kid", map[string]any{
				"sub": "alice@acme.com", "exp": exp, "typ": string(auth.TokenUserBearer), "tenant_id": "acme",
			}),
			wantStatus: http.StatusNoContent, wantTenant: "acme",
		},
		{
			name: "embedded token resolves from the configured claim",
			token: hs256(t, embeddedSecret, "embedded-kid", map[string]any{
				"sub": "bob@acme.com", "exp": exp, "typ": string(auth.TokenUserBearer), "aud": []string{embeddedOIDCAudience},
				"tenant_id": "globex", claim: "acme",
			}),
			wantStatus: http.StatusNoContent, wantTenant: "acme",
		},
		{
			name: "embedded token without the configured claim is refused",
			token: hs256(t, embeddedSecret, "embedded-kid", map[string]any{
				"sub": "carol@acme.com", "exp": exp, "typ": string(auth.TokenUserBearer), "aud": []string{embeddedOIDCAudience},
				"tenant_id": "acme",
			}),
			wantStatus: http.StatusUnauthorized, wantCode: "TENANT_CLAIM_MISSING",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got authmw.Principal
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, _ = authmw.FromContext(r.Context())
				w.WriteHeader(http.StatusNoContent)
			})
			h := authmw.Wrap(inner, authmw.Options{
				Verifier:    verifier,
				MultiTenant: true,
				Registry:    admitAllRegistry{},
			})
			req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if tc.wantTenant != "" && got.TenantID != tc.wantTenant {
				t.Errorf("tenant = %q, want %q", got.TenantID, tc.wantTenant)
			}
			if tc.wantCode != "" && !strings.Contains(rr.Body.String(), tc.wantCode) {
				t.Errorf("body = %s, want code %s", rr.Body.String(), tc.wantCode)
			}
		})
	}
}
