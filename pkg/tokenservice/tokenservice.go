// SPDX-License-Identifier: MIT

// Package tokenservice implements the §13.3 POST /v1/oauth/token
// endpoint as an http.Handler. The handler decodes RFC 8693
// token-exchange requests, drives pkg/tokenexchange.Validate against
// the supplied subject (and optional actor), signs the issued token
// with the configured Signer, and returns the RFC 8693 response
// envelope.
//
// This is the minimal Token Service: in-memory issued-tokens store,
// no Postgres advisory lock, no KMS — the dev HMAC signer from
// pkg/auth/jwt is plugged in via the Signer interface. Production
// swaps in a KMS-backed signer and the Postgres write-before-issue
// transaction behind the same handler shape.
package tokenservice

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/tokenexchange"
)

// IssuedTokenStore records issued-token metadata. The §13.3
// write-before-issue rule means the Token Service commits a record
// here before it returns a minted token to the caller, so every live
// token can be matched against its revocation state.
type IssuedTokenStore interface {
	Record(ctx context.Context, tok issuedtokenstore.IssuedToken) error
}

// Server is the §13.3 Token Service http handler.
type Server struct {
	signer       *jwt.HMACSigner
	verifier     jwt.Verifier
	issuer       string
	perDialect   map[string]time.Duration
	issuedTokens IssuedTokenStore

	mu     sync.Mutex
	issued map[string]bool // jti seen
}

// Options configures the Server.
type Options struct {
	Signer   *jwt.HMACSigner
	Verifier jwt.Verifier
	Issuer   string

	// PerDialectCap is the §13.3 per-dialect lifetime ceiling keyed
	// by audience. Empty map means no cap (the validator falls
	// through to min(requested, subject.exp, actor.exp)).
	PerDialectCap map[string]time.Duration

	// IssuedTokens, when set, records every minted token before it is
	// returned (§13.3 write-before-issue). When nil, no durable
	// issued-token record is kept.
	IssuedTokens IssuedTokenStore
}

// NewServer returns a Server.
func NewServer(opts Options) *Server {
	if opts.Verifier == nil {
		opts.Verifier = opts.Signer
	}
	if opts.PerDialectCap == nil {
		opts.PerDialectCap = map[string]time.Duration{}
	}
	return &Server{
		signer:       opts.Signer,
		verifier:     opts.Verifier,
		issuer:       opts.Issuer,
		perDialect:   opts.PerDialectCap,
		issuedTokens: opts.IssuedTokens,
		issued:       map[string]bool{},
	}
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
	// Caller token is the Authorization: Bearer header per §13.3.
	callerToken := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if callerToken == "" {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "missing Authorization: Bearer caller token")
		return
	}
	callerClaims, err := s.verifier.Verify(callerToken)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		return
	}

	req, err := parseRequest(r)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if req.GrantType != grantTypeExchange {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", req.GrantType)
		return
	}
	if req.SubjectToken == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "subject_token is required")
		return
	}

	subjectClaims, err := s.verifier.Verify(req.SubjectToken)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
		return
	}

	var actorClaims *jwt.Claims
	if req.ActorToken != "" {
		ac, err := s.verifier.Verify(req.ActorToken)
		if err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
			return
		}
		actorClaims = &ac
	}

	now := time.Now().UTC()
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
			writeOAuthError(w, mapStatus(ee.Code), ee.Code, ee.Reason)
			return
		}
		writeOAuthError(w, http.StatusInternalServerError, "server_error", verr.Error())
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
		return
	}

	// §13.3 write-before-issue: the issued-token metadata is recorded
	// before the token is handed to the caller. The token is not
	// returned when the record fails, so every live token is
	// matchable against its revocation state.
	if s.issuedTokens != nil {
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
		if err := s.issuedTokens.Record(r.Context(), rec); err != nil {
			writeOAuthError(w, http.StatusInternalServerError, "server_error",
				"issued-token record failed: "+err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, Response{
		AccessToken:     signed,
		IssuedTokenType: tokenTypeJWT,
		TokenType:       "Bearer",
		ExpiresIn:       issued.Exp.Unix() - now.Unix(),
		Scope:           strings.Join(issued.Scope, " "),
	})
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
