// SPDX-License-Identifier: MIT

// Package introspection implements the §10.6 line 661 real-time group
// check: when a tenant sets identityProvider.introspectionEnabled, the
// gateway calls the OIDC provider's RFC 7662 token-introspection
// endpoint on the auth hot path and uses the group set the provider
// reports rather than the (possibly stale) groups claim carried in the
// JWT. This catches LDAP/AD group-membership changes that occurred after
// the JWT was minted, at the documented latency cost.
//
// The latency cost is bounded by a short-TTL cache: the group set for a
// (tenant, token) pair is reused until the per-tenant cache TTL expires,
// so a burst of requests from the same caller triggers at most one
// introspection round-trip per window. The per-tenant introspection
// configuration is itself cached for a fixed window so a tenant that has
// not enabled introspection does not pay a config read on every request.
//
// spec: §10.6 line 661.
package introspection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Config is a tenant's resolved §10.6 real-time group-check
// configuration. A zero Config (Enabled false) means the tenant keeps
// JWT-claim groups.
type Config struct {
	// Enabled mirrors identityProvider.introspectionEnabled. When false
	// the Verifier short-circuits and the middleware keeps the JWT groups.
	Enabled bool

	// Endpoint is the RFC 7662 introspection endpoint URL. Required when
	// Enabled is true; an enabled config with an empty endpoint is a
	// misconfiguration the Verifier fails closed on.
	Endpoint string

	// ClientID and ClientSecret authenticate the gateway to the
	// introspection endpoint via HTTP Basic auth (RFC 7662 §2.1). An
	// empty ClientID skips the Authorization header for endpoints that
	// authenticate by other means.
	ClientID     string
	ClientSecret string

	// CacheTTL bounds how long an introspection result is reused for a
	// (tenant, token) pair. Zero applies DefaultCacheTTL.
	CacheTTL time.Duration

	// GroupsClaim names the introspection-response field carrying the
	// group set. Zero applies DefaultGroupsClaim ("groups").
	GroupsClaim string
}

// DefaultCacheTTL is the introspection-result cache window applied when a
// tenant config leaves CacheTTL zero. It keeps the real-time check fresh
// while amortizing the latency cost across a request burst.
const DefaultCacheTTL = 30 * time.Second

// DefaultGroupsClaim is the introspection-response field the Verifier
// reads the group set from when a tenant config leaves GroupsClaim empty.
const DefaultGroupsClaim = "groups"

// configCacheTTL bounds how long a tenant's resolved Config is reused
// before the ConfigSource is consulted again. It keeps the per-request
// tenant read off the auth hot path for the common case (introspection
// disabled) without making an enable/disable flip take effect only on
// process restart.
const configCacheTTL = 30 * time.Second

// ConfigSource resolves a tenant's §10.6 introspection configuration.
// The gateway backs it with the tenant store's identityProvider record.
type ConfigSource interface {
	// IntrospectionConfig returns the introspection configuration for
	// tenantID. A non-nil error is a storage failure the Verifier
	// surfaces so the middleware fails closed rather than silently
	// skipping the real-time check.
	IntrospectionConfig(ctx context.Context, tenantID string) (Config, error)
}

// Verifier performs RFC 7662 token introspection and caches both the
// per-tenant configuration and the per-token group result. It satisfies
// the auth middleware's GroupIntrospector seam.
type Verifier struct {
	source ConfigSource
	client *http.Client
	clock  func() time.Time

	mu      sync.Mutex
	groups  map[string]groupEntry
	configs map[string]configEntry
}

type groupEntry struct {
	active bool
	groups []string
	expiry time.Time
}

type configEntry struct {
	cfg    Config
	expiry time.Time
}

// Option configures a Verifier.
type Option func(*Verifier)

// WithHTTPClient overrides the HTTP client used for the introspection
// call (the default carries a 5-second timeout).
func WithHTTPClient(c *http.Client) Option {
	return func(v *Verifier) {
		if c != nil {
			v.client = c
		}
	}
}

// WithClock overrides the time source, for deterministic cache tests.
func WithClock(clock func() time.Time) Option {
	return func(v *Verifier) {
		if clock != nil {
			v.clock = clock
		}
	}
}

// New returns a Verifier reading per-tenant configuration from source.
func New(source ConfigSource, opts ...Option) *Verifier {
	v := &Verifier{
		source:  source,
		client:  &http.Client{Timeout: 5 * time.Second},
		clock:   func() time.Time { return time.Now().UTC() },
		groups:  map[string]groupEntry{},
		configs: map[string]configEntry{},
	}
	for _, o := range opts {
		o(v)
	}
	return v
}

// IntrospectGroups runs the §10.6 real-time group check for a bearer
// token. The returns are:
//
//   - enabled: whether the tenant turned on introspectionEnabled. When
//     false the middleware keeps the JWT groups claim and ignores the
//     other returns.
//   - active: the RFC 7662 token-active verdict (only meaningful when
//     enabled). A false verdict means the provider considers the token
//     inactive (revoked/expired upstream); the middleware rejects it.
//   - groups: the authoritative group set the provider reports (only
//     meaningful when enabled and active).
//   - err: a config-read, misconfiguration, or transport failure. When
//     enabled is true the middleware fails closed on a non-nil err
//     rather than honoring the JWT groups, because the operator opted
//     into real-time checks for a security reason.
//
// spec: §10.6 line 661.
func (v *Verifier) IntrospectGroups(ctx context.Context, tenantID, token string) (enabled, active bool, groups []string, err error) {
	cfg, cerr := v.config(ctx, tenantID)
	if cerr != nil {
		return false, false, nil, cerr
	}
	if !cfg.Enabled {
		return false, false, nil, nil
	}
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return true, false, nil, fmt.Errorf("introspection: tenant %q enabled introspection with no endpoint configured", tenantID)
	}

	key := cacheKey(tenantID, token)
	if e, ok := v.cachedGroups(key); ok {
		return true, e.active, e.groups, nil
	}

	act, grps, ierr := v.introspect(ctx, cfg, token)
	if ierr != nil {
		return true, false, nil, ierr
	}
	v.storeGroups(key, act, grps, cfg.CacheTTL)
	return true, act, grps, nil
}

// config resolves a tenant's introspection configuration, caching the
// result for configCacheTTL so a request burst does not re-read the
// tenant store on every call.
func (v *Verifier) config(ctx context.Context, tenantID string) (Config, error) {
	v.mu.Lock()
	if e, ok := v.configs[tenantID]; ok && v.clock().Before(e.expiry) {
		cfg := e.cfg
		v.mu.Unlock()
		return cfg, nil
	}
	v.mu.Unlock()

	cfg, err := v.source.IntrospectionConfig(ctx, tenantID)
	if err != nil {
		return Config{}, err
	}

	v.mu.Lock()
	v.configs[tenantID] = configEntry{cfg: cfg, expiry: v.clock().Add(configCacheTTL)}
	v.mu.Unlock()
	return cfg, nil
}

func (v *Verifier) cachedGroups(key string) (groupEntry, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.groups[key]
	if !ok || !v.clock().Before(e.expiry) {
		return groupEntry{}, false
	}
	return e, true
}

func (v *Verifier) storeGroups(key string, active bool, groups []string, ttl time.Duration) {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.groups[key] = groupEntry{
		active: active,
		groups: append([]string(nil), groups...),
		expiry: v.clock().Add(ttl),
	}
}

// introspectResponse is the RFC 7662 introspection-response subset the
// gateway reads. `active` is the required field; the group set is read
// from the configured GroupsClaim out of the raw payload so a provider
// that names it differently is still supported.
type introspectResponse struct {
	Active bool `json:"active"`
}

func (v *Verifier) introspect(ctx context.Context, cfg Config, token string) (bool, []string, error) {
	form := url.Values{
		"token":           {token},
		"token_type_hint": {"access_token"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return false, nil, fmt.Errorf("introspection: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if cfg.ClientID != "" {
		req.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return false, nil, fmt.Errorf("introspection: endpoint unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, nil, fmt.Errorf("introspection: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return false, nil, fmt.Errorf("introspection: endpoint returned status %d", resp.StatusCode)
	}

	var ir introspectResponse
	if err := json.Unmarshal(body, &ir); err != nil {
		return false, nil, fmt.Errorf("introspection: decode response: %w", err)
	}
	if !ir.Active {
		// An inactive token carries no authoritative group set.
		return false, nil, nil
	}

	claim := cfg.GroupsClaim
	if claim == "" {
		claim = DefaultGroupsClaim
	}
	groups, err := extractGroups(body, claim)
	if err != nil {
		return false, nil, err
	}
	return true, groups, nil
}

// extractGroups reads the configured group claim out of the raw
// introspection response. It accepts a JSON array of strings (the common
// shape) and a single space- or comma-delimited string (the OAuth
// scope-style shape some providers use). A missing claim yields an empty
// group set, not an error: an active token with no group membership is a
// valid state.
func extractGroups(body []byte, claim string) ([]string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("introspection: decode response: %w", err)
	}
	v, ok := raw[claim]
	if !ok || len(v) == 0 {
		return nil, nil
	}
	var arr []string
	if err := json.Unmarshal(v, &arr); err == nil {
		return arr, nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return splitDelimited(s), nil
	}
	return nil, fmt.Errorf("introspection: group claim %q is neither a string array nor a string", claim)
}

func splitDelimited(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t'
	})
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// cacheKey derives a stable per-(tenant, token) cache key. The token is
// hashed so the raw bearer never lands in the in-memory map.
func cacheKey(tenantID, token string) string {
	sum := sha256.Sum256([]byte(token))
	return tenantID + "\x00" + hex.EncodeToString(sum[:])
}
