// SPDX-License-Identifier: MIT

// Package tokenservice implements the §13.3 POST /v1/oauth/token
// endpoint as an http.Handler. The handler decodes RFC 8693
// token-exchange requests, drives pkg/tokenexchange.Validate against
// the supplied subject (and optional actor), signs the issued token
// with the configured Signer, and returns the RFC 8693 response
// envelope.
//
// The Server signs with any jwt.Signer. §4 requires the production
// Token Service to sign with KMS-backed material: jwt.KMSSigner holds
// signing material whose durable form is KMS-envelope-encrypted, so
// no plaintext signing key is persisted. The dev HMAC signer
// (jwt.HMACSigner) satisfies the same interface for the no-cloud
// development path. The Server keeps an in-memory issued-tokens store
// and no Postgres advisory lock; a durable IssuedTokenStore makes the
// §13.3 write-before-issue record durable.
package tokenservice

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/tokenexchange"
)

// IssuedTokenStore records issued-token metadata. The §13.3
// write-before-issue rule means the Token Service commits a record
// here before it returns a minted token to the caller, so every live
// token can be matched against its revocation state.
type IssuedTokenStore interface {
	Record(ctx context.Context, tok issuedtokenstore.IssuedToken) error
}

// IssuedTokenAuditStore extends IssuedTokenStore with the §13.3
// write-before-issue combined transaction: the issued_tokens INSERT
// and the token.exchanged audit_log INSERT happen in one Postgres
// transaction under the §11.7 per-tenant advisory lock. When the
// configured IssuedTokens dependency satisfies this interface (the
// Postgres-backed issuedtokenstore.Store does), the Token Service
// drives the combined path; otherwise it falls back to the in-memory
// Auditor.
// spec: §13.3 line 589.
type IssuedTokenAuditStore interface {
	IssuedTokenStore
	RecordWithAudit(ctx context.Context, tok issuedtokenstore.IssuedToken, auditEventType string, auditPayload json.RawMessage, auditAt time.Time) (audit.Row, error)
}

// Auditor writes §16.7 audit rows on the Token Service's behalf. The
// in-memory dev path uses an Auditor backed by audit.ChainSet (via
// pkg/gateway/policy.ChainSetAppender), so a dev install still emits
// `token.exchanged`, `token.revoked`, and `token.exchange_rate_limited`
// rows even though the rows are lost on restart. The Postgres path
// reaches the durable chain through IssuedTokenAuditStore so the audit
// row and the issued-token INSERT share one COMMIT.
// spec: §13.3 lines 587, 597, 609.
type Auditor interface {
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error)
}

// Server is the §13.3 Token Service http handler.
type Server struct {
	signer       jwt.Signer
	verifier     jwt.Verifier
	issuer       string
	perDialect   map[string]time.Duration
	issuedTokens IssuedTokenStore
	auditor      Auditor
	metrics      Metrics

	rateLimiter *RateLimiter

	mu     sync.Mutex
	issued map[string]bool // jti seen
}

// Options configures the Server.
type Options struct {
	// Signer mints the issued JWTs. §4 wants the production Token
	// Service to use jwt.KMSSigner (KMS-envelope-backed signing
	// material); jwt.HMACSigner is the development signer. Both
	// satisfy jwt.Signer.
	Signer jwt.Signer

	Verifier jwt.Verifier
	Issuer   string

	// PerDialectCap is the §13.3 per-dialect lifetime ceiling keyed
	// by audience. Empty map means no cap (the validator falls
	// through to min(requested, subject.exp, actor.exp)).
	PerDialectCap map[string]time.Duration

	// IssuedTokens, when set, records every minted token before it is
	// returned (§13.3 write-before-issue). When nil, no durable
	// issued-token record is kept. The Postgres-backed
	// issuedtokenstore.Store satisfies IssuedTokenAuditStore so the
	// handler binds the issued_tokens INSERT and the token.exchanged
	// audit INSERT in a single COMMIT (§13.3 line 589).
	IssuedTokens IssuedTokenStore

	// Auditor, when set, receives the §13.3 token-exchange audit
	// events: `token.exchanged` on every accepted (and rejected)
	// exchange, `token.exchange_rate_limited` on a sampled rate-limit
	// rejection, and `token.revoked` from the GRPCServer revocation
	// hot path. When IssuedTokens satisfies IssuedTokenAuditStore the
	// Auditor is reached only for the rate-limit and revocation paths
	// (the write-before-issue success path uses the combined
	// IssuedTokenAuditStore.RecordWithAudit transaction).
	//
	// A nil Auditor disables emission, used by tests that do not
	// exercise the §16.7 audit catalog assertions.
	Auditor Auditor

	// Metrics, when set, receives the §16.1 Token Service catalog
	// emissions. Pass NoMetrics (or leave zero) for the test path.
	Metrics Metrics

	// RateLimit configures the §13.3 per-(tenant, sub) and global
	// per-tenant rate limits on POST /v1/oauth/token. Zero means
	// unlimited; production callers populate via flags.
	RateLimit RateLimitOptions

	// Now overrides time.Now for the handler and the rate limiter.
	// Tests inject a fixed clock; production leaves this nil.
	Now func() time.Time
}

// NewServer returns a Server. When Options.Verifier is nil and the
// configured Signer also verifies (both jwt.HMACSigner and
// jwt.KMSSigner do), the Server verifies caller, subject, and actor
// tokens with the signer itself.
func NewServer(opts Options) *Server {
	if opts.Verifier == nil {
		if v, ok := opts.Signer.(jwt.Verifier); ok {
			opts.Verifier = v
		}
	}
	if opts.PerDialectCap == nil {
		opts.PerDialectCap = map[string]time.Duration{}
	}
	if opts.Metrics == nil {
		opts.Metrics = NoMetrics{}
	}
	s := &Server{
		signer:       opts.Signer,
		verifier:     opts.Verifier,
		issuer:       opts.Issuer,
		perDialect:   opts.PerDialectCap,
		issuedTokens: opts.IssuedTokens,
		auditor:      opts.Auditor,
		metrics:      opts.Metrics,
		issued:       map[string]bool{},
	}
	if !opts.RateLimit.IsZero() {
		s.rateLimiter = NewRateLimiter(opts.RateLimit, opts.Now)
	}
	return s
}

// Handler returns the http.Handler that routes POST /v1/oauth/token.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/oauth/token", s.handle)
	return mux
}

// Request is the RFC 8693 form-or-JSON request body.
type Request struct {
	GrantType          string `json:"grant_type"`
	SubjectToken       string `json:"subject_token"`
	SubjectTokenType   string `json:"subject_token_type"`
	ActorToken         string `json:"actor_token"`
	ActorTokenType     string `json:"actor_token_type"`
	RequestedTokenType string `json:"requested_token_type"`
	Scope              string `json:"scope"`
	Audience           string `json:"audience"`
	ExpiresIn          int64  `json:"expires_in"`
}

// Response is the RFC 8693 successful response.
type Response struct {
	AccessToken     string `json:"access_token"`
	IssuedTokenType string `json:"issued_token_type"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int64  `json:"expires_in"`
	Scope           string `json:"scope,omitempty"`
}

const (
	grantTypeExchange = "urn:ietf:params:oauth:grant-type:token-exchange"
	tokenTypeJWT      = "urn:ietf:params:oauth:token-type:jwt"
)

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	start := s.clockNow()
	op := "exchange"
	finish := func(errClass string) {
		s.metrics.RecordRequestDuration(op, s.clockNow().Sub(start))
		if errClass != "" {
			s.metrics.IncErrors(op, errClass)
		}
	}

	// Caller token is the Authorization: Bearer header per §13.3.
	callerToken := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if callerToken == "" {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "missing Authorization: Bearer caller token")
		finish("invalid_client")
		return
	}
	callerClaims, err := s.verifier.Verify(callerToken)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		finish("invalid_client")
		return
	}

	req, err := parseRequest(r)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		finish("invalid_request")
		return
	}
	if req.GrantType != grantTypeExchange {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", req.GrantType)
		finish("unsupported_grant_type")
		return
	}
	if req.SubjectToken == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "subject_token is required")
		finish("invalid_request")
		return
	}

	subjectClaims, err := s.verifier.Verify(req.SubjectToken)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
		finish("invalid_grant")
		return
	}

	var actorClaims *jwt.Claims
	if req.ActorToken != "" {
		ac, err := s.verifier.Verify(req.ActorToken)
		if err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
			finish("invalid_grant")
			return
		}
		actorClaims = &ac
	}

	now := s.clockNow()
	// §13.3 line 607 / line 609: rate-limit per (tenant_id, sub) plus
	// a global per-tenant bucket. The check fires before any signing
	// or audit work so a brute-force attacker cannot consume signer
	// cycles. The rate limiter samples its audit emission per §13.3
	// line 609 to avoid saturating the per-tenant audit advisory lock.
	if s.rateLimiter != nil {
		dec := s.rateLimiter.Allow(now, callerClaims.TenantID, callerClaims.Subject)
		if !dec.Allowed {
			retryAfter := dec.RetryAfter
			if retryAfter <= 0 {
				retryAfter = time.Second
			}
			s.metrics.IncRateLimited(dec.LimitTier)
			if dec.AuditSampled {
				s.metrics.IncRateLimitedSampled(dec.LimitTier)
				s.emitRateLimitAudit(r.Context(), callerClaims.TenantID, callerClaims.Subject, dec.LimitTier, now)
			}
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			writeOAuthError(w, http.StatusTooManyRequests, "rate_limited",
				"§13.3 "+dec.LimitTier+" rate limit exceeded")
			finish("rate_limited")
			return
		}
	}

	exchangeReq := tokenexchange.Request{
		Subject: toExchangeToken(subjectClaims),
		Caller:  toExchangeToken(callerClaims),
		Requested: tokenexchange.Token{
			Scope:    splitScope(req.Scope),
			Audience: splitSpace(req.Audience),
		},
		RequestedExp: now.Add(durationFromExpiresIn(req.ExpiresIn)),
		Now:          now,
	}
	if actorClaims != nil {
		actor := toExchangeToken(*actorClaims)
		exchangeReq.Actor = &actor
	}
	if cap, ok := s.perDialect[req.Audience]; ok {
		exchangeReq.PerDialectCap = cap
	}

	issued, verr := tokenexchange.Validate(exchangeReq)
	if verr != nil {
		var ee *tokenexchange.ExchangeError
		if errors.As(verr, &ee) {
			// §13.3 line 585 / line 589: rejected exchanges still
			// emit a `token.exchanged` audit row with the
			// policy_result reason so the SIEM has cross-tenant
			// probe evidence.
			s.emitExchangeAudit(r.Context(), subjectClaims.TenantID, exchangeAuditPayload{
				CallerSub:    callerClaims.Subject,
				SubjectSub:   subjectClaims.Subject,
				PolicyResult: "rejected:" + ee.Reason,
				ErrorCode:    ee.Code,
				Audience:     splitSpace(req.Audience),
				Scope:        splitScope(req.Scope),
				Now:          now,
			})
			writeOAuthError(w, mapStatus(ee.Code), ee.Code, ee.Reason)
			finish(ee.Code)
			return
		}
		writeOAuthError(w, http.StatusInternalServerError, "server_error", verr.Error())
		finish("server_error")
		return
	}

	// Mint the issued token via the signer.
	jti := newJTI(now)
	s.mu.Lock()
	s.issued[jti] = true
	s.mu.Unlock()

	out := jwt.Claims{
		Issuer:          s.issuer,
		Subject:         issued.Subject,
		Audience:        issued.Audience,
		Expiry:          issued.Exp.Unix(),
		IssuedAt:        now.Unix(),
		JWTID:           jti,
		TenantID:        issued.TenantID,
		SessionID:       issued.SessionID,
		CallerType:      string(issued.CallerType),
		DelegationDepth: issued.DelegationDepth,
		Scope:           strings.Join(issued.Scope, " "),
		Typ:             auth.TokenType(issued.Typ),
	}
	signed, err := s.signer.Sign(out)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", err.Error())
		finish("server_error")
		return
	}

	// §13.3 write-before-issue: the issued-token row + the
	// token.exchanged audit row commit in one Postgres transaction
	// under the per-tenant audit advisory lock. The signed token is
	// not handed to the caller until COMMIT succeeds, so every live
	// token has a matching issued_tokens record and a matching
	// audit_log row. spec: §13.3 line 589.
	hash := sha256.Sum256([]byte(signed))
	rec := issuedtokenstore.IssuedToken{
		JTI:       jti,
		TenantID:  issued.TenantID,
		Subject:   issued.Subject,
		TokenHash: hash[:],
		Scope:     issued.Scope,
		Audience:  strings.Join(issued.Audience, " "),
		IssuedAt:  now,
		ExpiresAt: issued.Exp,
	}
	auditPayload := exchangeAuditPayload{
		CallerSub:       callerClaims.Subject,
		SubjectSub:      issued.Subject,
		JTI:             jti,
		Scope:           issued.Scope,
		Audience:        issued.Audience,
		DelegationDepth: issued.DelegationDepth,
		PolicyResult:    "accepted",
		Now:             now,
	}
	if as, ok := s.issuedTokens.(IssuedTokenAuditStore); ok {
		// Postgres path: one transaction binds the issued-token
		// INSERT and the audit_log INSERT under the §11.7 lock.
		if _, err := as.RecordWithAudit(r.Context(), rec, string(obsaudit.EventTokenExchanged),
			auditPayload.JSON(), now); err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error",
				"§13.3 write-before-issue failed: "+err.Error())
			finish("server_error")
			return
		}
	} else if s.issuedTokens != nil {
		// In-memory dev path: no advisory lock is needed because the
		// issued-tokens record and the audit append share no Postgres
		// state. We still write the issued record first so the
		// audit row references a durable jti.
		if err := s.issuedTokens.Record(r.Context(), rec); err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error",
				"issued-token record failed: "+err.Error())
			finish("server_error")
			return
		}
		s.emitExchangeAudit(r.Context(), issued.TenantID, auditPayload)
	} else {
		// Test-only path with no durable issued-token store.
		s.emitExchangeAudit(r.Context(), issued.TenantID, auditPayload)
	}

	writeJSON(w, http.StatusOK, Response{
		AccessToken:     signed,
		IssuedTokenType: tokenTypeJWT,
		TokenType:       "Bearer",
		ExpiresIn:       issued.Exp.Unix() - now.Unix(),
		Scope:           strings.Join(issued.Scope, " "),
	})
	finish("")
}

// exchangeAuditPayload is the §16.7 / §25.4 token.exchanged audit row
// payload. The §13.3 record contains only claim identifiers and
// metadata — never the raw token, the subject_token bytes, or the
// actor_token bytes.
// spec: §13.3 line 587 ("Token contents — access_token, subject_token,
// actor_token — are NEVER written to audit payloads").
type exchangeAuditPayload struct {
	CallerSub       string    `json:"caller_sub,omitempty"`
	SubjectSub      string    `json:"subject_sub,omitempty"`
	JTI             string    `json:"jti,omitempty"`
	Scope           []string  `json:"scope,omitempty"`
	Audience        []string  `json:"audience,omitempty"`
	DelegationDepth int       `json:"delegation_depth,omitempty"`
	PolicyResult    string    `json:"policy_result"`
	ErrorCode       string    `json:"error_code,omitempty"`
	Now             time.Time `json:"timestamp"`
}

// JSON returns the audit payload as canonical JSON for the audit row.
func (p exchangeAuditPayload) JSON() json.RawMessage {
	b, _ := json.Marshal(p)
	return json.RawMessage(b)
}

// emitExchangeAudit writes a token.exchanged audit row through the
// configured Auditor (the in-memory dev path). When no Auditor is
// configured the call is a no-op; the durable Postgres write-before-
// issue path covers the success case via IssuedTokenAuditStore.
// spec: §13.3 line 587.
func (s *Server) emitExchangeAudit(ctx context.Context, tenantID string, payload exchangeAuditPayload) {
	if s.auditor == nil || tenantID == "" {
		return
	}
	_, _ = s.auditor.Append(ctx, tenantID, string(obsaudit.EventTokenExchanged), payload.JSON(), payload.Now)
}

// rateLimitAuditPayload is the §13.3 token.exchange_rate_limited audit
// row body. Sampling guarantees one row per (tenant_id, sub,
// limit_tier) per 10s window per replica (§13.3 line 611).
type rateLimitAuditPayload struct {
	TenantID  string    `json:"tenant_id"`
	Sub       string    `json:"sub,omitempty"`
	LimitTier string    `json:"limit_tier"`
	Timestamp time.Time `json:"timestamp"`
}

// JSON returns the audit payload as canonical JSON for the audit row.
func (p rateLimitAuditPayload) JSON() json.RawMessage {
	b, _ := json.Marshal(p)
	return json.RawMessage(b)
}

// emitRateLimitAudit writes a token.exchange_rate_limited audit row on
// a sampled rate-limit rejection (one row per (tenant, sub, tier) per
// 10s window per replica, per §13.3 line 609 sampling discipline).
// spec: §13.3 line 609.
func (s *Server) emitRateLimitAudit(ctx context.Context, tenantID, sub, limitTier string, now time.Time) {
	if s.auditor == nil || tenantID == "" {
		return
	}
	payload := rateLimitAuditPayload{
		TenantID:  tenantID,
		Sub:       sub,
		LimitTier: limitTier,
		Timestamp: now,
	}
	_, _ = s.auditor.Append(ctx, tenantID, string(obsaudit.EventTokenExchangeRateLimited), payload.JSON(), now)
}

// EmitRevocation writes a token.revoked audit row for a Token Service-
// driven revocation (the GRPCServer.RevokeCredentials path). spec:
// §13.3 line 597.
func (s *Server) EmitRevocation(ctx context.Context, tenantID, sub, jti, reason string, at time.Time) {
	if s.auditor == nil || tenantID == "" {
		return
	}
	body, _ := json.Marshal(struct {
		Sub       string    `json:"sub,omitempty"`
		JTI       string    `json:"jti,omitempty"`
		Reason    string    `json:"reason,omitempty"`
		Timestamp time.Time `json:"timestamp"`
	}{Sub: sub, JTI: jti, Reason: reason, Timestamp: at})
	_, _ = s.auditor.Append(ctx, tenantID, string(obsaudit.EventTokenRevoked), json.RawMessage(body), at)
}

// clockNow returns the configured clock or time.Now.
func (s *Server) clockNow() time.Time {
	if s.rateLimiter != nil {
		return s.rateLimiter.Now()
	}
	return time.Now().UTC()
}

func parseRequest(r *http.Request) (Request, error) {
	switch {
	case strings.HasPrefix(r.Header.Get("Content-Type"), "application/json"):
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return Request{}, err
		}
		return req, nil
	default:
		if err := r.ParseForm(); err != nil {
			return Request{}, err
		}
		return Request{
			GrantType:          r.PostForm.Get("grant_type"),
			SubjectToken:       r.PostForm.Get("subject_token"),
			SubjectTokenType:   r.PostForm.Get("subject_token_type"),
			ActorToken:         r.PostForm.Get("actor_token"),
			ActorTokenType:     r.PostForm.Get("actor_token_type"),
			RequestedTokenType: r.PostForm.Get("requested_token_type"),
			Scope:              r.PostForm.Get("scope"),
			Audience:           r.PostForm.Get("audience"),
			ExpiresIn:          0,
		}, nil
	}
}

func toExchangeToken(c jwt.Claims) tokenexchange.Token {
	return tokenexchange.Token{
		TenantID:        c.TenantID,
		Subject:         c.Subject,
		SessionID:       c.SessionID,
		CallerType:      tokenexchange.CallerType(c.CallerType),
		DelegationDepth: c.DelegationDepth,
		Scope:           splitScope(c.Scope),
		Audience:        append([]string{}, c.Audience...),
		Typ:             tokenexchange.TokenType(c.Typ),
		Exp:             c.ExpiryTime(),
	}
}

func splitScope(s string) []string { return splitSpace(s) }
func splitSpace(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func durationFromExpiresIn(seconds int64) time.Duration {
	if seconds <= 0 {
		return time.Hour // sensible default per §13.3 dialect cap discipline
	}
	return time.Duration(seconds) * time.Second
}

func mapStatus(rfcCode string) int {
	switch rfcCode {
	case "invalid_request", "invalid_grant", "unauthorized_client", "unsupported_grant_type":
		return http.StatusBadRequest
	case "invalid_client":
		return http.StatusUnauthorized
	case "invalid_scope":
		return http.StatusBadRequest
	}
	return http.StatusBadRequest
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":             code,
		"error_description": description,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Helpers to keep go vet quiet about unused imports if we ever
// rearrange the types.
var _ = auth.TokenUserBearer

func newJTI(now time.Time) string {
	// Monotonic-ish jti: nanoseconds + a short counter via sync/atomic
	// would be ideal; sync.Mutex-guarded counter is fine for the
	// minimal service.
	jtiMu.Lock()
	defer jtiMu.Unlock()
	jtiCounter++
	return "jti_" + strings.ReplaceAll(now.UTC().Format("20060102150405.000000"), ".", "_") + "_" +
		strings.ToLower(intToHex(jtiCounter))
}

var (
	jtiMu      sync.Mutex
	jtiCounter uint64
)

func intToHex(n uint64) string {
	const hex = "0123456789abcdef"
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = hex[n&0xf]
		n >>= 4
	}
	return string(buf[i:])
}
