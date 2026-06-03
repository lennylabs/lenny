// SPDX-License-Identifier: MIT

package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// RefreshMetrics records the outcome of each §25.4 service-account
// token refresh. The production adapter increments
// lenny_ops_gateway_auth_token_refresh_total{status}; tests pass a
// recording stub or the Noop.
//
// spec: §25.4 line 1971.
type RefreshMetrics interface {
	// RefreshDone records one refresh attempt. status is "success"
	// when a fresh token was loaded and "failure" when the loader
	// returned an error or the token could not be parsed.
	RefreshDone(status string)
}

// NoopRefreshMetrics discards refresh outcomes. It is the default when
// a Client is built without a metrics adapter.
type NoopRefreshMetrics struct{}

// RefreshDone implements RefreshMetrics.
func (NoopRefreshMetrics) RefreshDone(string) {}

// TokenLoader yields the current raw bearer token. The production
// loader reads the projected ServiceAccount token volume; tests supply
// a closure.
type TokenLoader func() (string, error)

// FileTokenLoader reads the bearer token from path on every call, the
// projected-ServiceAccount-token volume the kubelet rotates in place
// (§25.4 "Calling the Gateway"). Reading per-refresh — rather than
// caching the file handle — picks up the kubelet's in-place rotation
// without an inotify watch.
func FileTokenLoader(path string) TokenLoader {
	return func() (string, error) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read service-account token %q: %w", path, err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
}

// RefreshingTokenSourceConfig configures a RefreshingTokenSource.
type RefreshingTokenSource struct {
	loader              TokenLoader
	refreshBeforeExpiry time.Duration
	minTokenTTL         time.Duration
	metrics             RefreshMetrics
	now                 func() time.Time

	mu      sync.Mutex
	token   string
	expiry  time.Time // zero when the token carries no exp claim
	revoked bool
}

// TokenSourceConfig configures a RefreshingTokenSource.
type TokenSourceConfig struct {
	// Loader reads the raw bearer token. Required.
	Loader TokenLoader
	// RefreshBeforeExpiry is how far ahead of the token's exp claim a
	// refresh is triggered (§25.4 ops.security.oidc
	// tokenRefreshBeforeExpirySeconds). A non-positive value defaults
	// to 5 minutes.
	RefreshBeforeExpiry time.Duration
	// MinTokenTTL is the §25.4 minTokenTTLSeconds floor. NewRefreshing-
	// TokenSource rejects a startup token whose remaining lifetime is
	// below this floor so a misconfigured short-lived projection fails
	// fast rather than thrashing the refresh loop. Zero disables the
	// floor.
	MinTokenTTL time.Duration
	// Metrics records refresh outcomes. Nil uses NoopRefreshMetrics.
	Metrics RefreshMetrics
	// Now overrides the clock. Nil uses time.Now.
	Now func() time.Time
}

// NewRefreshingTokenSource loads the initial token, enforces the
// MinTokenTTL floor, and returns a TokenSource that pre-emptively
// refreshes before the token expires and on an explicit revocation
// signal.
//
// spec: §25.4 lines 1956-1971.
func NewRefreshingTokenSource(cfg TokenSourceConfig) (*RefreshingTokenSource, error) {
	if cfg.Loader == nil {
		return nil, errors.New("refreshing token source: Loader is required")
	}
	refreshBefore := cfg.RefreshBeforeExpiry
	if refreshBefore <= 0 {
		refreshBefore = 5 * time.Minute
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	metrics := cfg.Metrics
	if metrics == nil {
		metrics = NoopRefreshMetrics{}
	}
	s := &RefreshingTokenSource{
		loader:              cfg.Loader,
		refreshBeforeExpiry: refreshBefore,
		minTokenTTL:         cfg.MinTokenTTL,
		metrics:             metrics,
		now:                 now,
	}
	if err := s.refresh(); err != nil {
		return nil, err
	}
	// §25.4 line 1959: reject a startup token whose remaining lifetime
	// is already below the configured floor.
	if s.minTokenTTL > 0 && !s.expiry.IsZero() {
		if ttl := s.expiry.Sub(now()); ttl < s.minTokenTTL {
			return nil, fmt.Errorf("refreshing token source: startup token TTL %s is below the minTokenTTL floor %s", ttl.Round(time.Second), s.minTokenTTL)
		}
	}
	return s, nil
}

// Token returns the current bearer token, refreshing it first when it
// is within RefreshBeforeExpiry of its exp claim or when a prior 401
// flagged it revoked. Token satisfies the TokenSource interface.
func (s *RefreshingTokenSource) Token(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.needsRefresh() {
		if err := s.refresh(); err != nil {
			return "", err
		}
	}
	return s.token, nil
}

// MarkRevoked flags the cached token so the next Token call reloads it.
// The Client calls this when the gateway returns 401, the §25.4
// revocation-detection path.
func (s *RefreshingTokenSource) MarkRevoked() {
	s.mu.Lock()
	s.revoked = true
	s.mu.Unlock()
}

// needsRefresh reports whether the cached token must be reloaded. The
// caller holds s.mu.
func (s *RefreshingTokenSource) needsRefresh() bool {
	if s.token == "" || s.revoked {
		return true
	}
	if s.expiry.IsZero() {
		return false
	}
	return !s.now().Before(s.expiry.Add(-s.refreshBeforeExpiry))
}

// refresh reloads the token from the loader, parses its exp claim, and
// records the outcome. The caller holds s.mu (except the constructor,
// which runs before the source is shared).
func (s *RefreshingTokenSource) refresh() error {
	tok, err := s.loader()
	if err != nil {
		s.metrics.RefreshDone("failure")
		return fmt.Errorf("refreshing token source: load: %w", err)
	}
	exp, err := parseTokenExpiry(tok)
	if err != nil {
		s.metrics.RefreshDone("failure")
		return fmt.Errorf("refreshing token source: %w", err)
	}
	s.token = tok
	s.expiry = exp
	s.revoked = false
	s.metrics.RefreshDone("success")
	return nil
}

// parseTokenExpiry decodes the unverified JWT payload segment and
// returns the exp claim as a time. The token comes from the trusted
// projected-ServiceAccount-token volume, so the signature is not
// re-verified here; only the expiry is read to schedule refresh. A
// token without an exp claim returns the zero time (treated as
// non-expiring for refresh-scheduling).
func parseTokenExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, errors.New("malformed token: want a JWT with at least two segments")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("decode token payload: %w", err)
	}
	var claims struct {
		Exp *int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("decode token claims: %w", err)
	}
	if claims.Exp == nil {
		return time.Time{}, nil
	}
	return time.Unix(*claims.Exp, 0).UTC(), nil
}

// compile-time guard: RefreshingTokenSource is a TokenSource.
var _ TokenSource = (*RefreshingTokenSource)(nil)
