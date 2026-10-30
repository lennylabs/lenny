// SPDX-License-Identifier: MIT

package impersonation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	auditcatalog "github.com/lennylabs/lenny/pkg/observability/audit"
)

// AuditAppender is the §11.7 audit hash-chain surface the impersonation
// flow writes its started and ended events to. The gateway passes the
// write-time tenant-scope validator over the Postgres-backed auditstore,
// which applies the §11.7 CMP-058 platform-tenant residency routing on
// every write that carries target_tenant_id.
type AuditAppender interface {
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error)
}

// Signer mints the target-user bearer once the started audit commits.
// pkg/auth/jwt.BreakerSigner, the gateway's breaker-wrapped remote signer
// over the §10.2 platform:jwt-signing key, satisfies it.
type Signer interface {
	Sign(ctx context.Context, c jwt.Claims) (string, error)
}

// IssuedTokens is the slice of the issued-token index the service drives.
// *issuedtokenstore.Store satisfies it.
//
// spec: §13.3 (Platform-admin impersonation, rules 1, 2, and 3); §12.2.
type IssuedTokens interface {
	Get(ctx context.Context, tenantID, jti string) (issuedtokenstore.IssuedToken, error)
	RecordImpersonation(ctx context.Context, tok issuedtokenstore.IssuedToken, callerTenantID string) error
	EndImpersonation(ctx context.Context, jti string, at time.Time) (issuedtokenstore.IssuedToken, error)
	ListActiveImpersonations(ctx context.Context, now time.Time) ([]issuedtokenstore.IssuedToken, error)
}

// RevocationCache pushes a revoked jti into this replica's revocation
// cache and publishes it to peer replicas. The gateway's revocation
// propagator satisfies it.
//
// spec: §13.3 (Platform-admin impersonation, rule 3); §11.4 step 5.
type RevocationCache interface {
	Revoke(jti string)
}

// CrossTenantReads emits the §12.3 cross_tenant_read audit event for a
// read under the __all__ sentinel. *auditstore.Store satisfies it.
//
// spec: §13.3 (Platform-admin impersonation, rule 7); §12.3.
type CrossTenantReads interface {
	EmitCrossTenantRead(ctx context.Context, r auditstore.CrossTenantRead) error
}

// Config configures a Service.
type Config struct {
	// PlatformTenantID is the §11.7 platform tenant both audit events are
	// written under (defaults to "platform"). The CMP-058 residency gate
	// keys on this id, so it MUST match the auditstore's platform tenant.
	PlatformTenantID string
	// MaxDuration caps the requested impersonation_duration_seconds. New
	// clamps it to the §15.1 maximum of one hour, and zero defaults to
	// that maximum.
	MaxDuration time.Duration
	// Issuer and Audience are stamped on the minted bearer.
	Issuer   string
	Audience []string
	// Clock supplies the issue and end instants; nil defaults to time.Now
	// in UTC.
	Clock func() time.Time
	// NewID generates the impersonation session id, which is also the
	// minted bearer's jti. Required.
	NewID func() string
}

// Service is the §13.3 platform-admin impersonation issuer.
type Service struct {
	tokens   IssuedTokens
	cache    RevocationCache
	reads    CrossTenantReads
	appender AuditAppender
	signer   Signer
	cfg      Config
}

// Duration bounds and fixed audit values.
const (
	// minDuration and maxDuration are the §15.1 durationSeconds range.
	minDuration           = time.Minute
	maxDuration           = time.Hour
	defaultPlatformTenant = "platform"
	// crossTenantCategory is the §13.3 rule 7 query category.
	crossTenantCategory = "impersonation_session"
	endEndpoint         = "DELETE /v1/admin/impersonation/{id}"
	listEndpoint        = "GET /v1/admin/impersonation"
)

// New returns a Service. It panics on a missing collaborator, a wiring
// error rather than a runtime condition. MaxDuration is clamped to the
// §15.1 maximum so no configuration admits a longer bearer.
//
// spec: §15.1 (POST /v1/admin/impersonation); §13.3.
func New(tokens IssuedTokens, cache RevocationCache, reads CrossTenantReads, appender AuditAppender, signer Signer, cfg Config) *Service {
	if tokens == nil || cache == nil || reads == nil || appender == nil || signer == nil {
		panic("impersonation: issued tokens, revocation cache, cross-tenant reads, appender, and signer are required")
	}
	if cfg.NewID == nil {
		panic("impersonation: cfg.NewID is required")
	}
	if cfg.PlatformTenantID == "" {
		cfg.PlatformTenantID = defaultPlatformTenant
	}
	if cfg.MaxDuration <= 0 || cfg.MaxDuration > maxDuration {
		cfg.MaxDuration = maxDuration
	}
	if cfg.Clock == nil {
		cfg.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &Service{tokens: tokens, cache: cache, reads: reads, appender: appender, signer: signer, cfg: cfg}
}

// IssueRequest is one impersonation-session request. The handler resolves
// the target user (existence and roles) and the caller's bearer identity
// and supplies them; the Service owns the caller check, the
// audit-before-side-effect ordering, the mint, and the record.
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
	// CallerTenantID and CallerJTI identify the caller's own bearer. The
	// caller check reads its issued_tokens row, and the record fence reads
	// the caller's user row under CallerTenantID.
	CallerTenantID string
	CallerJTI      string
}

// Issue establishes an impersonation session in the §13.3 rule 1 order.
// It first refuses a caller whose own bearer step (1a) of "Write-before-issue
// ordering" would reject as a subject_token, writing nothing. It then
// writes admin.impersonation_started, signs the bearer, and records it in
// issued_tokens under the user-row fence, and returns the bearer only once
// the record commits. A CMP-058 fail-closed on the started event returns
// the audit error with no bearer signed. The mint writes no
// token.exchanged row.
//
// spec: §13.3 (Platform-admin impersonation, rule 1); §16.7; §11.7.
func (s *Service) Issue(ctx context.Context, req IssueRequest) (Ticket, string, error) {
	if err := s.validate(req); err != nil {
		return Ticket{}, "", err
	}
	if err := s.checkCaller(ctx, req.CallerTenantID, req.CallerJTI); err != nil {
		return Ticket{}, "", err
	}
	// The JWT iat and exp are whole Unix seconds, so the issue instant is
	// truncated before it fixes the started event time, the claims, and the
	// row's issued_at and exp: the row then records the bearer's exp exactly.
	// spec: §13.3 (Platform-admin impersonation, rule 1 (c); `exp` granularity).
	now := s.cfg.Clock().UTC().Truncate(time.Second)
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
	if err := s.appendStarted(ctx, t); err != nil {
		return Ticket{}, "", err
	}
	signed, err := s.sign(ctx, t, req.TargetRoles)
	if err != nil {
		return Ticket{}, "", err
	}
	if err := s.record(ctx, t, signed, req.CallerTenantID); err != nil {
		return Ticket{}, "", err
	}
	return t, signed, nil
}

// validate checks the required fields and the duration range.
func (s *Service) validate(req IssueRequest) error {
	if req.AdminSub == "" || req.TargetTenantID == "" || req.TargetUserID == "" || req.CallerTenantID == "" {
		return ErrMissingField
	}
	if req.Duration < minDuration || req.Duration > s.cfg.MaxDuration {
		return fmt.Errorf("%w: %s (min %s, max %s)", ErrInvalidDuration, req.Duration, minDuration, s.cfg.MaxDuration)
	}
	return nil
}

// checkCaller reads the caller's bearer row in the caller's tenant and
// refuses the call when step (1a) would reject it as a subject_token. A
// bearer with no row, or a caller without a jti, passes.
//
// spec: §13.3 (Platform-admin impersonation, rule 1; Write-before-issue
// ordering, step (1a)).
func (s *Service) checkCaller(ctx context.Context, tenantID, jti string) error {
	if jti == "" {
		return nil
	}
	row, err := s.tokens.Get(ctx, tenantID, jti)
	if errors.Is(err, issuedtokenstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("impersonation: read caller bearer: %w", err)
	}
	if refusal := row.SourceRefusal(issuedtokenstore.SourceSubjectToken); refusal != nil {
		return fmt.Errorf("%w: %w", ErrIssuanceRefused, refusal)
	}
	return nil
}

// appendStarted writes admin.impersonation_started under the platform
// tenant.
func (s *Service) appendStarted(ctx context.Context, t Ticket) error {
	payload, err := startedPayload(t)
	if err != nil {
		return err
	}
	_, err = s.appender.Append(ctx, s.cfg.PlatformTenantID, string(auditcatalog.EventAdminImpersonationStarted), payload, t.IssuedAt)
	return err
}

// sign mints the target-user bearer whose jti is the session id and whose
// exp is the started event's time plus the requested duration.
func (s *Service) sign(ctx context.Context, t Ticket, roles []auth.Role) (string, error) {
	signed, err := s.signer.Sign(ctx, jwt.Claims{
		Issuer:     s.cfg.Issuer,
		Subject:    t.TargetUserID,
		Audience:   s.cfg.Audience,
		Expiry:     t.ExpiresAt.Unix(),
		NotBefore:  t.IssuedAt.Unix(),
		IssuedAt:   t.IssuedAt.Unix(),
		JWTID:      t.ID,
		TenantID:   t.TargetTenantID,
		CallerType: "human",
		Roles:      roles,
		Typ:        auth.TokenUserBearer,
	})
	if err != nil {
		return "", fmt.Errorf("impersonation: mint bearer: %w", err)
	}
	return signed, nil
}

// record writes the bearer's impersonation row under the §13.3 rule 1 (c)
// user-row fence. An invalidated caller or target refuses the issuance.
func (s *Service) record(ctx context.Context, t Ticket, signed, callerTenantID string) error {
	err := s.tokens.RecordImpersonation(ctx, impersonationRow(t, signed, s.cfg.Audience), callerTenantID)
	if err == nil {
		return nil
	}
	var invalidated *issuedtokenstore.UserInvalidatedError
	if errors.As(err, &invalidated) {
		return fmt.Errorf("%w: %w", ErrIssuanceRefused, err)
	}
	return fmt.Errorf("impersonation: record session: %w", err)
}

// impersonationRow builds the issued_tokens row of a minted bearer: the
// target's tenant and sub, act_sub set to the admin, and no parent.
//
// spec: §13.3 (Platform-admin impersonation, rules 1 (c) and 2).
func impersonationRow(t Ticket, signed string, audience []string) issuedtokenstore.IssuedToken {
	sum := sha256.Sum256([]byte(signed))
	return issuedtokenstore.IssuedToken{
		JTI:        t.ID,
		TenantID:   t.TargetTenantID,
		Subject:    t.TargetUserID,
		TokenHash:  sum[:],
		Audience:   strings.Join(audience, " "),
		Audiences:  audience,
		IssuedAt:   t.IssuedAt,
		ExpiresAt:  t.ExpiresAt,
		ActSubject: t.AdminSub,
	}
}

// End terminates a session for DELETE /v1/admin/impersonation/{id}. It
// revokes the impersonation row in one conditional update, pushes the jti
// into the revocation cache, then appends admin.impersonation_ended and
// emits cross_tenant_read. The append and the emit are each attempted
// whatever the other's outcome, neither undoes the revocation, and End
// returns their joined errors with the EndResult. A miss returns
// ErrNotFound, or ErrAlreadyEnded wrapping the store's *NotActiveError,
// after emitting cross_tenant_read with a row count of zero.
//
// spec: §13.3 (Platform-admin impersonation, rules 3 and 7); §16.7; §12.3.
func (s *Service) End(ctx context.Context, id, endedBy string) (EndResult, error) {
	now := s.cfg.Clock().UTC().Truncate(time.Second)
	row, err := s.tokens.EndImpersonation(ctx, id, now)
	if err != nil {
		return EndResult{}, s.endMiss(ctx, err, endedBy)
	}
	s.cache.Revoke(row.JTI)
	appendErr := s.appendEnded(ctx, row, endedBy, now)
	emitErr := s.emitRead(ctx, endEndpoint, 1, endedBy)
	return EndResult{SessionID: row.JTI, EndedAt: now}, errors.Join(appendErr, emitErr)
}

// endMiss maps an EndImpersonation failure. A missing or inactive row
// still set __all__, so it emits cross_tenant_read with a row count of
// zero; any other store error is returned as is.
func (s *Service) endMiss(ctx context.Context, err error, endedBy string) error {
	var notActive *issuedtokenstore.NotActiveError
	switch {
	case errors.Is(err, issuedtokenstore.ErrNotFound):
		return errors.Join(ErrNotFound, s.emitRead(ctx, endEndpoint, 0, endedBy))
	case errors.As(err, &notActive):
		return errors.Join(fmt.Errorf("%w: %w", ErrAlreadyEnded, notActive), s.emitRead(ctx, endEndpoint, 0, endedBy))
	default:
		return fmt.Errorf("impersonation: end session: %w", err)
	}
}

// appendEnded writes admin.impersonation_ended with the §16.7 payload built
// from the revoked row and the caller.
func (s *Service) appendEnded(ctx context.Context, row issuedtokenstore.IssuedToken, endedBy string, endedAt time.Time) error {
	payload, err := endedPayload(row, s.cfg.PlatformTenantID, endedBy, endedAt)
	if err != nil {
		return err
	}
	if _, err := s.appender.Append(ctx, s.cfg.PlatformTenantID, string(auditcatalog.EventAdminImpersonationEnded), payload, endedAt); err != nil {
		return err
	}
	return nil
}

// emitRead emits one cross_tenant_read event of the impersonation_session
// category.
func (s *Service) emitRead(ctx context.Context, endpoint string, rowCount int, caller string) error {
	return s.reads.EmitCrossTenantRead(ctx, auditstore.CrossTenantRead{
		Category:      crossTenantCategory,
		RowCount:      rowCount,
		Endpoint:      endpoint,
		CallerSubject: caller,
	})
}

// ListActive returns the active impersonation sessions in every tenant for
// GET /v1/admin/impersonation. It emits cross_tenant_read before it reads
// any row and returns no list when the emit fails. The event's row count is
// zero, because it is written before the read.
//
// spec: §13.3 (Platform-admin impersonation, rules 2 and 7); §12.3.
func (s *Service) ListActive(ctx context.Context, callerSub string) ([]Session, error) {
	if err := s.emitRead(ctx, listEndpoint, 0, callerSub); err != nil {
		return nil, fmt.Errorf("impersonation: emit cross_tenant_read: %w", err)
	}
	rows, err := s.tokens.ListActiveImpersonations(ctx, s.cfg.Clock().UTC())
	if err != nil {
		return nil, fmt.Errorf("impersonation: list sessions: %w", err)
	}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		out = append(out, Session{
			ID:             r.JTI,
			AdminSub:       r.ActSubject,
			TargetTenantID: r.TenantID,
			TargetUserID:   r.Subject,
			IssuedAt:       r.IssuedAt,
			ExpiresAt:      r.ExpiresAt,
		})
	}
	return out, nil
}

// startedPayload renders the §16.7 admin.impersonation_started canonical
// payload. target_tenant_id is a top-level field so the §11.7 CMP-058
// residency gate routes the write to the target's regional
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

// endedPayload renders the §16.7 admin.impersonation_ended canonical
// payload from the revoked row and the caller. It carries the identifying
// fields a SIEM joins on impersonation_session_id; the ticket reference
// stays on the started event.
//
// spec: §16.7 (admin.impersonation_ended).
func endedPayload(row issuedtokenstore.IssuedToken, adminTenantID, endedBy string, endedAt time.Time) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"admin_sub":                row.ActSubject,
		"admin_tenant_id":          adminTenantID,
		"target_tenant_id":         row.TenantID,
		"target_user_id":           row.Subject,
		"impersonation_session_id": row.JTI,
		"ended_by":                 endedBy,
		"started_at":               row.IssuedAt.UTC().Format(time.RFC3339Nano),
		"ended_at":                 endedAt.UTC().Format(time.RFC3339Nano),
	})
}
