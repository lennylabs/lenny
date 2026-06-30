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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
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

// IssuedTokenRotationStore extends IssuedTokenAuditStore with the §13.3
// line 597 atomic token-rotation path: the new token's INSERT, its
// `token.exchanged` audit row, and the revoked_at stamp on the previous
// token all commit in one transaction. When the configured IssuedTokens
// dependency satisfies it (the Postgres-backed issuedtokenstore.Store
// does), a detected self-rotation revokes the caller's previous token
// the instant the replacement is minted, with no grace period. The
// returned `revoked` reports whether the previous token was live and got
// revoked, and `revokedSub` is its subject for the §16.7 token.revoked
// row. F-16.7.5.
type IssuedTokenRotationStore interface {
	IssuedTokenAuditStore
	RecordWithRotationAudit(ctx context.Context, tok issuedtokenstore.IssuedToken, prevJTI, revokedReason, exchangeEventType string, exchangePayload json.RawMessage, at time.Time) (revokedSub string, revoked bool, err error)
}

// The Postgres-backed issuedtokenstore.Store is the production
// IssuedTokenRotationStore; this assertion fails the build if the
// rotation write-before-issue surface ever drops off the store.
var _ IssuedTokenRotationStore = (*issuedtokenstore.Store)(nil)

// RevocationStore is the optional §13.3 line 603 / §8.3 recursive
// revocation surface. When the configured IssuedTokens dependency
// satisfies it (the Postgres-backed issuedtokenstore.Store does), the
// Token Service serves the `requested_token_type=...:access_token:revoked`
// revocation request: it revokes the subject token and cascades to its
// delegation descendants. F-16.7.5.
type RevocationStore interface {
	RevokeCascade(ctx context.Context, tenantID, rootJTI, rootReason, childReason string, at time.Time) ([]issuedtokenstore.RevokedToken, error)
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
//
// The Server is fully stateless across replicas: every minted token's
// metadata lives in the configured IssuedTokenStore (Postgres in
// production), and the jti primary key plus ErrAlreadyExists is the
// duplicate-detection contract. There is no in-process jti cache, so
// any replica can serve any request.
// spec: §4.3 line 209 ("fully stateless — all persistent state lives
// in Postgres ... so any replica can handle any request with no
// affinity requirements").
type Server struct {
	signer       jwt.Signer
	verifier     jwt.Verifier
	issuer       string
	perDialect   map[string]time.Duration
	issuedTokens IssuedTokenStore
	auditor      Auditor
	metrics      Metrics

	rateLimiter *RateLimiter

	// driftDegraded is the §13.3 line 595 self-check consulted on
	// every exchange. A non-nil function returning true causes the
	// exchange to fail with 503 token_validation_unavailable. F-13.3.5.
	driftDegraded func() bool

	// revocationPropagator, when set, publishes a revoked jti onto the
	// §12.6 Redis EventBus so peer replicas load it into their
	// in-memory revocation cache. Its success/failure sets the §16.7
	// token.revoked `propagation_mode` field (`eventbus` on success,
	// `postgres_only` on a nil propagator or a publish error — peers
	// then fall back to the authoritative Postgres check). F-16.7.5.
	revocationPropagator func(ctx context.Context, tenantID, jti string) error
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

	// DriftDegraded, when non-nil, is consulted on every exchange.
	// When it reports true (the §13.3 line 595 5s NTP-drift ceiling
	// is exceeded), the handler short-circuits the exchange with
	// 503 token_validation_unavailable rather than issuing or
	// validating a token whose `exp` it cannot trust. F-13.3.5.
	DriftDegraded func() bool

	// Now overrides time.Now for the handler and the rate limiter.
	// Tests inject a fixed clock; production leaves this nil.
	Now func() time.Time

	// RevocationPropagator, when set, publishes a revoked jti onto the
	// §12.6 EventBus for cluster-wide cache invalidation. It backs the
	// §16.7 token.revoked `propagation_mode` field. Nil leaves every
	// revocation `postgres_only` (Postgres remains the authoritative
	// store, so correctness is preserved; peers fall back to the
	// issued_tokens.revoked_at check). F-16.7.5.
	RevocationPropagator func(ctx context.Context, tenantID, jti string) error
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
		signer:        opts.Signer,
		verifier:      opts.Verifier,
		issuer:        opts.Issuer,
		perDialect:    opts.PerDialectCap,
		issuedTokens:  opts.IssuedTokens,
		auditor:       opts.Auditor,
		metrics:       opts.Metrics,
		driftDegraded: opts.DriftDegraded,

		revocationPropagator: opts.RevocationPropagator,
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

	// revokedTokenType is the §13.3 line 603 requested_token_type that
	// turns a token-exchange into a recursive-revocation request.
	revokedTokenType = "urn:ietf:params:oauth:token-type:access_token:revoked"
)

// §16.7 token.revoked revocation_reason and propagation_mode enums.
const (
	revocationReasonExplicit         = "explicit_revoke"
	revocationReasonCascade          = "cascade_from_parent"
	revocationReasonRotationReplaced = "rotation_replaced"

	propagationModeEventBus     = "eventbus"
	propagationModePostgresOnly = "postgres_only"
)

// revokedAuditPayload is the §16.7 line 666 `token.revoked` audit row
// payload. It carries claim identifiers and revocation provenance only,
// never the raw token bytes.
type revokedAuditPayload struct {
	RevokedJTI       string    `json:"revoked_jti"`
	RevokedSub       string    `json:"revoked_sub,omitempty"`
	RevocationReason string    `json:"revocation_reason"`
	CascadeRootJTI   string    `json:"cascade_root_jti,omitempty"`
	PropagationMode  string    `json:"propagation_mode"`
	Timestamp        time.Time `json:"timestamp"`
}

// handle dispatches POST /v1/oauth/token through the §13.3 exchange
// pipeline: a drift gate, caller authentication, request parse and
// validation, rate limiting, and then either the recursive-revocation
// branch or the mint-and-write-before-issue branch. Each stage is a
// helper that owns its error envelope and `// spec:` citations; handle
// is the ordered sequence and returns as soon as a stage has written a
// response. spec: §13 token service.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	finish := s.newRequestFinisher()

	if !s.checkDrift(w, finish) {
		return
	}
	callerClaims, ok := s.authenticateCaller(w, r, finish)
	if !ok {
		return
	}
	req, ok := parseAndValidateRequest(w, r, finish)
	if !ok {
		return
	}
	subjectClaims, actorClaims, ok := s.verifyExchangeTokens(w, req, finish)
	if !ok {
		return
	}

	now := s.clockNow()
	if !s.allowRateLimited(w, r, callerClaims, now, finish) {
		return
	}

	// §13.3 line 603 / §8.3 recursive revocation: a token-exchange
	// carrying requested_token_type=...:access_token:revoked is a
	// revocation request, not a mint. The subject_token is the root to
	// revoke; the Token Service cascades to its delegation descendants
	// and emits one token.revoked per revoked node. F-16.7.5.
	if req.RequestedTokenType == revokedTokenType {
		s.handleRevocationRequest(w, r, callerClaims, subjectClaims, now, finish)
		return
	}

	s.handleExchange(w, r, req, callerClaims, subjectClaims, actorClaims, now, finish)
}

// newRequestFinisher returns the per-request metric finisher closed over
// the request start time. Calling it with a non-empty error class
// records the §16.1 error counter (and the 5xx counter for 5xx classes,
// per F-13.3.4); calling it with "" records only the request duration on
// the success path.
func (s *Server) newRequestFinisher() func(errClass string) {
	start := s.clockNow()
	const op = "exchange"
	return func(errClass string) {
		s.metrics.RecordRequestDuration(op, s.clockNow().Sub(start))
		if errClass != "" {
			s.metrics.IncErrors(op, errClass)
			// §16.1 lenny_oauth_token_5xx_total feeds the §16.5
			// TokenStoreUnavailable alert; record every 5xx error class so a
			// Postgres outage is distinguishable from a client error. The
			// 4xx classes (invalid_*, rate_limited) are excluded. F-13.3.4.
			if is5xxErrorClass(errClass) {
				s.metrics.Inc5xx(errClass)
			}
		}
	}
}

// checkDrift is the §13.3 line 595 drift gate. It reports false (and
// writes 503 token_validation_unavailable) when this replica's NTP drift
// exceeds the 5s ceiling, so the handler refuses to issue or validate a
// token whose `exp` it cannot trust. The replica's /healthz
// simultaneously reports degraded so the Service controller removes it
// from endpoints. F-13.3.5.
func (s *Server) checkDrift(w http.ResponseWriter, finish func(string)) bool {
	if s.driftDegraded != nil && s.driftDegraded() {
		writeOAuthError(w, http.StatusServiceUnavailable, "token_validation_unavailable",
			"replica wall-clock drift exceeds the §13.3 5s ceiling")
		finish("token_validation_unavailable")
		return false
	}
	return true
}

// authenticateCaller verifies the §13.3 Authorization: Bearer caller
// token. It returns the caller claims and true on success; on a missing
// or invalid token it writes 401 invalid_client and returns false.
func (s *Server) authenticateCaller(w http.ResponseWriter, r *http.Request, finish func(string)) (jwt.Claims, bool) {
	callerToken := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if callerToken == "" {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "missing Authorization: Bearer caller token")
		finish("invalid_client")
		return jwt.Claims{}, false
	}
	callerClaims, err := s.verifier.Verify(callerToken)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		finish("invalid_client")
		return jwt.Claims{}, false
	}
	return callerClaims, true
}

// parseAndValidateRequest decodes the RFC 8693 request body and enforces
// the §13.3 request preconditions (grant_type is the token-exchange
// grant and subject_token is present). It returns the request and true
// on success; on a malformed body or a failed precondition it writes the
// matching 400 envelope and returns false.
func parseAndValidateRequest(w http.ResponseWriter, r *http.Request, finish func(string)) (Request, bool) {
	req, err := parseRequest(r)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		finish("invalid_request")
		return Request{}, false
	}
	if req.GrantType != grantTypeExchange {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", req.GrantType)
		finish("unsupported_grant_type")
		return Request{}, false
	}
	if req.SubjectToken == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "subject_token is required")
		finish("invalid_request")
		return Request{}, false
	}
	return req, true
}

// verifyExchangeTokens verifies the subject token and the optional actor
// token. It returns the subject claims, the actor claims (nil when no
// actor_token was supplied), and true on success; on a verification
// failure it writes 400 invalid_grant and returns false.
func (s *Server) verifyExchangeTokens(w http.ResponseWriter, req Request, finish func(string)) (jwt.Claims, *jwt.Claims, bool) {
	subjectClaims, err := s.verifier.Verify(req.SubjectToken)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
		finish("invalid_grant")
		return jwt.Claims{}, nil, false
	}

	var actorClaims *jwt.Claims
	if req.ActorToken != "" {
		ac, err := s.verifier.Verify(req.ActorToken)
		if err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
			finish("invalid_grant")
			return jwt.Claims{}, nil, false
		}
		actorClaims = &ac
	}
	return subjectClaims, actorClaims, true
}

// allowRateLimited applies the §13.3 line 607 / line 609 per-(tenant,
// sub) and global per-tenant rate limits. It returns true when the
// request is allowed (or no limiter is configured); on a rejection it
// emits the unconditional and sampled metrics, the sampled audit row,
// the Retry-After header, the 429 rate_limited envelope, and returns
// false. The check fires before any signing or audit work so a
// brute-force attacker cannot consume signer cycles.
func (s *Server) allowRateLimited(w http.ResponseWriter, r *http.Request, callerClaims jwt.Claims, now time.Time, finish func(string)) bool {
	if s.rateLimiter == nil {
		return true
	}
	dec := s.rateLimiter.Allow(now, callerClaims.TenantID, callerClaims.Subject)
	if dec.Allowed {
		return true
	}
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
	return false
}

// handleExchange runs the mint branch of the §13.3 pipeline: it
// validates the exchange, mints and signs the issued token, and commits
// the write-before-issue record. Each step writes its own error envelope
// on failure and returns; on success it renders the RFC 8693 response.
func (s *Server) handleExchange(w http.ResponseWriter, r *http.Request, req Request, callerClaims, subjectClaims jwt.Claims, actorClaims *jwt.Claims, now time.Time, finish func(string)) {
	exchangeReq := s.buildExchangeRequest(req, callerClaims, subjectClaims, actorClaims, now)

	issued, ok := s.validateExchange(w, r, req, callerClaims, subjectClaims, exchangeReq, now, finish)
	if !ok {
		return
	}

	jti, signed, ok := s.mintToken(w, issued, now, finish)
	if !ok {
		return
	}

	if !s.persistIssuedToken(w, r, jti, issued, signed, exchangeReq.PerDialectCap, callerClaims, subjectClaims, actorClaims, now, finish) {
		return
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

// buildExchangeRequest assembles the tokenexchange.Request from the
// caller, subject, and optional actor claims, and resolves the §13.3
// per-dialect lifetime cap.
func (s *Server) buildExchangeRequest(req Request, callerClaims, subjectClaims jwt.Claims, actorClaims *jwt.Claims, now time.Time) tokenexchange.Request {
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
	if dialectCap := s.resolveDialectCap(exchangeReq.Requested.Audience, req.Audience); dialectCap > 0 {
		exchangeReq.PerDialectCap = dialectCap
	}
	return exchangeReq
}

// resolveDialectCap returns the §13.3 per-dialect lifetime cap to apply.
// The cap key is one audience dialect (e.g. lenny-gateway, lenny-ops,
// llm-proxy). An exchange can request a multi-value audience (RFC 8693),
// so it iterates the requested audiences and applies the tightest
// matching cap. This closes the F-13.3.12 lookup bug where a multi-value
// string was passed verbatim and never matched a single-key entry. The
// returned value is recorded on the issued_tokens row for forensic
// reconstruction. spec: §4.3 line 193, §13.3 line 564.
func (s *Server) resolveDialectCap(requestedAudience []string, rawAudience string) time.Duration {
	var applied time.Duration
	for _, aud := range requestedAudience {
		if c, ok := s.perDialect[aud]; ok {
			if applied == 0 || c < applied {
				applied = c
			}
		}
	}
	if applied == 0 {
		// Back-compat: callers that still pass a singleton through
		// req.Audience (the raw form, before whitespace splitting) hit
		// the original direct lookup. This preserves dialect cap
		// enforcement on the singleton "lenny-gateway" path even when
		// the exchange request carried no Audience list (e.g.
		// non-RFC-8693 callers).
		if c, ok := s.perDialect[rawAudience]; ok {
			applied = c
		}
	}
	return applied
}

// validateExchange runs tokenexchange.Validate and renders the §13.3
// rejection path. It returns the issued token and true on acceptance; on
// a rejection it emits the §13.3 line 585 token.exchanged audit row,
// writes the mapped error envelope, and returns false. A failed
// rejection-audit write fails closed with 500 token_exchange_failed
// (§13.3 line 589).
func (s *Server) validateExchange(w http.ResponseWriter, r *http.Request, req Request, callerClaims, subjectClaims jwt.Claims, exchangeReq tokenexchange.Request, now time.Time, finish func(string)) (tokenexchange.Issued, bool) {
	issued, verr := tokenexchange.Validate(exchangeReq)
	if verr == nil {
		return issued, true
	}
	var ee *tokenexchange.ExchangeError
	if errors.As(verr, &ee) {
		// §13.3 line 585 / line 589: rejected exchanges still
		// emit a `token.exchanged` audit row with the
		// policy_result reason so the SIEM has cross-tenant
		// probe evidence.
		if auditErr := s.recordExchangeAudit(r.Context(), subjectClaims.TenantID, exchangeAuditPayload{
			CallerSub:    callerClaims.Subject,
			SubjectSub:   subjectClaims.Subject,
			PolicyResult: "rejected:" + ee.Reason,
			ErrorCode:    ee.Code,
			Audience:     splitSpace(req.Audience),
			Scope:        splitScope(req.Scope),
			Now:          now,
		}); auditErr != nil {
			// §13.3 line 589: fail closed when the rejection-audit
			// write fails. Return 500 token_exchange_failed with the
			// originally intended rejection reason in the error
			// body's detail so operators can reconstruct the attempt,
			// rather than silently swallowing the rejection by
			// returning the 4xx the client could retry.
			writeOAuthError(w, http.StatusInternalServerError, "token_exchange_failed",
				"§13.3 rejection-audit write failed; intended rejection: "+ee.Reason)
			finish("token_exchange_failed")
			return tokenexchange.Issued{}, false
		}
		writeOAuthError(w, mapStatus(ee.Code), ee.Code, ee.Reason)
		finish(ee.Code)
		return tokenexchange.Issued{}, false
	}
	writeOAuthError(w, http.StatusInternalServerError, "server_error", verr.Error())
	finish("server_error")
	return tokenexchange.Issued{}, false
}

// mintToken builds the issued JWT claims and signs them. It returns the
// freshly minted jti, the signed token, and true on success. The jti is
// a crypto/rand-derived 128-bit identifier so two replicas minting at
// the same wall-clock instant do not collide on the issued_tokens
// primary key. spec: §4.3 line 209 (no affinity requirements / replicas
// serve interchangeably). An open JWTSigner circuit breaker fails with
// 503 KMS_SIGNING_UNAVAILABLE and `retryable: true` (§10.2 line 225,
// F-10.2.6).
func (s *Server) mintToken(w http.ResponseWriter, issued tokenexchange.Issued, now time.Time, finish func(string)) (string, string, bool) {
	jti, err := newJTI()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", err.Error())
		finish("server_error")
		return "", "", false
	}

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
		// §13.3 line 580: stamp the preserved/narrowed operability-tool
		// allowlist onto the issued token so an operability-scope surface
		// has the claim to enforce against. F-13.3.11.
		AuthorizedTools: issued.AuthorizedTools,
		Typ:             auth.TokenType(issued.Typ),
	}
	signed, err := s.signer.Sign(out)
	if err != nil {
		// spec: §10.2 line 225 — when the JWTSigner circuit breaker is
		// open (or otherwise reports the KMS-backed signing material is
		// unreachable), the mint fails with 503 KMS_SIGNING_UNAVAILABLE
		// and `retryable: true`. The §16.5 KMSSigningUnavailable alert
		// reads `lenny_gateway_kms_signing_errors_total`. F-10.2.6.
		if errors.Is(err, jwt.ErrSigningUnavailable) {
			writeKMSUnavailable(w, err.Error())
			finish("kms_signing_unavailable")
			return "", "", false
		}
		writeOAuthError(w, http.StatusInternalServerError, "server_error", err.Error())
		finish("server_error")
		return "", "", false
	}
	return jti, signed, true
}

// persistIssuedToken commits the §13.3 write-before-issue record: the
// issued_tokens row and the token.exchanged audit row commit in one
// Postgres transaction under the per-tenant audit advisory lock, so the
// signed token is not handed to the caller until COMMIT succeeds. It
// routes between the rotation, transactional-audit, in-memory dev, and
// test-only store paths and returns true once the record is durable.
// spec: §13.3 line 589.
func (s *Server) persistIssuedToken(w http.ResponseWriter, r *http.Request, jti string, issued tokenexchange.Issued, signed string, dialectCap time.Duration, callerClaims, subjectClaims jwt.Claims, actorClaims *jwt.Claims, now time.Time, finish func(string)) bool {
	hash := sha256.Sum256([]byte(signed))
	rec := issuedtokenstore.IssuedToken{
		JTI:       jti,
		TenantID:  issued.TenantID,
		Subject:   issued.Subject,
		TokenHash: hash[:],
		Scope:     issued.Scope,
		// Audience: legacy space-joined form for back-compat with
		// pre-0058 readers. Audiences carries the list form so a
		// forensic reverse lookup can match individual audiences.
		// spec: §4.3 line 193, migration 0058.
		Audience:                 strings.Join(issued.Audience, " "),
		Audiences:                append([]string{}, issued.Audience...),
		DialectCapAppliedSeconds: int(dialectCap.Seconds()),
		IssuedAt:                 now,
		ExpiresAt:                issued.Exp,
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

	isRotation := s.isSelfRotation(issued, callerClaims, subjectClaims, actorClaims)

	auditStore, _ := s.issuedTokens.(IssuedTokenAuditStore)
	rotationStore, _ := s.issuedTokens.(IssuedTokenRotationStore)
	switch {
	case isRotation && rotationStore != nil:
		// Postgres rotation path: the new issued-token INSERT, the
		// token.exchanged audit row, and the revoked_at stamp on the
		// previous token commit in one transaction (§13.3 line 597).
		revokedSub, revoked, err := rotationStore.RecordWithRotationAudit(r.Context(), rec,
			subjectClaims.JWTID, revocationReasonRotationReplaced,
			string(obsaudit.EventTokenExchanged), auditPayload.JSON(), now)
		if err != nil {
			writeIssueStoreError(w, finish, err)
			return false
		}
		if revoked {
			// The token.revoked row and cluster propagation run after the
			// durable commit so propagation_mode reflects the real publish
			// outcome.
			s.emitRotationRevoked(r.Context(), issued.TenantID, subjectClaims.JWTID, revokedSub, now)
		}
	case auditStore != nil:
		// Postgres path: one transaction binds the issued-token
		// INSERT and the audit_log INSERT under the §11.7 lock.
		if _, err := auditStore.RecordWithAudit(r.Context(), rec, string(obsaudit.EventTokenExchanged),
			auditPayload.JSON(), now); err != nil {
			writeIssueStoreError(w, finish, err)
			return false
		}
	case s.issuedTokens != nil:
		// In-memory dev path: no advisory lock is needed because the
		// issued-tokens record and the audit append share no Postgres
		// state. We still write the issued record first so the
		// audit row references a durable jti.
		if err := s.issuedTokens.Record(r.Context(), rec); err != nil {
			writeIssueStoreError(w, finish, err)
			return false
		}
		// The accepted-exchange write-before-issue fail-closed obligation
		// (§13.3 line 589) is met by the production Postgres path above,
		// which routes through auditStore.RecordWithAudit/writeIssueStoreError.
		// This in-memory dev path is non-production, so the audit write is
		// best-effort and the returned error is discarded.
		_ = s.recordExchangeAudit(r.Context(), issued.TenantID, auditPayload)
	default:
		// Test-only path with no durable issued-token store; the audit
		// write is best-effort here too (non-production), so the error is
		// discarded.
		_ = s.recordExchangeAudit(r.Context(), issued.TenantID, auditPayload)
	}
	return true
}

// isSelfRotation reports whether this exchange is a §13.3 line 597
// self-rotation: the caller presents its own current token as the
// subject and requests a privilege-equivalent replacement (same audience
// set, same scope, no actor/delegation). On detection the previous token
// is revoked atomically with the mint, and a §16.7 token.revoked row with
// revocation_reason=rotation_replaced is emitted. The guard is
// deliberately narrow: a cross-audience or scope-narrowing self-exchange
// is a scope_narrow / dialect derivation, not a rotation, so its subject
// token is left live. F-16.7.5.
func (s *Server) isSelfRotation(issued tokenexchange.Issued, callerClaims, subjectClaims jwt.Claims, actorClaims *jwt.Claims) bool {
	return actorClaims == nil &&
		callerClaims.JWTID != "" &&
		callerClaims.JWTID == subjectClaims.JWTID &&
		sameStringSet(issued.Audience, subjectClaims.Audience) &&
		sameStringSet(issued.Scope, splitScope(subjectClaims.Scope))
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

// recordExchangeAudit writes a token.exchanged audit row through the
// configured Auditor (the in-memory dev path) and returns the
// auditor.Append error so callers can fail closed on a rejection-audit
// write failure (§13.3 line 589). When no Auditor is configured the call
// is a no-op returning nil; the durable Postgres write-before-issue path
// covers the accepted-exchange success case via IssuedTokenAuditStore.
// spec: §13.3 line 587, line 589.
func (s *Server) recordExchangeAudit(ctx context.Context, tenantID string, payload exchangeAuditPayload) error {
	if s.auditor == nil || tenantID == "" {
		return nil
	}
	_, err := s.auditor.Append(ctx, tenantID, string(obsaudit.EventTokenExchanged), payload.JSON(), payload.Now)
	return err
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

// handleRevocationRequest serves the §13.3 line 603 / §8.3 recursive
// revocation request (`requested_token_type=...:access_token:revoked`).
// It revokes the subject token (the root) and every delegation
// descendant reachable through parent_jti, then emits one §16.7
// token.revoked audit row per revoked node — `explicit_revoke` for the
// root and `cascade_from_parent` for each descendant — and pushes each
// revoked jti onto the cluster revocation propagation seam. The
// revocation is durable in Postgres before the success response is
// returned. F-16.7.5.
func (s *Server) handleRevocationRequest(w http.ResponseWriter, r *http.Request, caller, subject jwt.Claims, now time.Time, finish func(string)) {
	if caller.TenantID != subject.TenantID {
		writeOAuthError(w, http.StatusForbidden, "invalid_grant", "cross-tenant revocation is not permitted")
		finish("invalid_grant")
		return
	}
	if subject.JWTID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "subject_token carries no jti to revoke")
		finish("invalid_request")
		return
	}
	rs, ok := s.issuedTokens.(RevocationStore)
	if !ok {
		w.Header().Set("Retry-After", "5")
		writeOAuthError(w, http.StatusServiceUnavailable, "token_store_unavailable",
			"§13.3 recursive revocation requires a durable issued-token store")
		finish("token_store_unavailable")
		return
	}
	revoked, err := rs.RevokeCascade(r.Context(), subject.TenantID, subject.JWTID,
		revocationReasonExplicit, revocationReasonCascade, now)
	if errors.Is(err, issuedtokenstore.ErrNotFound) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
			"subject_token jti is not a known issued token")
		finish("invalid_grant")
		return
	}
	if err != nil {
		writeIssueStoreError(w, finish, err)
		return
	}
	for _, rt := range revoked {
		s.emitTokenRevoked(r.Context(), subject.TenantID, rt, subject.JWTID, now)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"revoked":       true,
		"revoked_count": len(revoked),
	})
	finish("")
}

// emitTokenRevoked writes the §16.7 token.revoked audit row for one
// revoked node and propagates the revocation cluster-wide. The root of
// a cascade carries `revocation_reason=explicit_revoke` and no
// `cascade_root_jti`; each descendant carries `cascade_from_parent` and
// the root jti. F-16.7.5.
func (s *Server) emitTokenRevoked(ctx context.Context, tenantID string, rt issuedtokenstore.RevokedToken, rootJTI string, at time.Time) {
	reason := revocationReasonCascade
	cascadeRoot := rootJTI
	if rt.IsRoot {
		reason = revocationReasonExplicit
		cascadeRoot = ""
	}
	mode := s.propagateRevocation(ctx, tenantID, rt.JTI)
	if s.auditor == nil || tenantID == "" {
		return
	}
	body, _ := json.Marshal(revokedAuditPayload{
		RevokedJTI:       rt.JTI,
		RevokedSub:       rt.Subject,
		RevocationReason: reason,
		CascadeRootJTI:   cascadeRoot,
		PropagationMode:  mode,
		Timestamp:        at,
	})
	_, _ = s.auditor.Append(ctx, tenantID, string(obsaudit.EventTokenRevoked), json.RawMessage(body), at)
}

// emitRotationRevoked writes the §16.7 token.revoked row for a
// rotation_replaced revocation and propagates the revoked jti
// cluster-wide. The revoked token is the caller's previous token, which
// was atomically revoked inside the write-before-issue transaction that
// minted its replacement (§13.3 line 597). A rotation is not a cascade,
// so the row carries no cascade_root_jti. F-16.7.5.
func (s *Server) emitRotationRevoked(ctx context.Context, tenantID, revokedJTI, revokedSub string, at time.Time) {
	mode := s.propagateRevocation(ctx, tenantID, revokedJTI)
	if s.auditor == nil || tenantID == "" {
		return
	}
	body, _ := json.Marshal(revokedAuditPayload{
		RevokedJTI:       revokedJTI,
		RevokedSub:       revokedSub,
		RevocationReason: revocationReasonRotationReplaced,
		PropagationMode:  mode,
		Timestamp:        at,
	})
	_, _ = s.auditor.Append(ctx, tenantID, string(obsaudit.EventTokenRevoked), json.RawMessage(body), at)
}

// propagateRevocation publishes a revoked jti onto the cluster EventBus
// and reports the resulting §16.7 propagation_mode. A nil propagator or
// a publish failure yields `postgres_only`: Postgres remains the
// authoritative revocation store, so peers fall back to the
// issued_tokens.revoked_at check. F-16.7.5.
func (s *Server) propagateRevocation(ctx context.Context, tenantID, jti string) string {
	if s.revocationPropagator == nil {
		return propagationModePostgresOnly
	}
	if err := s.revocationPropagator(ctx, tenantID, jti); err != nil {
		return propagationModePostgresOnly
	}
	return propagationModeEventBus
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
		// §13.3 line 583(e): the subject's narrowed operability-tool
		// allowlist is carried into the exchange so Validate can preserve
		// or further narrow it rather than dropping it.
		AuthorizedTools: append([]string{}, c.AuthorizedTools...),
		Typ:             tokenexchange.TokenType(c.Typ),
		Exp:             c.ExpiryTime(),
	}
}

// sameStringSet reports whether a and b contain the same set of
// non-empty strings, ignoring order and duplicates. The §13.3 rotation
// guard uses it to distinguish a privilege-equivalent self-rotation
// (issued audience and scope equal the subject's) from a scope-narrowing
// or cross-dialect self-exchange. F-16.7.5.
func sameStringSet(a, b []string) bool {
	sa := stringSet(a)
	sb := stringSet(b)
	if len(sa) != len(sb) {
		return false
	}
	for k := range sa {
		if _, ok := sb[k]; !ok {
			return false
		}
	}
	return true
}

func stringSet(xs []string) map[string]struct{} {
	s := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		if x != "" {
			s[x] = struct{}{}
		}
	}
	return s
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

// is5xxErrorClass reports whether the RFC 8693 / §13.3 error class maps
// to a 5xx response, so the §16.1 lenny_oauth_token_5xx_total counter is
// incremented for it. The 4xx classes (invalid_client, invalid_grant,
// invalid_request, unsupported_grant_type, invalid_scope) and the 429
// rate_limited class are excluded. spec: §16.1. F-13.3.4.
func is5xxErrorClass(c string) bool {
	switch c {
	case "server_error", "token_exchange_failed", "token_store_unavailable",
		"token_validation_unavailable", "kms_signing_unavailable":
		return true
	}
	return false
}

// writeIssueStoreError maps a write-before-issue store failure to the
// §13.3 response. A Postgres outage (primary unreachable or in failover)
// is unavailability: the caller receives `503 token_store_unavailable`
// and the §16.5 TokenStoreUnavailable alert fires; the platform does not
// fall back to issuing tokens without audit coverage. Any other failure
// (a constraint violation, a logic bug) is a `500 token_exchange_failed`:
// the write-before-issue invariant could not be satisfied so no token is
// issued, matching the §13.3 line 589 code for a failed exchange write.
// spec: §13.3 line 589, line 591. F-13.3.4.
func writeIssueStoreError(w http.ResponseWriter, finish func(string), err error) {
	if pgtenant.IsUnavailable(err) {
		w.Header().Set("Retry-After", "5")
		writeOAuthError(w, http.StatusServiceUnavailable, "token_store_unavailable",
			"§13.3 token issuance is unavailable during a Postgres outage: "+err.Error())
		finish("token_store_unavailable")
		return
	}
	writeOAuthError(w, http.StatusInternalServerError, "token_exchange_failed",
		"§13.3 write-before-issue failed: "+err.Error())
	finish("token_exchange_failed")
}

// writeKMSUnavailable writes the §10.2 line 225 / §15.1 line 1102
// KMS_SIGNING_UNAVAILABLE envelope: HTTP 503 with `retryable: true` so
// the client retries the mint after the circuit-breaker cooldown
// elapses. The body uses the Lenny error envelope (rather than the
// OAuth envelope) because §15.1's catalog row carries the code and
// transient classification, and a client matching on the catalog
// expects the lenny shape.
func writeKMSUnavailable(w http.ResponseWriter, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "30")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":      "KMS_SIGNING_UNAVAILABLE",
			"message":   description,
			"retryable": true,
		},
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

// newJTI returns a fresh RFC 7519 token identifier. The identifier is a
// hex-encoded 128-bit value drawn from crypto/rand so two simultaneously
// started Token Service replicas cannot collide on the
// `issued_tokens.jti` primary key. spec: §4.3 line 209 — replicas serve
// any request with no affinity requirements, which requires
// collision-safe identifiers across processes.
//
// The same pattern is used at pkg/credential/lease_mint.go for lease
// IDs.
func newJTI() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("tokenservice: generate jti: %w", err)
	}
	return "jti_" + hex.EncodeToString(b[:]), nil
}
