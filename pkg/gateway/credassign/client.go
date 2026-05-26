// SPDX-License-Identifier: MIT

package credassign

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// ErrTokenServiceUnavailable is the sentinel a session-start path
// reads to surface §4.3 "Token Service down, retryable" to the caller.
// The session-start handler maps it to HTTP 503 with Retry-After.
//
// spec: §4.3 line 214 "New sessions that require LLM or OAuth
// credentials cannot start and fail with a retryable error, allowing
// clients to back off and retry."
var ErrTokenServiceUnavailable = errors.New("credassign: Token Service unavailable")

// Client is the §4.3 gateway-side Token Service client. It satisfies
// Assigner over an mTLS gRPC connection to the Token Service Deployment
// so the gateway never holds KMS decrypt rights and never mints leases
// in-process. After the gateway-side cutover, every code path that
// would have called the in-process §4.9 credassign.Service goes through
// Client instead.
//
// The client mirrors three pieces of state into the gateway process so
// the §4.9 LLM reverse proxy continues to serve requests without an
// extra Token Service round-trip per upstream call:
//
//   - The minted lease goes into the local credleasestore so the proxy
//     can resolve the bearer token the pod presents.
//   - The materialized upstream credential the server returned (over
//     mTLS) goes into the local credcache so the proxy can inject it
//     into the upstream LLM request.
//   - The per-lease pool binding (pool id and provider) is recorded so
//     the §4.9 Proactive Lease Renewal worker can rotate the lease and
//     ProtoLeaseByID can convert a recorded lease back to the adapter
//     wire form.
//
// Tenant identity is bound at construction time. A gateway replica
// serves exactly one tenant's credential pools (the §4.6 binder runs
// per-session and selects the tenant's pool from the session record),
// but the gRPC contract still carries the tenant id on every request
// so the Token Service can apply §4.2 row-level security.
type Client struct {
	stub     tokensv1.TokenServiceClient
	leases   credleasestore.LeaseStore
	creds    *credcache.Cache
	tenantID string

	// timeout bounds each gRPC call. The renewal worker schedules
	// renewals on a §4.9 renewBefore deadline that is many minutes
	// ahead of expiry, so a short per-call timeout still leaves
	// generous headroom.
	timeout time.Duration

	// subsystem is the §4.1 / §4.3 per-subsystem breaker the gateway
	// wraps Token Service calls in. spec: §4.3 line 211. A nil value
	// (zero-value Subsystem) admits every call: tests and dev mode
	// run without the breaker, and the breaker is wired in by the
	// gateway main package.
	subsystem *subsystem.Subsystem

	mu       sync.Mutex
	observer func(LeaseAssignment)
	// renewalPool records the pool a lease was minted from so the §4.9
	// renewal worker can re-mint a replacement after a rotation. The
	// Token Service is authoritative for the lease record; this map is
	// gateway-local because the renewal worker drives it.
	renewalPool map[string]string
}

// ClientOptions configures a Token Service client. A zero Timeout
// selects the default DefaultClientTimeout.
type ClientOptions struct {
	// Stub is the generated gRPC client bound to an mTLS connection.
	Stub tokensv1.TokenServiceClient
	// Leases is the gateway's credential-lease store. The §4.9 LLM
	// proxy resolves a presented lease token here.
	Leases credleasestore.LeaseStore
	// Creds is the gateway's upstream-credential cache the LLM proxy
	// reads on every upstream call.
	Creds *credcache.Cache
	// TenantID is the tenant the gateway replica serves. Empty
	// disables tenant validation on the wire (used in dev mode).
	TenantID string
	// Timeout bounds each gRPC call. Zero selects DefaultClientTimeout.
	Timeout time.Duration
	// Subsystem is the §4.1 / §4.3 per-subsystem breaker the gateway
	// wraps Token Service calls in. spec: §4.3 line 211. A nil value
	// keeps the breaker absent: every call is admitted (test/dev path).
	Subsystem *subsystem.Subsystem
}

// DefaultClientTimeout bounds each gateway → Token Service gRPC call.
// The Token Service is fully stateless and serves AssignCredentials in
// the low milliseconds, so a five-second budget covers a worst-case
// degraded Postgres or KMS round-trip while still failing fast on a
// dead replica so the gateway's automatic in-memory circuit breaker
// (§4.3) trips.
const DefaultClientTimeout = 5 * time.Second

// NewClient returns a Token Service client. The caller dials the gRPC
// connection with the project's mTLS material and passes the resulting
// stub.
func NewClient(opts ClientOptions) *Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultClientTimeout
	}
	return &Client{
		stub:        opts.Stub,
		leases:      opts.Leases,
		creds:       opts.Creds,
		tenantID:    opts.TenantID,
		timeout:     timeout,
		subsystem:   opts.Subsystem,
		renewalPool: make(map[string]string),
	}
}

// callTokenService runs fn through the §4.3 / §4.1 per-subsystem
// breaker. When the breaker is open it returns ErrTokenServiceUnavailable
// so the session-start path can surface §4.3's retryable error.
// transient gRPC codes (Unavailable, DeadlineExceeded) classified as
// breaker-triggering. spec: §4.3 line 211, line 214.
func (c *Client) callTokenService(ctx context.Context, fn func(context.Context) error) error {
	if c.subsystem == nil {
		return fn(ctx)
	}
	err := c.subsystem.Do(ctx, fn)
	if errors.Is(err, subsystem.ErrCircuitOpen) {
		return ErrTokenServiceUnavailable
	}
	return err
}

// OnAssigned mirrors Service.OnAssigned. The §4.9 Proactive Lease
// Renewal worker registers here so each successful AssignProto invokes
// the observer with the materialized lease and the pool it was minted
// from.
func (c *Client) OnAssigned(fn func(LeaseAssignment)) {
	c.mu.Lock()
	c.observer = fn
	c.mu.Unlock()
}

// Assign issues a §4.9 AssignCredentials RPC for the named pool and
// returns the materialized lease. It mirrors the response into the
// gateway's local lease store and credential cache so the §4.9 LLM
// proxy and the renewal worker have everything they need without
// another round-trip.
func (c *Client) Assign(poolName, sessionID, spiffeURI, tenantID string) (credential.Lease, error) {
	lease, observer, err := c.assign(poolName, sessionID, spiffeURI, tenantID)
	if err != nil {
		return credential.Lease{}, err
	}
	if observer != nil {
		observer(LeaseAssignment{PoolName: poolName, Lease: lease})
	}
	return lease, nil
}

func (c *Client) assign(poolName, sessionID, spiffeURI, tenantID string) (credential.Lease, func(LeaseAssignment), error) {
	parent, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	var (
		resp *tokensv1.AssignCredentialsResponse
		err  error
	)
	// spec: §4.9 line 1468 — when the caller supplies a per-request
	// tenantID it wins over the client's replica-wide tenantID so the
	// minted lease records the session's tenant.
	effectiveTenant := tenantID
	if effectiveTenant == "" {
		effectiveTenant = c.tenantID
	}
	// Route the gRPC through the §4.1 / §4.3 per-subsystem breaker so
	// a degraded Token Service trips the breaker open after consecutive
	// failures and the session-start path fails fast with
	// ErrTokenServiceUnavailable.
	callErr := c.callTokenService(parent, func(ctx context.Context) error {
		resp, err = c.stub.AssignCredentials(ctx, &tokensv1.AssignCredentialsRequest{
			TenantId:     effectiveTenant,
			SessionId:    sessionID,
			PodSpiffeUri: spiffeURI,
			PoolIds:      []string{poolName},
		})
		// Translate breaker-relevant transient errors so the breaker
		// counts them as failures while user errors (NotFound,
		// ResourceExhausted) do not.
		return classifyTokenServiceError(err)
	})
	if callErr != nil {
		if errors.Is(callErr, ErrTokenServiceUnavailable) {
			return credential.Lease{}, nil, callErr
		}
		// Restore the original gRPC error mapping when the call ran
		// and returned a non-transient gRPC error.
		if err != nil {
			return credential.Lease{}, nil, mapAssignError(poolName, err)
		}
	}
	if err != nil {
		return credential.Lease{}, nil, mapAssignError(poolName, err)
	}
	if len(resp.Leases) == 0 {
		return credential.Lease{}, nil, fmt.Errorf("credassign: Token Service returned empty lease set for pool %q", poolName)
	}
	if len(resp.Leases) > 1 {
		return credential.Lease{}, nil, fmt.Errorf("credassign: Token Service returned %d leases for pool %q; want exactly one", len(resp.Leases), poolName)
	}
	var protoLease *tokensv1.CredentialLease
	for _, l := range resp.Leases {
		protoLease = l
	}
	lease, err := credentialLeaseFromProto(protoLease)
	if err != nil {
		return credential.Lease{}, nil, err
	}
	if err := c.recordAssignedLease(lease, protoLease, poolName); err != nil {
		return credential.Lease{}, nil, err
	}

	c.mu.Lock()
	observer := c.observer
	c.mu.Unlock()
	return lease, observer, nil
}

// classifyTokenServiceError filters which gRPC errors should count as
// breaker-triggering. Only transient transport failures (Unavailable,
// DeadlineExceeded, ResourceExhausted of the server itself,
// Internal/Unknown) trip the breaker; logical errors (NotFound,
// InvalidArgument) report user-level conditions that do not indicate
// Token Service degradation. spec: §4.3 line 211 "automatic, in-memory
// circuit breaker".
func classifyTokenServiceError(err error) error {
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.OK,
		codes.NotFound,
		codes.InvalidArgument,
		codes.PermissionDenied,
		codes.Unauthenticated,
		codes.AlreadyExists,
		codes.FailedPrecondition,
		codes.OutOfRange,
		codes.Unimplemented:
		// User-classified errors: do not count against the breaker.
		return nil
	default:
		// Unavailable, DeadlineExceeded, Internal, Unknown,
		// ResourceExhausted-of-the-server, etc.: count as failure so a
		// degraded Token Service trips the breaker.
		return err
	}
}

// AssignProto mirrors Service.AssignProto: it issues AssignCredentials
// over the wire, mirrors the response into local state, and returns the
// gateway → pod adapter wire form of the lease.
func (c *Client) AssignProto(poolName, sessionID, spiffeURI, tenantID string) (*adapterv1.CredentialLease, error) {
	lease, err := c.Assign(poolName, sessionID, spiffeURI, tenantID)
	if err != nil {
		return nil, err
	}
	return ProtoLease(lease)
}

// ProtoLeaseByID mirrors Service.ProtoLeaseByID for the gateway-side
// renewal worker. The local credleasestore is authoritative for the
// gateway's view of the lease (mirrored by Assign and RotateCredentials).
func (c *Client) ProtoLeaseByID(leaseID string) (*adapterv1.CredentialLease, error) {
	lease, ok := c.leases.GetByID(leaseID)
	if !ok {
		return nil, fmt.Errorf("credassign: no recorded lease with id %s", leaseID)
	}
	return ProtoLease(lease)
}

// Release issues a §4.9 RevokeCredentials RPC for the lease and removes
// it from the local lease store. The credential cache entry is left in
// place because other leases on the same pool credential continue to
// need it; the cache is managed by the AssignCredentials path and the
// §4.9 emergency revocation deny-list, not by Release.
func (c *Client) Release(leaseID string) {
	lease, ok := c.leases.GetByID(leaseID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	_, err := c.stub.RevokeCredentials(ctx, &tokensv1.RevokeCredentialsRequest{
		TenantId: c.tenantID,
		LeaseId:  leaseID,
		Reason:   "session_released",
	})
	if err != nil && status.Code(err) != codes.NotFound {
		// The Token Service is the authoritative store; a transient
		// error leaves the lease alive there. The gateway-side removal
		// still happens so the local proxy stops resolving the bearer.
		// A subsequent renewal worker pass or admin tool will reconcile.
		// The §16 lenny_credential_revoke_failed_total counter (when
		// wired) labels this for alerting.
		_ = err
	}
	c.leases.Remove(leaseID)
	c.mu.Lock()
	delete(c.renewalPool, leaseID)
	if lease.Source == credential.SourcePool {
		// No further bookkeeping; the Token Service decrements the
		// pool's per-credential slot count on its side.
	}
	c.mu.Unlock()
}

// ReleaseSession releases every §4.9 credential lease the client holds
// for the session: it issues a RevokeCredentials RPC for each and drops
// the local lease entry, returning the session's pool slots. It is the
// §7.1 step 23 session-teardown lease release the gateway runs when a
// session reaches a terminal state. A session with no leases is a no-op.
//
// spec: §7.1 line 52 (step 23).
func (c *Client) ReleaseSession(sessionID string) {
	if sessionID == "" {
		return
	}
	for _, lease := range c.leases.LeasesBySession([]string{sessionID}) {
		c.Release(lease.LeaseID)
	}
}

// recordAssignedLease mirrors the §4.3 AssignCredentials response into
// the gateway's local state: the lease into the credleasestore, the
// materialized upstream credential into the credcache, and the pool
// binding into the per-lease renewal map.
func (c *Client) recordAssignedLease(lease credential.Lease, proto *tokensv1.CredentialLease, poolName string) error {
	if err := c.leases.Put(lease); err != nil {
		return fmt.Errorf("credassign: record lease %s in local store: %w", lease.LeaseID, err)
	}
	if proto.GetUpstreamCredential() != "" {
		c.creds.Put(lease.CredentialKey(), proto.GetUpstreamCredential())
	}
	c.mu.Lock()
	if poolName != "" {
		c.renewalPool[lease.LeaseID] = poolName
	}
	c.mu.Unlock()
	return nil
}

// PoolForLease reports the §4.9 pool a lease was minted from. The §4.9
// Proactive Lease Renewal worker uses it to re-mint a replacement from
// the same pool; the lease record itself carries PoolID, but a lease
// retrieved through ProtoLeaseByID may have been minted before the
// process restarted, so the renewal worker prefers the explicit binding
// where one is recorded.
func (c *Client) PoolForLease(leaseID string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pool, ok := c.renewalPool[leaseID]
	return pool, ok
}

// credentialLeaseFromProto decodes the gateway↔Token-Service wire form
// back to the in-process credential.Lease the gateway stores. Direct
// inverse of leaseToProto(WithSecret) on the server side.
func credentialLeaseFromProto(p *tokensv1.CredentialLease) (credential.Lease, error) {
	if p == nil {
		return credential.Lease{}, errors.New("credassign: nil CredentialLease on the wire")
	}
	lease := credential.Lease{
		LeaseID:         p.GetLeaseId(),
		SessionID:       p.GetSessionId(),
		Provider:        credential.Provider(p.GetProvider()),
		Source:          credential.LeaseSource(p.GetSource()),
		PoolID:          p.GetPoolId(),
		CredentialID:    p.GetCredentialId(),
		TenantID:        p.GetTenantId(),
		CredentialRef:   p.GetCredentialRef(),
		DeliveryMode:    credential.DeliveryMode(p.GetDeliveryMode()),
		IssuedAt:        p.GetIssuedAt().AsTime(),
		ExpiresAt:       p.GetExpiresAt().AsTime(),
		RenewBefore:     p.GetRenewBefore().AsTime(),
		FallbackAllowed: p.GetFallbackAllowed(),
		SpiffeURI:       p.GetSpiffeUri(),
	}
	if lease.DeliveryMode == credential.DeliveryProxy {
		lease.Proxy = &credential.ProxyConfig{
			ProxyURL:      p.GetProxyUrl(),
			ProxyDialect:  p.GetProxyDialect(),
			LeaseToken:    p.GetLeaseToken(),
			UpstreamModel: p.GetUpstreamModel(),
		}
	}
	if lease.DeliveryMode == credential.DeliveryDirect {
		// §4.9 direct-mode materializedConfig: the per-provider upstream
		// credential bundle the gateway forwards to the pod through the
		// adapter credential file. Copy so the lease does not alias the
		// proto's map.
		if mc := p.GetMaterializedConfig(); len(mc) > 0 {
			lease.Direct = make(credential.MaterializedConfig, len(mc))
			for k, v := range mc {
				lease.Direct[k] = v
			}
		}
	}
	return lease, nil
}

// mapAssignError translates a Token Service gRPC status into the same
// error sentinels the in-process credassign.Service returns. Keeping
// the surface stable lets every existing call site (the §4.7 binder, the
// §4.9 renewal worker, gateway tests) handle the cutover transparently.
func mapAssignError(poolName string, err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		return ErrPoolNotFound
	case codes.ResourceExhausted:
		return credential.ErrPoolExhausted
	default:
		return fmt.Errorf("credassign: Token Service AssignCredentials for pool %q: %w", poolName, err)
	}
}
