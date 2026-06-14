// SPDX-License-Identifier: MIT

// Package eventsubscription is the §25.5 webhook-subscription service.
// It owns the persisted subscription Record, the redacted read-view
// Subscription, the secret lifecycle (server-generated secret, SHA-256
// hash-at-rest, fingerprint, rotation with a dual-secret overlap
// window), the SSRF/DNS-rebinding validation of the callback URL, the
// tenant-isolation gate, the delivery-tracking Record, and the
// canonical §25.5 error codes the §25.2 envelope reports.
//
// The lenny-ops opsserver wires its CRUD handlers against the Service;
// the §25.5 webhook delivery worker (pkg/ops/opsservice/webhookloop)
// reads the active subscriptions through a small adapter so a Store
// change is visible to the worker without a process restart.
//
// spec: §25.5 lines 2613-2806.
package eventsubscription

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Canonical §25.5 error codes for the subscription surface. The HTTP
// status and §25.2 category each maps to are fixed in the opsserver
// error map. spec: §25.5 lines 2795-2802, line 2763.
const (
	// ErrCodeInvalidFilter rejects an unrecognized event type or
	// severity in the filter (400).
	ErrCodeInvalidFilter = "INVALID_EVENT_FILTER"
	// ErrCodeWebhookValidation rejects a callback URL that failed SSRF
	// validation (422).
	ErrCodeWebhookValidation = "WEBHOOK_VALIDATION_FAILED"
	// ErrCodeNotFound reports a missing subscription id (404).
	ErrCodeNotFound = "SUBSCRIPTION_NOT_FOUND"
	// ErrCodeNoDurableStore reports the subscription CRUD store is
	// unreachable, e.g. a Postgres outage (503).
	ErrCodeNoDurableStore = "SUBSCRIPTION_STORE_UNAVAILABLE"
	// ErrCodeTenantForbidden rejects a tenant-admin caller that tries to
	// register a wildcard or cross-tenant subscription (403).
	ErrCodeTenantForbidden = "SUBSCRIPTION_TENANT_FORBIDDEN"
)

// Error returned by Service methods carries one of the §25.5 canonical
// codes via CodeOf. Each error wraps a typed Error so the opsserver can
// map it to an HTTP status without string matching.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// CodeOf returns the canonical error code embedded in err, or empty
// when err is not a typed Error.
func CodeOf(err error) string {
	if err == nil {
		return ""
	}
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
}

// TenantFilterAll is the §25.5 line 2760 wildcard tenant filter that
// matches every event, including platform-scoped events that carry no
// tenant label. A tenant-admin may not register it.
const TenantFilterAll = "*"

// rotationOverlapWindow is the §25.5 line 2729 dual-secret overlap: for
// this long after a rotation, a delivery may be signed with either the
// previous or the new secret so a receiver can be updated without
// dropping deliveries.
const rotationOverlapWindow = 60 * time.Second

// secretRotationWarning is the §25.5 lines 2705-2709 single-use notice
// returned alongside a freshly generated secret.
const secretRotationWarning = "This is the only time the secret will be returned. Store it securely. To rotate, delete and recreate this subscription or POST to /v1/admin/event-subscriptions/{id}/rotate-secret."

// Record is one persisted §25.5 webhook subscription. It holds the
// SHA-256 secret hash and fingerprint rather than the plaintext secret;
// the plaintext is returned once on Create/RotateSecret and never read
// back. spec: §25.5 lines 2613-2647, 2715-2733.
type Record struct {
	ID                        string
	CallbackURL               string
	Types                     []string
	Severity                  []string
	Description               string
	SecretHash                string
	SecretFingerprint         string
	PreviousSecretFingerprint string
	SecretRotatedAt           time.Time
	CreatedBy                 string
	CreatedByTenantID         string
	TenantFilter              string
	Generation                int64
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	Active                    bool
}

// View returns the redacted read-view of the record. The secret hash is
// never exposed; only the fingerprint is, so an operator can confirm
// which secret is in effect without learning it. spec: §25.5 line 2718.
func (r Record) View() Subscription {
	return Subscription{
		ID:                r.ID,
		CallbackURL:       r.CallbackURL,
		Types:             append([]string(nil), r.Types...),
		Severity:          append([]string(nil), r.Severity...),
		Description:       r.Description,
		SecretFingerprint: r.SecretFingerprint,
		CreatedBy:         r.CreatedBy,
		CreatedByTenantID: r.CreatedByTenantID,
		TenantFilter:      r.TenantFilter,
		Generation:        r.Generation,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
		Active:            r.Active,
	}
}

// WithinRotationOverlap reports whether the previous secret is still
// honored: true for rotationOverlapWindow after the last rotation. The
// delivery worker consults this to decide whether to also accept the
// prior secret during the cut-over. spec: §25.5 line 2729.
func (r Record) WithinRotationOverlap(now time.Time) bool {
	if r.SecretRotatedAt.IsZero() || r.PreviousSecretFingerprint == "" {
		return false
	}
	return now.Sub(r.SecretRotatedAt) < rotationOverlapWindow
}

// Subscription is the redacted §25.5 read-view returned by the list and
// get endpoints. It carries no secret material — only the fingerprint.
// The plaintext secret is returned once via Reveal on Create/Rotate.
// spec: §25.5 lines 2613-2647, 2711-2713.
type Subscription struct {
	ID                string    `json:"id"`
	CallbackURL       string    `json:"callbackUrl"`
	Types             []string  `json:"types,omitempty"`
	Severity          []string  `json:"severity,omitempty"`
	Description       string    `json:"description,omitempty"`
	SecretFingerprint string    `json:"secretFingerprint,omitempty"`
	CreatedBy         string    `json:"createdBy,omitempty"`
	CreatedByTenantID string    `json:"createdByTenantId,omitempty"`
	TenantFilter      string    `json:"tenantFilter"`
	Generation        int64     `json:"generation"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
	Active            bool      `json:"active"`
}

// Reveal is the Create/RotateSecret response: the redacted subscription
// plus the plaintext secret returned exactly once with the single-use
// warning. spec: §25.5 lines 2702-2710.
type Reveal struct {
	Subscription
	Secret                string `json:"secret"`
	SecretRotationWarning string `json:"secretRotationWarning"`
}

// CreateRequest carries the caller-supplied fields on Create. The secret
// is generated server-side, so the request never carries one. spec:
// §25.5 lines 2702-2704.
type CreateRequest struct {
	CallbackURL  string   `json:"callbackUrl"`
	Types        []string `json:"types,omitempty"`
	Severity     []string `json:"severity,omitempty"`
	Description  string   `json:"description,omitempty"`
	TenantFilter string   `json:"tenantFilter,omitempty"`
}

// UpdateRequest patches a subscription's filters and metadata. Every
// field is a pointer so an omitted field leaves the stored value
// unchanged. The secret is never updated here (use RotateSecret).
// spec: §25.5 line 2568, lines 2613-2647.
type UpdateRequest struct {
	CallbackURL  *string   `json:"callbackUrl,omitempty"`
	Types        *[]string `json:"types,omitempty"`
	Severity     *[]string `json:"severity,omitempty"`
	Description  *string   `json:"description,omitempty"`
	TenantFilter *string   `json:"tenantFilter,omitempty"`
	Active       *bool     `json:"active,omitempty"`
}

// Caller is the authenticated principal performing a subscription
// operation: the OIDC sub, the tenant the principal acts for, and
// whether the principal holds the platform-admin role. The opsserver
// resolves it from the verified bearer. spec: §25.5 lines 2758-2766.
type Caller struct {
	Subject       string
	TenantID      string
	PlatformAdmin bool
}

// Delivery is one §25.5 webhook delivery-tracking row. spec: §25.5 lines
// 2649-2664.
type Delivery struct {
	ID             int64
	SubscriptionID string
	EventID        string
	EventType      string
	Status         string
	Attempts       int
	LastAttemptAt  time.Time
	LastError      string
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// Delivery status values. spec: §25.5 line 2655.
const (
	DeliveryDelivered = "delivered"
	DeliveryFailed    = "failed"
	DeliveryPending   = "pending"
)

// Store persists §25.5 webhook subscriptions and their delivery rows.
// The production Postgres backend lives in
// pkg/ops/eventsubscription/pgstore; the in-memory MemoryStore in this
// package backs unit tests and the v1 developer-mode lenny-ops binary.
type Store interface {
	Create(ctx context.Context, rec Record) error
	Get(ctx context.Context, id string) (Record, error)
	List(ctx context.Context) ([]Record, error)
	Update(ctx context.Context, id string, mutate func(*Record) error) (Record, error)
	Delete(ctx context.Context, id string) (Record, error)
	RecordDelivery(ctx context.Context, d Delivery) (Delivery, error)
	ListDeliveries(ctx context.Context, subID string, limit int) ([]Delivery, error)
	// DeleteExpired removes delivery-tracking rows whose expires_at is at
	// or before before, deleting at most limit rows so a single sweep does
	// not hold a long lock, and returns the number deleted. The §25.5
	// retention cron calls it in a loop until a sweep deletes fewer than
	// limit. spec: §25.5 lines 2649-2664.
	DeleteExpired(ctx context.Context, before time.Time, limit int) (int, error)
}

// Service composes the Store with the §25.5 validation, secret
// lifecycle, tenant-isolation, and audit surfaces so the opsserver
// handler does not reach the Store with malformed or unauthorized
// payloads.
type Service struct {
	Store Store
	// SSRF validates the callback URL at create and update time. A nil
	// validator applies the default (HTTPS-only, no allowlist) policy.
	SSRF *SSRFValidator
	// IDFunc generates the subscription id on Create. A nil IDFunc uses a
	// time-prefixed id; tests inject a pin so assertions can name the id.
	IDFunc func() string
	// SecretFunc generates the plaintext secret. A nil SecretFunc uses
	// the CSPRNG GenerateSecret; tests inject a pin.
	SecretFunc func() (string, error)
	// Now returns the timestamp source. A nil Now uses time.Now.
	Now func() time.Time
	// audit receives the §25.5 line 2731/2804 subscription audit events.
	audit AuditSink
	// OnChange, when non-nil, is invoked after every successful CRUD
	// mutation (Create, Update, RotateSecret, Delete) with the affected
	// subscription id. The lenny-ops wiring uses it for the §25.5 line
	// 2751 synchronous in-process cache update plus the
	// subscription_cache_invalidate RPC across replicas, so a change made
	// on one replica reaches the leader's delivery worker within a few
	// hundred milliseconds rather than at the next periodic refresh.
	OnChange func(ctx context.Context, subID string)
	// OnSecret, when non-nil, is invoked on Create and RotateSecret with
	// the freshly generated plaintext secret so the delivery worker's
	// in-memory reveal cache can sign deliveries. The plaintext is held
	// only in memory and never persisted; the store keeps the SHA-256
	// hash. spec: §25.5 lines 2715-2733, 2747-2756.
	OnSecret func(subID, secret string, generation int64)
	// OnRemove, when non-nil, is invoked on Delete with the removed
	// subscription id so the reveal cache can drop its plaintext secret.
	OnRemove func(subID string)
}

// NewService builds a Service against the supplied Store with the
// documented defaults.
func NewService(store Store) *Service {
	return &Service{Store: store, audit: noopAuditSink{}}
}

// SetAuditSink installs the §25.5 subscription audit-event sink. A nil
// sink restores the no-op default. spec: §25.5 lines 2731, 2804-2806.
func (s *Service) SetAuditSink(a AuditSink) {
	if a == nil {
		a = noopAuditSink{}
	}
	s.audit = a
}

func (s *Service) idFunc() func() string {
	if s.IDFunc != nil {
		return s.IDFunc
	}
	return defaultIDFunc
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) genSecret() (string, error) {
	if s.SecretFunc != nil {
		return s.SecretFunc()
	}
	return GenerateSecret()
}

func (s *Service) auditSink() AuditSink {
	if s.audit == nil {
		return noopAuditSink{}
	}
	return s.audit
}

// Create validates the request, applies the tenant-isolation gate,
// generates the secret server-side, persists the hash + fingerprint,
// emits the subscription_created audit event, and returns the plaintext
// secret exactly once. spec: §25.5 lines 2702-2733, 2758-2766.
func (s *Service) Create(ctx context.Context, req CreateRequest, caller Caller) (Reveal, error) {
	if s == nil || s.Store == nil {
		return Reveal{}, storeUnavailable()
	}
	if err := s.validateURL(ctx, req.CallbackURL); err != nil {
		return Reveal{}, err
	}
	types, err := normalizeTypes(req.Types)
	if err != nil {
		return Reveal{}, err
	}
	severity, err := normalizeSeverity(req.Severity)
	if err != nil {
		return Reveal{}, err
	}
	tenantFilter, createdByTenant, err := resolveTenantFilter(req.TenantFilter, caller)
	if err != nil {
		return Reveal{}, err
	}

	secret, err := s.genSecret()
	if err != nil {
		return Reveal{}, &Error{Code: ErrCodeNoDurableStore, Message: "secret generation failed: " + err.Error()}
	}
	now := s.now()
	rec := Record{
		ID:                s.idFunc()(),
		CallbackURL:       req.CallbackURL,
		Types:             types,
		Severity:          severity,
		Description:       req.Description,
		SecretHash:        HashSecret(secret),
		SecretFingerprint: Fingerprint(secret),
		CreatedBy:         caller.Subject,
		CreatedByTenantID: createdByTenant,
		TenantFilter:      tenantFilter,
		Generation:        0,
		CreatedAt:         now,
		UpdatedAt:         now,
		Active:            true,
	}
	if err := s.Store.Create(ctx, rec); err != nil {
		return Reveal{}, err
	}
	s.auditSink().Emit(AuditEvent{
		Type:  EventSubscriptionCreated,
		Actor: caller.Subject,
		Details: map[string]any{
			"subscriptionId":    rec.ID,
			"callbackUrl":       rec.CallbackURL,
			"secretFingerprint": rec.SecretFingerprint,
			"tenantFilter":      rec.TenantFilter,
		},
	})
	s.fireSecret(rec.ID, secret, rec.Generation)
	s.fireChange(ctx, rec.ID)
	return Reveal{Subscription: rec.View(), Secret: secret, SecretRotationWarning: secretRotationWarning}, nil
}

// Get reads one subscription by id, redacted.
func (s *Service) Get(ctx context.Context, id string) (Subscription, error) {
	if s == nil || s.Store == nil {
		return Subscription{}, storeUnavailable()
	}
	if id == "" {
		return Subscription{}, &Error{Code: ErrCodeNotFound, Message: "id is required"}
	}
	rec, err := s.Store.Get(ctx, id)
	if err != nil {
		return Subscription{}, err
	}
	return rec.View(), nil
}

// List returns every subscription, redacted, sorted by id.
func (s *Service) List(ctx context.Context) ([]Subscription, error) {
	if s == nil || s.Store == nil {
		return nil, storeUnavailable()
	}
	recs, err := s.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Subscription, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.View())
	}
	return out, nil
}

// Update patches a subscription's filters and metadata, bumps the
// generation counter so the §25.5 cache invalidates, and emits the
// subscription_updated audit event. spec: §25.5 lines 2568, 2751.
func (s *Service) Update(ctx context.Context, id string, req UpdateRequest, caller Caller) (Subscription, error) {
	if s == nil || s.Store == nil {
		return Subscription{}, storeUnavailable()
	}
	if id == "" {
		return Subscription{}, &Error{Code: ErrCodeNotFound, Message: "id is required"}
	}
	if req.CallbackURL != nil {
		if err := s.validateURL(ctx, *req.CallbackURL); err != nil {
			return Subscription{}, err
		}
	}
	var types []string
	if req.Types != nil {
		t, err := normalizeTypes(*req.Types)
		if err != nil {
			return Subscription{}, err
		}
		types = t
	}
	var severity []string
	if req.Severity != nil {
		sev, err := normalizeSeverity(*req.Severity)
		if err != nil {
			return Subscription{}, err
		}
		severity = sev
	}
	now := s.now()
	rec, err := s.Store.Update(ctx, id, func(r *Record) error {
		if req.CallbackURL != nil {
			r.CallbackURL = *req.CallbackURL
		}
		if req.Types != nil {
			r.Types = types
		}
		if req.Severity != nil {
			r.Severity = severity
		}
		if req.Description != nil {
			r.Description = *req.Description
		}
		if req.TenantFilter != nil {
			tf, createdByTenant, terr := resolveTenantFilter(*req.TenantFilter, caller)
			if terr != nil {
				return terr
			}
			r.TenantFilter = tf
			r.CreatedByTenantID = createdByTenant
		}
		if req.Active != nil {
			r.Active = *req.Active
		}
		r.Generation++
		r.UpdatedAt = now
		return nil
	})
	if err != nil {
		return Subscription{}, err
	}
	s.auditSink().Emit(AuditEvent{
		Type:  EventSubscriptionUpdated,
		Actor: caller.Subject,
		Details: map[string]any{
			"subscriptionId": rec.ID,
			"generation":     rec.Generation,
		},
	})
	s.fireChange(ctx, rec.ID)
	return rec.View(), nil
}

// RotateSecret generates a new secret, updates the stored hash and
// fingerprint, records the previous fingerprint and rotation time for
// the dual-secret overlap window, bumps the generation, emits the
// subscription_secret_rotated audit event, and returns the new
// plaintext once. spec: §25.5 lines 2723-2733.
func (s *Service) RotateSecret(ctx context.Context, id string, caller Caller) (Reveal, error) {
	if s == nil || s.Store == nil {
		return Reveal{}, storeUnavailable()
	}
	if id == "" {
		return Reveal{}, &Error{Code: ErrCodeNotFound, Message: "id is required"}
	}
	secret, err := s.genSecret()
	if err != nil {
		return Reveal{}, &Error{Code: ErrCodeNoDurableStore, Message: "secret generation failed: " + err.Error()}
	}
	now := s.now()
	rec, err := s.Store.Update(ctx, id, func(r *Record) error {
		r.PreviousSecretFingerprint = r.SecretFingerprint
		r.SecretHash = HashSecret(secret)
		r.SecretFingerprint = Fingerprint(secret)
		r.SecretRotatedAt = now
		r.Generation++
		r.UpdatedAt = now
		return nil
	})
	if err != nil {
		return Reveal{}, err
	}
	s.auditSink().Emit(AuditEvent{
		Type:  EventSubscriptionSecretRotated,
		Actor: caller.Subject,
		Details: map[string]any{
			"subscriptionId":            rec.ID,
			"secretFingerprint":         rec.SecretFingerprint,
			"previousSecretFingerprint": rec.PreviousSecretFingerprint,
		},
	})
	s.fireSecret(rec.ID, secret, rec.Generation)
	s.fireChange(ctx, rec.ID)
	return Reveal{Subscription: rec.View(), Secret: secret, SecretRotationWarning: secretRotationWarning}, nil
}

// Delete removes a subscription and emits the subscription_deleted
// audit event. spec: §25.5 line 2806.
func (s *Service) Delete(ctx context.Context, id string, caller Caller) error {
	if s == nil || s.Store == nil {
		return storeUnavailable()
	}
	if id == "" {
		return &Error{Code: ErrCodeNotFound, Message: "id is required"}
	}
	rec, err := s.Store.Delete(ctx, id)
	if err != nil {
		return err
	}
	s.auditSink().Emit(AuditEvent{
		Type:  EventSubscriptionDeleted,
		Actor: caller.Subject,
		Details: map[string]any{
			"subscriptionId": rec.ID,
		},
	})
	if s.OnRemove != nil {
		s.OnRemove(rec.ID)
	}
	s.fireChange(ctx, rec.ID)
	return nil
}

// fireChange invokes the OnChange hook when one is registered. spec:
// §25.5 line 2751.
func (s *Service) fireChange(ctx context.Context, subID string) {
	if s.OnChange != nil {
		s.OnChange(ctx, subID)
	}
}

// fireSecret invokes the OnSecret hook when one is registered. spec:
// §25.5 lines 2747-2756.
func (s *Service) fireSecret(subID, secret string, generation int64) {
	if s.OnSecret != nil {
		s.OnSecret(subID, secret, generation)
	}
}

// ListDeliveries returns the recent delivery attempts for a
// subscription, newest-first, bounded by limit (default 100, max 1000).
// spec: §25.5 line 2569.
func (s *Service) ListDeliveries(ctx context.Context, id string, limit int) ([]Delivery, error) {
	if s == nil || s.Store == nil {
		return nil, storeUnavailable()
	}
	if id == "" {
		return nil, &Error{Code: ErrCodeNotFound, Message: "id is required"}
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	// Confirm the subscription exists so a missing id is a 404 rather
	// than an empty list.
	if _, err := s.Store.Get(ctx, id); err != nil {
		return nil, err
	}
	return s.Store.ListDeliveries(ctx, id, limit)
}

func (s *Service) validateURL(ctx context.Context, raw string) error {
	v := s.SSRF
	if v == nil {
		v = defaultSSRFValidator
	}
	return v.Validate(ctx, raw)
}

func storeUnavailable() *Error {
	return &Error{Code: ErrCodeNoDurableStore, Message: "subscription store unavailable"}
}

// normalizeTypes trims and sorts the filter types and rejects empty
// entries with INVALID_EVENT_FILTER. spec: §25.5 line 2795.
func normalizeTypes(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			return nil, &Error{Code: ErrCodeInvalidFilter, Message: "types entries must be non-empty"}
		}
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

// validSeverities is the §16.6 catalog severity set the filter accepts.
var validSeverities = map[string]bool{"info": true, "warning": true, "critical": true}

// normalizeSeverity trims, lower-cases, and sorts the severity filter,
// rejecting any value outside the §16.6 catalog set with
// INVALID_EVENT_FILTER. spec: §25.5 lines 2693, 2795.
func normalizeSeverity(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, sev := range in {
		sev = strings.ToLower(strings.TrimSpace(sev))
		if sev == "" {
			continue
		}
		if !validSeverities[sev] {
			return nil, &Error{Code: ErrCodeInvalidFilter, Message: fmt.Sprintf("unrecognized severity %q", sev)}
		}
		out = append(out, sev)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// resolveTenantFilter applies the §25.5 tenant-isolation rule. A
// platform-admin may register any filter and defaults to the wildcard
// with a NULL created_by_tenant_id. A tenant-admin may only register
// its own tenant (an empty request defaults to it); a wildcard or a
// different tenant returns SUBSCRIPTION_TENANT_FORBIDDEN. The returned
// createdByTenant is empty for platform-admin scope. spec: §25.5 lines
// 2758-2766.
func resolveTenantFilter(requested string, caller Caller) (filter, createdByTenant string, err error) {
	requested = strings.TrimSpace(requested)
	if caller.PlatformAdmin {
		if requested == "" {
			requested = TenantFilterAll
		}
		return requested, "", nil
	}
	// tenant-admin
	if caller.TenantID == "" {
		return "", "", &Error{Code: ErrCodeTenantForbidden, Message: "tenant-admin caller has no tenant claim"}
	}
	if requested == "" {
		requested = caller.TenantID
	}
	if requested == TenantFilterAll || requested != caller.TenantID {
		return "", "", &Error{
			Code:    ErrCodeTenantForbidden,
			Message: "tenant-admin may only subscribe to its own tenant",
		}
	}
	return caller.TenantID, caller.TenantID, nil
}

// MemoryStore is an in-memory Store backing the v1 lenny-ops binary when
// no Postgres connection is configured, and the unit tests. It is
// safe for concurrent use.
type MemoryStore struct {
	mu         sync.Mutex
	rows       map[string]Record
	deliveries []Delivery
	nextDelID  int64
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: map[string]Record{}}
}

// Create persists the record.
func (m *MemoryStore) Create(_ context.Context, rec Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[rec.ID] = rec
	return nil
}

// Get returns the record or a typed NotFound error.
func (m *MemoryStore) Get(_ context.Context, id string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.rows[id]
	if !ok {
		return Record{}, notFound(id)
	}
	return rec, nil
}

// List returns every record sorted by id for stable output.
func (m *MemoryStore) List(_ context.Context) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Record, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Update applies mutate to the stored record under the lock and persists
// the result. A missing id returns NotFound; a mutate error aborts the
// write and is returned unchanged.
func (m *MemoryStore) Update(_ context.Context, id string, mutate func(*Record) error) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.rows[id]
	if !ok {
		return Record{}, notFound(id)
	}
	if err := mutate(&rec); err != nil {
		return Record{}, err
	}
	m.rows[id] = rec
	return rec, nil
}

// Delete removes the record and returns it for the audit trail.
func (m *MemoryStore) Delete(_ context.Context, id string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.rows[id]
	if !ok {
		return Record{}, notFound(id)
	}
	delete(m.rows, id)
	return rec, nil
}

// RecordDelivery appends a delivery row, allocating its id.
func (m *MemoryStore) RecordDelivery(_ context.Context, d Delivery) (Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextDelID++
	d.ID = m.nextDelID
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	m.deliveries = append(m.deliveries, d)
	return d, nil
}

// ListDeliveries returns up to limit recent deliveries for subID,
// newest-first.
func (m *MemoryStore) ListDeliveries(_ context.Context, subID string, limit int) ([]Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Delivery
	for i := len(m.deliveries) - 1; i >= 0; i-- {
		if m.deliveries[i].SubscriptionID != subID {
			continue
		}
		out = append(out, m.deliveries[i])
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// DeleteExpired removes delivery rows whose ExpiresAt is at or before
// before, deleting at most limit rows, and returns the count removed.
func (m *MemoryStore) DeleteExpired(_ context.Context, before time.Time, limit int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = len(m.deliveries)
	}
	kept := m.deliveries[:0]
	deleted := 0
	for _, d := range m.deliveries {
		if deleted < limit && !d.ExpiresAt.IsZero() && !d.ExpiresAt.After(before) {
			deleted++
			continue
		}
		kept = append(kept, d)
	}
	m.deliveries = kept
	return deleted, nil
}

func notFound(id string) error {
	return &Error{Code: ErrCodeNotFound, Message: fmt.Sprintf("subscription %q not found", id)}
}

// defaultIDFunc allocates a subscription id when the Service was
// constructed without an injected IDFunc. The Postgres backend uses the
// same format so ids are comparable across stores.
func defaultIDFunc() string {
	return fmt.Sprintf("sub_%d", time.Now().UnixNano())
}

var _ Store = (*MemoryStore)(nil)
