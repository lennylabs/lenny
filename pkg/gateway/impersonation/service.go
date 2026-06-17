// SPDX-License-Identifier: MIT

package impersonation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	auditcatalog "github.com/lennylabs/lenny/pkg/observability/audit"
)

// AuditAppender is the §11.7 audit hash-chain surface the impersonation
// flow writes its started/ended events to. The Postgres-backed
// pkg/gateway/auditstore.Store satisfies it and applies the §11.7 CMP-058
// platform-tenant residency routing on every write that carries
// target_tenant_id; the in-memory ChainSet adapter satisfies it for the
// minimal gateway.
type AuditAppender interface {
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error)
}

// Signer mints the target-user bearer once the started audit commits.
// pkg/auth/jwt.Signer (the KMS-backed / breaker-wrapped gateway signer)
// satisfies it.
type Signer interface {
	Sign(c jwt.Claims) (string, error)
}

// Config configures a Service.
type Config struct {
	// PlatformTenantID is the §11.7 platform tenant both audit events are
	// written under (defaults to "platform"). The CMP-058 residency gate
	// keys on this id, so it MUST match the auditstore's platform tenant.
	PlatformTenantID string
	// MaxDuration caps the requested impersonation_duration_seconds. A
	// request above it is rejected with ErrInvalidDuration. Zero defaults
	// to one hour.
	MaxDuration time.Duration
	// Issuer / Audience are stamped on the minted bearer.
	Issuer   string
	Audience []string
	// Clock supplies the issue/end instants; nil defaults to time.Now UTC.
	Clock func() time.Time
	// NewID generates the impersonation session id (also the minted
	// bearer's jti). Required.
	NewID func() string
}

// Service is the §13.3 platform-admin impersonation issuer.
type Service struct {
	store    Store
	appender AuditAppender
	signer   Signer
	cfg      Config
}

const (
	defaultMaxDuration    = time.Hour
	defaultPlatformTenant = "platform"
)

// New returns a Service. It panics on a missing collaborator, a wiring
// error rather than a runtime condition.
func New(store Store, appender AuditAppender, signer Signer, cfg Config) *Service {
	if store == nil || appender == nil || signer == nil {
		panic("impersonation: store, appender, and signer are required")
	}
	if cfg.NewID == nil {
		panic("impersonation: cfg.NewID is required")
	}
	if cfg.PlatformTenantID == "" {
		cfg.PlatformTenantID = defaultPlatformTenant
	}
	if cfg.MaxDuration <= 0 {
		cfg.MaxDuration = defaultMaxDuration
	}
	if cfg.Clock == nil {
		cfg.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: store, appender: appender, signer: signer, cfg: cfg}
}

// IssueRequest is one impersonation-session request. The handler resolves
// the target user (existence + roles) and supplies them; the Service owns
// the audit-before-side-effect ordering, the mint, and the ticket record.
type IssueRequest struct {
	AdminSub       string
	TargetTenantID string
	TargetUserID   string
	Reason         string
	// TicketRef is the operator's external justification reference,
	// recorded as the §16.7 ticket_id field.
	TicketRef string
	Duration  time.Duration
	// TargetRoles are the impersonated user's RBAC roles, stamped on the
	// minted bearer so the impersonation reflects the user's authority.
	TargetRoles []auth.Role
}

// Issue establishes an impersonation session. It writes
// admin.impersonation_started FIRST and mints the bearer only after the
// audit row commits (§16.7 line 680: audit must be durable before any
// externally observable side effect). When the audit write fails closed
// because the target tenant's residency region is unresolvable (the
// §11.7 CMP-058 gate), the error is returned and NO session is
// established and NO bearer is minted.
//
// spec: §13.3 line 585; §16.7 line 680; §11.7 lines 430-433.
func (s *Service) Issue(ctx context.Context, req IssueRequest) (Ticket, string, error) {
	if req.AdminSub == "" || req.TargetTenantID == "" || req.TargetUserID == "" {
		return Ticket{}, "", ErrMissingField
	}
	if req.Duration <= 0 || req.Duration > s.cfg.MaxDuration {
		return Ticket{}, "", fmt.Errorf("%w: %s (max %s)", ErrInvalidDuration, req.Duration, s.cfg.MaxDuration)
	}
	now := s.cfg.Clock().UTC()
	t := Ticket{
		ID:             s.cfg.NewID(),
		AdminSub:       req.AdminSub,
		AdminTenantID:  s.cfg.PlatformTenantID,
		TargetTenantID: req.TargetTenantID,
		TargetUserID:   req.TargetUserID,
		Reason:         req.Reason,
		TicketRef:      req.TicketRef,
		Duration:       req.Duration,
		IssuedAt:       now,
		ExpiresAt:      now.Add(req.Duration),
	}

	// 1. Durable audit BEFORE the side effect. A CMP-058 fail-closed
	//    (unresolvable target region) propagates and halts issuance.
	payload, err := startedPayload(t)
	if err != nil {
		return Ticket{}, "", err
	}
	if _, err := s.appender.Append(ctx, s.cfg.PlatformTenantID, string(auditcatalog.EventAdminImpersonationStarted), payload, now); err != nil {
		return Ticket{}, "", err
	}

	// 2. Mint the target-user bearer now that the start is durable.
	signed, err := s.signer.Sign(jwt.Claims{
		Issuer:     s.cfg.Issuer,
		Subject:    t.TargetUserID,
		Audience:   s.cfg.Audience,
		Expiry:     t.ExpiresAt.Unix(),
		NotBefore:  now.Unix(),
		IssuedAt:   now.Unix(),
		JWTID:      t.ID,
		TenantID:   t.TargetTenantID,
		CallerType: "human",
		Roles:      req.TargetRoles,
		Typ:        auth.TokenUserBearer,
	})
	if err != nil {
		return Ticket{}, "", fmt.Errorf("impersonation: mint bearer: %w", err)
	}

	// 3. Record the session for the listing and the expiry sweep.
	if err := s.store.Put(ctx, t); err != nil {
		return Ticket{}, "", fmt.Errorf("impersonation: record session: %w", err)
	}
	return t, signed, nil
}

// End terminates a session explicitly (DELETE /v1/admin/impersonation/{id}).
// It writes admin.impersonation_ended and stamps the terminal fields. A
// CMP-058 fail-closed on the ended event is returned to the caller so the
// operator retries; the session stays open until the ended event commits
// or the sweep expires it.
func (s *Service) End(ctx context.Context, id, endedBy string) (Ticket, error) {
	t, err := s.store.Get(ctx, id)
	if err != nil {
		return Ticket{}, err
	}
	if !t.Active() {
		return Ticket{}, ErrAlreadyEnded
	}
	return s.end(ctx, t, endedBy, EndReasonExplicit)
}

// SweepExpired emits admin.impersonation_ended (reason=expired) for every
// session whose minted bearer has expired and removes it from the active
// set. A failed ended-event write leaves the session due for the next
// sweep so the terminal record is retried; the minted bearer has already
// expired, so no access persists past ExpiresAt. It returns the count of
// sessions ended this pass.
func (s *Service) SweepExpired(ctx context.Context, now time.Time) (int, error) {
	due, err := s.store.DueForExpiry(ctx, now.UTC())
	if err != nil {
		return 0, err
	}
	ended := 0
	for _, t := range due {
		if _, eerr := s.end(ctx, t, "system", EndReasonExpired); eerr != nil {
			// Leave the session for the next sweep; the bearer has already
			// expired so this is a record-keeping retry, not an access risk.
			continue
		}
		ended++
	}
	return ended, nil
}

// end is the shared termination path: write the ended audit row, then
// stamp the terminal fields. The audit write precedes the state mutation
// so a CMP-058 fail-closed leaves the session active for retry.
func (s *Service) end(ctx context.Context, t Ticket, endedBy string, reason EndReason) (Ticket, error) {
	now := s.cfg.Clock().UTC()
	payload, err := endedPayload(t, now, endedBy, reason)
	if err != nil {
		return Ticket{}, err
	}
	if _, err := s.appender.Append(ctx, s.cfg.PlatformTenantID, string(auditcatalog.EventAdminImpersonationEnded), payload, now); err != nil {
		return Ticket{}, err
	}
	return s.store.MarkEnded(ctx, t.ID, now, endedBy, reason)
}

// ListActive returns the not-yet-ended sessions for
// GET /v1/admin/impersonation.
func (s *Service) ListActive(ctx context.Context) ([]Ticket, error) {
	return s.store.ListActive(ctx)
}

// startedPayload renders the §16.7 line 680 admin.impersonation_started
// canonical payload. target_tenant_id is a top-level field so the §11.7
// CMP-058 residency gate routes the write to the target's regional
// platform-Postgres.
func startedPayload(t Ticket) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"admin_sub":                      t.AdminSub,
		"admin_tenant_id":                t.AdminTenantID,
		"target_tenant_id":               t.TargetTenantID,
		"target_user_id":                 t.TargetUserID,
		"impersonation_reason":           t.Reason,
		"impersonation_duration_seconds": int64(t.Duration / time.Second),
		"ticket_id":                      t.TicketRef,
		"impersonation_session_id":       t.ID,
	})
}

// endedPayload renders the admin.impersonation_ended canonical payload.
// It mirrors the started event's identifying fields (so a SIEM can join
// the pair) and records who ended it and why.
func endedPayload(t Ticket, endedAt time.Time, endedBy string, reason EndReason) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"admin_sub":                t.AdminSub,
		"admin_tenant_id":          t.AdminTenantID,
		"target_tenant_id":         t.TargetTenantID,
		"target_user_id":           t.TargetUserID,
		"ticket_id":                t.TicketRef,
		"impersonation_session_id": t.ID,
		"ended_by":                 endedBy,
		"end_reason":               string(reason),
		"started_at":               t.IssuedAt.UTC().Format(time.RFC3339Nano),
		"ended_at":                 endedAt.UTC().Format(time.RFC3339Nano),
	})
}
