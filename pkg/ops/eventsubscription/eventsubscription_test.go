// SPDX-License-Identifier: MIT

package eventsubscription_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	es "github.com/lennylabs/lenny/pkg/ops/eventsubscription"
)

// publicResolver resolves every host to a fixed public IP so the SSRF
// validator's DNS step runs without real name resolution.
type publicResolver struct{}

func (publicResolver) LookupNetIP(_ context.Context, _, _ string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

// recordingAudit collects emitted audit events for assertion.
type recordingAudit struct{ events []es.AuditEvent }

func (r *recordingAudit) Emit(ev es.AuditEvent) { r.events = append(r.events, ev) }

func newService() (*es.Service, *recordingAudit) {
	svc := es.NewService(es.NewMemoryStore())
	svc.SSRF = es.NewSSRFValidator(es.SSRFConfig{Resolver: publicResolver{}})
	audit := &recordingAudit{}
	svc.SetAuditSink(audit)
	return svc, audit
}

var platformAdmin = es.Caller{Subject: "alice@acme.com", PlatformAdmin: true}

// spec: §25.5 lines 2702-2733 — Create generates a secret server-side,
// stores only the hash + fingerprint, returns the plaintext once, and
// emits subscription_created with the fingerprint.
func TestServiceCreateSecretLifecycle_spec_25_5(t *testing.T) {
	svc, audit := newService()
	rev, err := svc.Create(context.Background(), es.CreateRequest{
		CallbackURL: "https://acme.example/hook",
		Types:       []string{"dev.lenny.alert_fired"},
	}, platformAdmin)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rev.Secret == "" || rev.SecretRotationWarning == "" {
		t.Fatalf("Create did not reveal secret + warning: %+v", rev)
	}
	if rev.SecretFingerprint != es.Fingerprint(rev.Secret) {
		t.Errorf("fingerprint %q does not match secret", rev.SecretFingerprint)
	}

	// The stored record carries the hash, not the plaintext.
	rec, err := svc.Store.Get(context.Background(), rev.ID)
	if err != nil {
		t.Fatalf("store Get: %v", err)
	}
	if rec.SecretHash != es.HashSecret(rev.Secret) {
		t.Errorf("stored secret_hash mismatch")
	}
	if rec.CreatedBy != "alice@acme.com" {
		t.Errorf("createdBy = %q, want alice@acme.com", rec.CreatedBy)
	}

	// The read-view omits secret material entirely.
	got, _ := svc.Get(context.Background(), rev.ID, platformAdmin)
	if got.SecretFingerprint != rev.SecretFingerprint {
		t.Errorf("view fingerprint = %q, want %q", got.SecretFingerprint, rev.SecretFingerprint)
	}

	if len(audit.events) != 1 || audit.events[0].Type != es.EventSubscriptionCreated {
		t.Fatalf("audit = %+v, want one subscription_created", audit.events)
	}
	if audit.events[0].Details["secretFingerprint"] != rev.SecretFingerprint {
		t.Errorf("audit fingerprint = %v, want %s", audit.events[0].Details["secretFingerprint"], rev.SecretFingerprint)
	}
}

// spec: §25.5 lines 2723-2733 — RotateSecret generates a new secret,
// records the previous fingerprint + rotation time for the overlap
// window, bumps the generation, and emits subscription_secret_rotated.
func TestServiceRotateSecret_spec_25_5(t *testing.T) {
	svc, audit := newService()
	rev, _ := svc.Create(context.Background(), es.CreateRequest{CallbackURL: "https://acme.example/hook"}, platformAdmin)

	rot, err := svc.RotateSecret(context.Background(), rev.ID, platformAdmin)
	if err != nil {
		t.Fatalf("RotateSecret: %v", err)
	}
	if rot.Secret == rev.Secret {
		t.Fatalf("rotate returned the same secret")
	}
	rec, _ := svc.Store.Get(context.Background(), rev.ID)
	if rec.PreviousSecretFingerprint != rev.SecretFingerprint {
		t.Errorf("previous fingerprint = %q, want %q", rec.PreviousSecretFingerprint, rev.SecretFingerprint)
	}
	if rec.Generation != 1 {
		t.Errorf("generation = %d, want 1", rec.Generation)
	}
	if !rec.WithinRotationOverlap(rec.SecretRotatedAt.Add(30 * time.Second)) {
		t.Errorf("expected within overlap 30s after rotation")
	}
	if rec.WithinRotationOverlap(rec.SecretRotatedAt.Add(90 * time.Second)) {
		t.Errorf("expected outside overlap 90s after rotation")
	}
	if audit.events[len(audit.events)-1].Type != es.EventSubscriptionSecretRotated {
		t.Errorf("last audit = %s, want subscription_secret_rotated", audit.events[len(audit.events)-1].Type)
	}
}

// spec: §25.5 lines 2758-2766 — tenant isolation gate.
func TestServiceTenantIsolation_spec_25_5(t *testing.T) {
	svc, _ := newService()
	tenantAdmin := es.Caller{Subject: "bob@acme.com", TenantID: "acme"}

	// Wildcard from a tenant-admin is forbidden.
	if _, err := svc.Create(context.Background(), es.CreateRequest{
		CallbackURL: "https://acme.example/hook", TenantFilter: "*",
	}, tenantAdmin); es.CodeOf(err) != es.ErrCodeTenantForbidden {
		t.Errorf("wildcard tenant-admin err = %v, want SUBSCRIPTION_TENANT_FORBIDDEN", err)
	}
	// Cross-tenant from a tenant-admin is forbidden.
	if _, err := svc.Create(context.Background(), es.CreateRequest{
		CallbackURL: "https://acme.example/hook", TenantFilter: "globex",
	}, tenantAdmin); es.CodeOf(err) != es.ErrCodeTenantForbidden {
		t.Errorf("cross-tenant err = %v, want SUBSCRIPTION_TENANT_FORBIDDEN", err)
	}
	// Own tenant (defaulted) succeeds and records created_by_tenant_id.
	rev, err := svc.Create(context.Background(), es.CreateRequest{CallbackURL: "https://acme.example/hook"}, tenantAdmin)
	if err != nil {
		t.Fatalf("own-tenant Create: %v", err)
	}
	if rev.TenantFilter != "acme" || rev.CreatedByTenantID != "acme" {
		t.Errorf("tenant-admin row filter=%q tenant=%q, want acme/acme", rev.TenantFilter, rev.CreatedByTenantID)
	}
	// platform-admin defaults to the wildcard with a NULL tenant.
	rev, _ = svc.Create(context.Background(), es.CreateRequest{CallbackURL: "https://acme.example/hook"}, platformAdmin)
	if rev.TenantFilter != es.TenantFilterAll || rev.CreatedByTenantID != "" {
		t.Errorf("platform-admin row filter=%q tenant=%q, want */empty", rev.TenantFilter, rev.CreatedByTenantID)
	}
}

// spec: §25.5 line 2568, line 2751 — Update patches fields and bumps the
// generation; an unknown id is SUBSCRIPTION_NOT_FOUND.
func TestServiceUpdate_spec_25_5(t *testing.T) {
	svc, _ := newService()
	rev, _ := svc.Create(context.Background(), es.CreateRequest{CallbackURL: "https://acme.example/hook"}, platformAdmin)

	desc := "bridge"
	active := false
	got, err := svc.Update(context.Background(), rev.ID, es.UpdateRequest{Description: &desc, Active: &active}, platformAdmin)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Description != "bridge" || got.Active != false || got.Generation != 1 {
		t.Errorf("update result = %+v, want desc=bridge active=false gen=1", got)
	}
	if _, err := svc.Update(context.Background(), "missing", es.UpdateRequest{Description: &desc}, platformAdmin); es.CodeOf(err) != es.ErrCodeNotFound {
		t.Errorf("update missing err = %v, want SUBSCRIPTION_NOT_FOUND", err)
	}
}

// spec: §25.5 line 2806 — Delete emits subscription_deleted; an unknown
// id is SUBSCRIPTION_NOT_FOUND.
func TestServiceDelete_spec_25_5(t *testing.T) {
	svc, audit := newService()
	rev, _ := svc.Create(context.Background(), es.CreateRequest{CallbackURL: "https://acme.example/hook"}, platformAdmin)
	if err := svc.Delete(context.Background(), rev.ID, platformAdmin); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := audit.events[len(audit.events)-1].Type; got != es.EventSubscriptionDeleted {
		t.Errorf("last audit = %s, want subscription_deleted", got)
	}
	if err := svc.Delete(context.Background(), rev.ID, platformAdmin); es.CodeOf(err) != es.ErrCodeNotFound {
		t.Errorf("double-delete err = %v, want SUBSCRIPTION_NOT_FOUND", err)
	}
}

// spec: §25.5 line 2569 — ListDeliveries returns recorded rows
// newest-first and 404s for a missing subscription.
func TestServiceListDeliveries_spec_25_5(t *testing.T) {
	svc, _ := newService()
	rev, _ := svc.Create(context.Background(), es.CreateRequest{CallbackURL: "https://acme.example/hook"}, platformAdmin)
	for i := 0; i < 2; i++ {
		if _, err := svc.Store.RecordDelivery(context.Background(), es.Delivery{
			SubscriptionID: rev.ID, EventID: "e", EventType: "t", Status: es.DeliveryDelivered,
		}); err != nil {
			t.Fatalf("RecordDelivery: %v", err)
		}
	}
	got, err := svc.ListDeliveries(context.Background(), rev.ID, 0, platformAdmin)
	if err != nil || len(got) != 2 {
		t.Fatalf("ListDeliveries = %d (%v), want 2", len(got), err)
	}
	if got[0].ID < got[1].ID {
		t.Errorf("deliveries not newest-first: %v", got)
	}
	if _, err := svc.ListDeliveries(context.Background(), "missing", 0, platformAdmin); es.CodeOf(err) != es.ErrCodeNotFound {
		t.Errorf("ListDeliveries missing err = %v, want SUBSCRIPTION_NOT_FOUND", err)
	}
}

// spec: §25.5 lines 2795-2802 — an invalid severity in the filter is
// INVALID_EVENT_FILTER.
func TestServiceInvalidFilter_spec_25_5(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.Create(context.Background(), es.CreateRequest{
		CallbackURL: "https://acme.example/hook", Severity: []string{"emergency"},
	}, platformAdmin); es.CodeOf(err) != es.ErrCodeInvalidFilter {
		t.Errorf("bad-severity err = %v, want INVALID_EVENT_FILTER", err)
	}
}

// spec: §25.5 line 2702 — secret entropy + fingerprint helpers.
func TestSecretHelpers_spec_25_5(t *testing.T) {
	a, err := es.GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	b, _ := es.GenerateSecret()
	if a == b {
		t.Fatalf("GenerateSecret returned identical secrets")
	}
	if len(es.HashSecret(a)) != 64 {
		t.Errorf("HashSecret length = %d, want 64 hex chars", len(es.HashSecret(a)))
	}
	if es.Fingerprint(a) != es.HashSecret(a)[:8] {
		t.Errorf("Fingerprint is not the first 8 chars of the hash")
	}
}
