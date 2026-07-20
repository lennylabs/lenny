// SPDX-License-Identifier: MIT

package eventsubscription_test

import (
	"context"
	"net/netip"
	"reflect"
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

// spec: §25.5 — the `PUT /v1/admin/event-subscriptions/{id}` endpoint
// "Update subscription filters", and `ops_event_subscriptions` persists
// those filters in the `types TEXT[] NOT NULL` and `severity TEXT[]`
// columns. Update must patch the Types and Severity filter fields the
// same way it patches Description and Active, and must reject an
// unrecognized severity with INVALID_EVENT_FILTER ("Unrecognized event
// type or severity in filter") exactly as Create does.
//
// diagnosis: TestServiceUpdate_spec_25_5 only ever sends
// Description/Active through UpdateRequest, so the branches in
// Service.Update that normalize and store req.Types/req.Severity (and
// the validation error path for a bad severity) were never executed by
// any test.
func TestServiceUpdateFilters_spec_25_5(t *testing.T) {
	svc, _ := newService()
	rev, _ := svc.Create(context.Background(), es.CreateRequest{CallbackURL: "https://acme.example/hook"}, platformAdmin)

	types := []string{"dev.lenny.session_failed", "dev.lenny.alert_fired"}
	severity := []string{"WARNING", "critical"}
	got, err := svc.Update(context.Background(), rev.ID, es.UpdateRequest{Types: &types, Severity: &severity}, platformAdmin)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	wantTypes := []string{"dev.lenny.alert_fired", "dev.lenny.session_failed"}
	wantSeverity := []string{"critical", "warning"}
	if !reflect.DeepEqual(got.Types, wantTypes) {
		t.Errorf("update Types = %v, want %v", got.Types, wantTypes)
	}
	if !reflect.DeepEqual(got.Severity, wantSeverity) {
		t.Errorf("update Severity = %v, want %v", got.Severity, wantSeverity)
	}
	if got.Generation != 1 {
		t.Errorf("update Generation = %d, want 1", got.Generation)
	}

	// The persisted record carries the same normalized filters the
	// read-view reported, confirming the patch reached the Store and
	// not just the returned view.
	rec, err := svc.Store.Get(context.Background(), rev.ID)
	if err != nil {
		t.Fatalf("store Get: %v", err)
	}
	if !reflect.DeepEqual(rec.Types, wantTypes) || !reflect.DeepEqual(rec.Severity, wantSeverity) {
		t.Errorf("stored record Types=%v Severity=%v, want %v / %v", rec.Types, rec.Severity, wantTypes, wantSeverity)
	}

	// An unrecognized severity on Update is rejected the same way Create
	// rejects it, and leaves the previously patched filters untouched.
	badSeverity := []string{"emergency"}
	if _, err := svc.Update(context.Background(), rev.ID, es.UpdateRequest{Severity: &badSeverity}, platformAdmin); es.CodeOf(err) != es.ErrCodeInvalidFilter {
		t.Errorf("update bad-severity err = %v, want INVALID_EVENT_FILTER", err)
	}
	unchanged, err := svc.Get(context.Background(), rev.ID, platformAdmin)
	if err != nil {
		t.Fatalf("Get after rejected update: %v", err)
	}
	if !reflect.DeepEqual(unchanged.Severity, wantSeverity) || unchanged.Generation != 1 {
		t.Errorf("rejected update mutated state: Severity=%v Generation=%d, want %v / 1", unchanged.Severity, unchanged.Generation, wantSeverity)
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
	got, _, err := svc.ListDeliveries(context.Background(), rev.ID, "", 0, platformAdmin)
	if err != nil || len(got) != 2 {
		t.Fatalf("ListDeliveries = %d (%v), want 2", len(got), err)
	}
	if got[0].ID < got[1].ID {
		t.Errorf("deliveries not newest-first: %v", got)
	}
	if _, _, err := svc.ListDeliveries(context.Background(), "missing", "", 0, platformAdmin); es.CodeOf(err) != es.ErrCodeNotFound {
		t.Errorf("ListDeliveries missing err = %v, want SUBSCRIPTION_NOT_FOUND", err)
	}
}

// TestMemoryStoreListDeliveriesKeysetPagination pins the §25.5 deliveries
// keyset walk over the delivery primary key: an empty cursor returns the
// newest page with a continuation cursor and hasMore, the continuation
// cursor returns the adjacent page with no overlap and no gap, the final
// page returns hasMore:false with an empty cursor, an empty subscription
// returns an empty page with hasMore:false, and a cursor whose row has
// aged out below the oldest retained delivery returns gapDetected with
// oldestAvailableCursor rather than a silently empty page. The Memory
// store mirrors the pgstore keyset so both back the same wire contract.
//
// spec: §25.5 (deliveries keyset pagination, gap on aged-out cursor).
func TestMemoryStoreListDeliveriesKeysetPagination_spec_25_5(t *testing.T) {
	ctx := context.Background()
	store := es.NewMemoryStore()
	// Record five deliveries; the first four expire in the past so the
	// retention sweep can age them out, the fifth survives. Ids are
	// assigned 1..5 in insertion order.
	past := time.Now().Add(-1 * time.Hour)
	future := time.Now().Add(24 * time.Hour)
	for i := 0; i < 5; i++ {
		exp := past
		if i == 4 {
			exp = future
		}
		if _, err := store.RecordDelivery(ctx, es.Delivery{
			SubscriptionID: "sub-1", EventID: "e", EventType: "t",
			Status: es.DeliveryDelivered, ExpiresAt: exp,
		}); err != nil {
			t.Fatalf("RecordDelivery %d: %v", i, err)
		}
	}

	// First page: newest two (ids 5,4), continuation cursor "4".
	page1, meta1, err := store.ListDeliveries(ctx, "sub-1", "", 2)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(page1) != 2 || page1[0].ID != 5 || page1[1].ID != 4 {
		t.Fatalf("first page ids = %v, want [5 4]", deliveryIDs(page1))
	}
	if !meta1.HasMore || meta1.Cursor != "4" || meta1.CursorKind != es.CursorKindPK {
		t.Fatalf("first page meta = %+v, want hasMore, cursor 4, cursorKind pk", meta1)
	}

	// Second page from the continuation cursor: ids 3,2, no overlap.
	page2, meta2, err := store.ListDeliveries(ctx, "sub-1", meta1.Cursor, 2)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(page2) != 2 || page2[0].ID != 3 || page2[1].ID != 2 {
		t.Fatalf("second page ids = %v, want [3 2]", deliveryIDs(page2))
	}
	if !meta2.HasMore || meta2.Cursor != "2" {
		t.Fatalf("second page meta = %+v, want hasMore, cursor 2", meta2)
	}

	// Final page: the last row (id 1), hasMore:false, empty cursor.
	page3, meta3, err := store.ListDeliveries(ctx, "sub-1", meta2.Cursor, 2)
	if err != nil {
		t.Fatalf("final page: %v", err)
	}
	if len(page3) != 1 || page3[0].ID != 1 {
		t.Fatalf("final page ids = %v, want [1]", deliveryIDs(page3))
	}
	if meta3.HasMore || meta3.Cursor != "" || meta3.GapDetected {
		t.Fatalf("final page meta = %+v, want no more, empty cursor, no gap", meta3)
	}

	// An empty subscription returns an empty page with no gap.
	empty, metaEmpty, err := store.ListDeliveries(ctx, "sub-none", "", 2)
	if err != nil {
		t.Fatalf("empty subscription: %v", err)
	}
	if len(empty) != 0 || metaEmpty.HasMore || metaEmpty.GapDetected {
		t.Fatalf("empty subscription meta = %+v (%d rows), want empty non-gap page", metaEmpty, len(empty))
	}

	// Age out the four expired rows (ids 1-4). The oldest retained delivery
	// is now id 5. A continuation cursor of "4" (its row purged, below the
	// retention floor) must report a gap toward the oldest retained cursor
	// rather than an indistinguishable empty page.
	if purged, err := store.DeleteExpired(ctx, time.Now(), 100); err != nil || purged != 4 {
		t.Fatalf("DeleteExpired = %d (%v), want 4", purged, err)
	}
	aged, metaAged, err := store.ListDeliveries(ctx, "sub-1", "4", 2)
	if err != nil {
		t.Fatalf("aged-out page: %v", err)
	}
	if len(aged) != 0 {
		t.Fatalf("aged-out page returned %d rows, want 0", len(aged))
	}
	if !metaAged.GapDetected || metaAged.OldestAvailableCursor != "5" {
		t.Fatalf("aged-out meta = %+v, want gapDetected with oldestAvailableCursor 5", metaAged)
	}
	// The canonical §25.2 gap envelope is four fields: without the reason and
	// the resync hint the caller has no documented recovery step.
	if metaAged.GapReason != es.GapReasonAgedOut || metaAged.SuggestedAction != es.SuggestedActionResync {
		t.Errorf("aged-out meta = %+v, want gapReason %q and suggestedAction %q", metaAged, es.GapReasonAgedOut, es.SuggestedActionResync)
	}

	// A malformed cursor cannot be honored and reports the same gap toward
	// the oldest retained delivery.
	_, metaBad, err := store.ListDeliveries(ctx, "sub-1", "not-an-int", 2)
	if err != nil {
		t.Fatalf("malformed cursor: %v", err)
	}
	if !metaBad.GapDetected || metaBad.OldestAvailableCursor != "5" {
		t.Fatalf("malformed-cursor meta = %+v, want gapDetected with oldestAvailableCursor 5", metaBad)
	}
	if metaBad.GapReason != es.GapReasonUnresolvable || metaBad.SuggestedAction != es.SuggestedActionResync {
		t.Errorf("malformed-cursor meta = %+v, want gapReason %q and suggestedAction %q", metaBad, es.GapReasonUnresolvable, es.SuggestedActionResync)
	}
}

func deliveryIDs(ds []es.Delivery) []int64 {
	ids := make([]int64, len(ds))
	for i, d := range ds {
		ids[i] = d.ID
	}
	return ids
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

// spec: §25.5 error-codes table ("`INVALID_EVENT_FILTER` | `PERMANENT` |
// 400 | Unrecognized event type or severity in filter") and §16.6
// ("Every event is a CloudEvents v1.0.2 ... record. The CloudEvents
// `type` attribute is `dev.lenny.<short_name>` where `<short_name>` is
// the identifier listed below ... the catalog below is the canonical
// enumeration."). An unrecognized Types entry on Create or Update must
// be rejected the same way an unrecognized Severity entry already is; a
// catalog entry (with or without the CloudEvents prefix) must still be
// accepted.
func TestServiceInvalidFilterType_spec_25_5(t *testing.T) {
	svc, _ := newService()

	if _, err := svc.Create(context.Background(), es.CreateRequest{
		CallbackURL: "https://acme.example/hook", Types: []string{"dev.lenny.not_a_real_event_type"},
	}, platformAdmin); es.CodeOf(err) != es.ErrCodeInvalidFilter {
		t.Errorf("bad-type Create err = %v, want INVALID_EVENT_FILTER", err)
	}

	rev, err := svc.Create(context.Background(), es.CreateRequest{
		CallbackURL: "https://acme.example/hook", Types: []string{"dev.lenny.alert_fired", "pool_state_changed"},
	}, platformAdmin)
	if err != nil {
		t.Fatalf("Create with catalog types: %v", err)
	}

	badTypes := []string{"not_a_real_event_type"}
	if _, err := svc.Update(context.Background(), rev.ID, es.UpdateRequest{Types: &badTypes}, platformAdmin); es.CodeOf(err) != es.ErrCodeInvalidFilter {
		t.Errorf("bad-type Update err = %v, want INVALID_EVENT_FILTER", err)
	}

	// The rejected Update left the previously stored (valid) filter
	// untouched.
	unchanged, err := svc.Get(context.Background(), rev.ID, platformAdmin)
	if err != nil {
		t.Fatalf("Get after rejected update: %v", err)
	}
	wantTypes := []string{"dev.lenny.alert_fired", "pool_state_changed"}
	if !reflect.DeepEqual(unchanged.Types, wantTypes) {
		t.Errorf("rejected update mutated Types: got %v, want %v", unchanged.Types, wantTypes)
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
