// SPDX-License-Identifier: MIT

// Package credassign is the §4.9 credential-assignment service. At
// session start it selects a credential from a pool, mints a
// CredentialLease, records the lease in the gateway's credential-lease
// store, and caches the real upstream credential in the gateway's
// credential cache so the §4.9 LLM reverse proxy can resolve and inject
// it. Releasing a lease frees the credential's session slot.
package credassign

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// ErrPoolNotFound reports that no credential pool is registered under
// the requested name.
var ErrPoolNotFound = errors.New("credassign: no such credential pool")

// Assigner is the shared §4.9 credential-assignment surface the gateway
// depends on. The in-process Service (used by lenny-token-service)
// satisfies it; the gateway-side Client (talking to lenny-token-service
// over mTLS) also satisfies it. Call sites depend on the interface so
// the gateway main can construct either implementation without
// branching at every call site.
type Assigner interface {
	// Assign mints a §4.9 credential lease from poolName for sessionID
	// and returns it. spiffeURI is the issuing pod's SPIFFE identity for
	// proxy-mode SPIFFE-binding; an empty value disables binding.
	// tenantID is recorded on the lease so downstream code paths
	// (proxy-mode usage accounting, audit) can attribute the lease to
	// its owning tenant regardless of the lease source. spec: §4.9
	// lines 1107-1141.
	Assign(poolName, sessionID, spiffeURI, tenantID string) (credential.Lease, error)
	// AssignProto mints a lease and returns its adapter wire form for
	// the §4.7 binder to push to a pod via AssignCredentials.
	AssignProto(poolName, sessionID, spiffeURI, tenantID string) (*adapterv1.CredentialLease, error)
	// ProtoLeaseByID returns the adapter wire form of a recorded lease.
	// The §4.9 renewal worker uses it to push a rotated lease to a pod
	// without re-deriving the wire form from raw fields.
	ProtoLeaseByID(leaseID string) (*adapterv1.CredentialLease, error)
	// Release frees the credential session slot a lease held and removes
	// the lease record. It is a no-op for an unknown lease id.
	Release(leaseID string)
	// ReleaseSession releases every lease the session holds back to its
	// pool. It is the §7.1 step 23 session-teardown release. A session
	// with no leases is a no-op.
	ReleaseSession(sessionID string)
	// OnAssigned registers an observer the assigner invokes after every
	// successful Assign. The §4.9 Proactive Lease Renewal worker
	// registers here so each minted lease is tracked for renewal.
	OnAssigned(fn func(LeaseAssignment))
}

// PoolCredential is one upstream credential in a §4.9 credential pool.
type PoolCredential struct {
	// ID identifies the credential within its pool.
	ID string
	// APIKey is the real upstream provider credential. It is held only
	// in memory and never leaves the gateway process. For proxy-mode
	// pools it is the secret the §4.9 LLM proxy injects upstream; for a
	// single-secret direct-mode provider (anthropic_direct) it is the
	// API key materialized into the runtime credential file.
	APIKey string
	// Materialized is the §4.9 direct-mode materializedConfig the Token
	// Service hands the runtime: the per-provider bundle of real upstream
	// credential fields (STS access keys, short-lived tokens, Vault
	// coordinates, etc.). It is consulted only for direct-mode pools. A
	// single-secret provider (anthropic_direct) may leave it empty and
	// rely on APIKey, which the assigner promotes to `{apiKey: APIKey}`.
	Materialized credential.MaterializedConfig
	// Healthy reports whether the credential is currently assignable.
	// An unhealthy credential (rate-limited, auth-failed, revoked) is
	// skipped by the assignment strategy.
	Healthy bool
}

// Pool is a §4.9 credential pool: a set of interchangeable upstream
// credentials plus the policy for leasing them.
type Pool struct {
	// Name is the pool identifier.
	Name string
	// Provider is the §4.9 upstream credential provider.
	Provider credential.Provider
	// DeliveryMode selects proxy or direct credential delivery.
	DeliveryMode credential.DeliveryMode
	// Strategy is the §4.9 credential assignment strategy.
	Strategy credential.AssignmentStrategy
	// ProxyURL and ProxyDialect configure proxy-mode delivery; they are
	// required when DeliveryMode is DeliveryProxy.
	ProxyURL     string
	ProxyDialect string
	// LeaseTTLSeconds overrides the §4.9 default lease TTL; 0 keeps it.
	LeaseTTLSeconds int
	// RenewBeforeSeconds overrides the §4.9 renew-before buffer; 0
	// keeps the default.
	RenewBeforeSeconds int
	// Credentials are the pool's upstream credentials.
	Credentials []PoolCredential
}

// poolState is a registered pool plus the mutable assignment state the
// service threads across Assign calls.
type poolState struct {
	pool      Pool
	selection credential.SelectionState
	// active counts in-use leases per credential ID, feeding the
	// least-loaded strategy.
	active map[string]int
	// revoked holds the credential IDs an emergency revocation
	// (§4.9 emergency credential revocation, line 1645) has marked
	// unselectable. A revoked credential is treated as unhealthy at
	// selection time so a replacement mint — the §4.9 line 1649 step-5
	// rotate — never re-selects the credential the operator just revoked.
	revoked map[string]bool
}

// Metrics receives §16.1 credential-pool telemetry from the assignment
// service. A nil Metrics disables emission. *gatewaymetrics.Metrics
// satisfies it.
//
// spec: §16.1 lines 51, 53, 55.
type Metrics interface {
	// IncCredentialLeaseAssignment counts a lease issued from pool for
	// provider. source is `primary` | `fallback` | `cached`.
	IncCredentialLeaseAssignment(provider, pool, source string)
	// ObserveCredentialLeaseDuration records a released lease's
	// assignment-to-release wall-clock duration.
	ObserveCredentialLeaseDuration(provider, pool string, seconds float64)
	// SetCredentialPoolUtilization sets the in-use/total credential
	// ratio for pool, in [0,1].
	SetCredentialPoolUtilization(pool string, ratio float64)
}

// Service is the §4.9 credential-assignment service. It is
// goroutine-safe.
type Service struct {
	leases credleasestore.LeaseStore
	creds  *credcache.Cache

	mu       sync.Mutex
	pools    map[string]*poolState
	now      func() time.Time
	observer func(LeaseAssignment)
	metrics  Metrics
}

// LeaseAssignment reports a §4.9 credential lease the service minted. It
// is delivered to the observer registered with OnAssigned so a caller —
// the §4.9 Proactive Lease Renewal worker — can track the lease for
// renewal without depending on the lease internals. PoolName names the
// §4.9 pool the lease was leased from, so the renewal worker can re-mint
// a replacement from the same pool.
type LeaseAssignment struct {
	// PoolName is the §4.9 credential pool the lease was minted from.
	PoolName string
	// Lease is the minted credential lease.
	Lease credential.Lease
}

// New returns a credential-assignment service that records leases in
// leases and upstream credentials in creds. leases is the
// credleasestore.LeaseStore the §4.9 LLM reverse proxy resolves a lease
// token against; passing the proxy's store lets a lease the service
// mints resolve on the proxy hot path.
func New(leases credleasestore.LeaseStore, creds *credcache.Cache) *Service {
	return &Service{
		leases: leases,
		creds:  creds,
		pools:  make(map[string]*poolState),
		now:    time.Now,
	}
}

// SetMetrics registers the §16.1 telemetry sink. A nil sink disables
// emission. SetMetrics is goroutine-safe and should be called before the
// service mints leases concurrently.
func (s *Service) SetMetrics(m Metrics) {
	s.mu.Lock()
	s.metrics = m
	s.mu.Unlock()
}

// OnAssigned registers an observer invoked with every §4.9 credential
// lease the service mints, after the lease is recorded in the lease
// store and its upstream credential cached. The §4.9 Proactive Lease
// Renewal worker registers here so each assigned lease is tracked for
// renewal. A nil observer clears any prior registration. OnAssigned is
// goroutine-safe; it must be called before the service mints leases
// concurrently. The observer runs on the Assign caller's goroutine, so
// a slow observer should hand off its own work.
func (s *Service) OnAssigned(fn func(LeaseAssignment)) {
	s.mu.Lock()
	s.observer = fn
	s.mu.Unlock()
}

// RegisterPool adds or replaces a credential pool. Replacing a pool
// resets its assignment state.
func (s *Service) RegisterPool(p Pool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pools[p.Name] = &poolState{
		pool:    p,
		active:  make(map[string]int),
		revoked: make(map[string]bool),
	}
}

// RevokeCredential marks a credential unselectable in the named pool, so
// every subsequent §4.9 lease mint from the pool skips it. It backs the
// §4.9 emergency credential revocation rotate (line 1649, step 5): once
// the gateway revokes a pool credential, a replacement lease minted from
// the same pool must come from a *different* credential, never the one
// just revoked. RevokeCredential is the in-process selection's side of
// that guarantee — the deny list stops the revoked credential reaching
// the provider; this stops the assignment service from handing it back
// out. A revocation against an unknown pool or credential is a no-op
// (the pool may not be registered on this replica). It is goroutine-safe.
//
// spec: spec/04_system-components.md lines 1645, 1649.
func (s *Service) RevokeCredential(poolName, credentialID string) {
	if poolName == "" || credentialID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, ok := s.pools[poolName]
	if !ok {
		return
	}
	ps.revoked[credentialID] = true
}

// Assign leases a credential from the named pool to a session. It
// selects a credential per the pool's §4.9 assignment strategy, mints
// the lease, records it in the credential-lease store, and caches the
// real upstream credential so the LLM proxy can inject it. spiffeURI is
// the issuing pod's SPIFFE identity for proxy-mode SPIFFE-binding; an
// empty value disables binding.
//
// Assign returns ErrPoolNotFound for an unknown pool and
// credential.ErrPoolExhausted when the pool has no assignable
// credential.
func (s *Service) Assign(poolName, sessionID, spiffeURI, tenantID string) (credential.Lease, error) {
	// spec: §16.3 line 351 — the credential.assign span (Gateway credential
	// service). The §4.9 assign surface carries no request context today, so
	// the span roots at Background and projects tenant/session/pool from the
	// call arguments rather than from correlation.From(ctx). No credential
	// material is attached (§16.4 line 376).
	_, span := tracing.NewTracer(nil).Start(context.Background(), tracing.SpanCredentialAssign)
	span.SetAttributes(
		attribute.String("tenant_id", tenantID),
		attribute.String("session_id", sessionID),
		attribute.String("credential.pool", poolName),
	)
	defer span.End()

	lease, observer, err := s.assignLocked(poolName, sessionID, spiffeURI, tenantID)
	if err != nil {
		tracing.RecordError(span, err)
		return credential.Lease{}, err
	}
	// The renewal observer runs outside s.mu: the §4.9 Proactive Lease
	// Renewal worker tracks the lease here, and holding the lock across
	// a caller-supplied callback would serialize unrelated Assign calls.
	if observer != nil {
		observer(LeaseAssignment{PoolName: poolName, Lease: lease})
	}
	return lease, nil
}

// assignLocked performs the §4.9 credential selection and lease mint
// under s.mu and returns the minted lease together with the registered
// renewal observer, which the caller invokes after releasing the lock.
func (s *Service) assignLocked(poolName, sessionID, spiffeURI, tenantID string) (credential.Lease, func(LeaseAssignment), error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ps, ok := s.pools[poolName]
	if !ok {
		return credential.Lease{}, nil, ErrPoolNotFound
	}

	candidates := make([]credential.CredentialCandidate, len(ps.pool.Credentials))
	for i, c := range ps.pool.Credentials {
		candidates[i] = credential.CredentialCandidate{
			CredentialID:   c.ID,
			ActiveSessions: ps.active[c.ID],
			// A credential the §4.9 emergency revocation marked is never
			// assignable, regardless of its declared health, so a step-5
			// replacement mint never re-selects the revoked credential.
			// spec: spec/04_system-components.md line 1649.
			Healthy: c.Healthy && !ps.revoked[c.ID],
		}
	}

	selected, nextState, err := credential.SelectCredential(ps.pool.Strategy, candidates, ps.selection)
	if err != nil {
		return credential.Lease{}, nil, err
	}

	lease, err := credential.MintLease(credential.MintRequest{
		SessionID:          sessionID,
		Provider:           ps.pool.Provider,
		Source:             credential.SourcePool,
		PoolID:             poolName,
		CredentialID:       selected.CredentialID,
		// spec: §4.9 line 1468 — record the lease's owning tenant on the
		// lease record so the LLM proxy can attribute proxy-extracted
		// usage to the right tenant without an out-of-band session
		// lookup.
		TenantID:           tenantID,
		DeliveryMode:       ps.pool.DeliveryMode,
		PoolTTLSeconds:     ps.pool.LeaseTTLSeconds,
		RenewBeforeSeconds: ps.pool.RenewBeforeSeconds,
		FallbackAllowed:    true,
		Now:                s.now(),
		SpiffeURI:          spiffeURI,
		ProxyURL:           ps.pool.ProxyURL,
		ProxyDialect:       ps.pool.ProxyDialect,
		Direct:             directConfigFor(ps.pool, selected.CredentialID),
	})
	if err != nil {
		return credential.Lease{}, nil, err
	}

	if err := s.leases.Put(lease); err != nil {
		return credential.Lease{}, nil, err
	}
	s.creds.Put(lease.CredentialKey(), credentialSecret(ps.pool, selected.CredentialID))

	ps.selection = nextState
	ps.active[selected.CredentialID]++

	// spec: §16.1 lines 51, 53 — count the lease assignment and refresh
	// the pool-utilization gauge. v1 has no §4.9 fallback chain, so every
	// pool lease is sourced from the pool's primary selection.
	if s.metrics != nil {
		s.metrics.IncCredentialLeaseAssignment(string(lease.Provider), poolName, "primary")
		s.metrics.SetCredentialPoolUtilization(poolName, poolUtilizationLocked(ps))
	}
	return lease, s.observer, nil
}

// poolUtilizationLocked returns the §16.1 credential-pool utilization:
// the fraction of the pool's credentials that currently back at least
// one active lease, in [0,1]. An empty pool reports zero. The caller
// holds s.mu.
//
// spec: §16.1 line 53 ("ratio of active leases to total pool
// credentials, in the range [0, 1]"; the CredentialPoolLow alert reads
// available credentials below 20% as utilization above 0.80).
func poolUtilizationLocked(ps *poolState) float64 {
	total := len(ps.pool.Credentials)
	if total == 0 {
		return 0
	}
	inUse := 0
	for _, c := range ps.pool.Credentials {
		if ps.active[c.ID] > 0 {
			inUse++
		}
	}
	return float64(inUse) / float64(total)
}

// Release frees the credential session slot a lease held and removes
// the lease from the credential-lease store. The cached upstream
// credential is left in place because other leases on the same pool
// credential continue to need it. Release is a no-op for an unknown
// lease ID.
func (s *Service) Release(leaseID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseLocked(leaseID)
}

// ReleaseSession releases every §4.9 credential lease the service holds
// for the session back to its pool. It is the §7.1 step 23
// "Release credential lease back to pool" teardown the gateway runs when
// a session reaches a terminal state: each of the session's leases is
// removed from the lease store and the backing credential's active-
// session counter decremented, so a completed session's pool slots are
// returned rather than leaking. Releasing a session with no leases (an
// unknown session, or one that needed no upstream credentials) is a
// no-op.
//
// spec: §7.1 line 52 (step 23). Without this the credential's `active`
// counter stays incremented after every session ends, so under sustained
// load `select.go` eventually reports `ErrPoolExhausted` for credentials
// that are in fact idle.
func (s *Service) ReleaseSession(sessionID string) {
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, lease := range s.leases.LeasesBySession([]string{sessionID}) {
		s.releaseLocked(lease.LeaseID)
	}
}

// releaseLocked frees the credential session slot a single lease held and
// removes the lease from the store. The caller holds s.mu. It is a no-op
// for an unknown lease ID.
func (s *Service) releaseLocked(leaseID string) {
	lease, ok := s.leases.GetByID(leaseID)
	if !ok {
		return
	}
	ps, poolKnown := s.pools[lease.PoolID]
	if poolKnown && ps.active[lease.CredentialID] > 0 {
		ps.active[lease.CredentialID]--
	}
	s.leases.Remove(leaseID)

	// spec: §16.1 lines 55, 53 — observe the lease's assignment-to-release
	// wall-clock duration and refresh the pool-utilization gauge.
	if s.metrics != nil {
		s.metrics.ObserveCredentialLeaseDuration(string(lease.Provider), lease.PoolID, lease.Duration(s.now()).Seconds())
		if poolKnown {
			s.metrics.SetCredentialPoolUtilization(lease.PoolID, poolUtilizationLocked(ps))
		}
	}
}

// directConfigFor returns the §4.9 direct-mode materializedConfig for a
// pool credential, or nil for a proxy-mode pool (where the lease carries
// the proxy config instead). For a single-secret provider
// (anthropic_direct) whose pool credential records only an APIKey, it
// promotes the key to `{apiKey: APIKey}`; otherwise it returns a copy of
// the credential's recorded Materialized bundle so the lease does not
// alias the pool's map. The mint path validates the result against the
// provider's Required:yes field set.
//
// spec: §4.9 lines 1246-1298.
func directConfigFor(p Pool, credentialID string) credential.MaterializedConfig {
	if p.DeliveryMode != credential.DeliveryDirect {
		return nil
	}
	for _, c := range p.Credentials {
		if c.ID != credentialID {
			continue
		}
		if len(c.Materialized) == 0 && p.Provider == credential.ProviderAnthropicDirect && c.APIKey != "" {
			return credential.MaterializedConfig{"apiKey": c.APIKey}
		}
		out := make(credential.MaterializedConfig, len(c.Materialized))
		for k, v := range c.Materialized {
			out[k] = v
		}
		return out
	}
	return nil
}

// credentialSecret returns the real upstream secret for a pool
// credential, or "" when the credential is no longer in the pool.
func credentialSecret(p Pool, credentialID string) string {
	for _, c := range p.Credentials {
		if c.ID == credentialID {
			return c.APIKey
		}
	}
	return ""
}

// UpstreamCredential returns the materialized upstream provider secret
// the service cached for a lease's backing credential. It satisfies the
// §4.3 Token Service gRPC server, which forwards the secret to the
// gateway over mTLS so the gateway's in-memory credential cache
// (`pkg/gateway/credcache`) can serve the §4.9 LLM reverse proxy. ok
// reports whether the secret is currently cached.
func (s *Service) UpstreamCredential(lease credential.Lease) (string, bool) {
	return s.creds.UpstreamCredential(lease)
}
