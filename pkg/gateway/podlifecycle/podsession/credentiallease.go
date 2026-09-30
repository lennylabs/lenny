// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// CredentialAssigner mints a session's §4.9 credential leases. The
// gateway's credassign.Service satisfies it; binder tests substitute a
// fake. AssignProto leases a credential from the named pool to the
// session, records the lease in the gateway's credential-lease store,
// caches the upstream credential for the §4.9 LLM proxy, and returns
// the wire-form lease the adapter materializes into the runtime
// credential file.
type CredentialAssigner interface {
	// AssignProto leases a credential from poolName to sessionID and
	// returns the wire-form CredentialLease. spiffeURI is the issuing
	// pod's SPIFFE identity for proxy-mode SPIFFE-binding; an empty value
	// disables binding. tenantID is recorded on the lease so the §4.9
	// LLM proxy can attribute proxy-extracted usage to the right tenant
	// (spec: §4.9).
	AssignProto(poolName, sessionID, spiffeURI, tenantID string) (*adapterv1.CredentialLease, error)
	// ReleaseSession releases every §4.9 credential lease the session
	// holds back to its pool. It is the §7.1 step 23 teardown the binder
	// runs when a session's pod is released, so a completed session's
	// pool slots are returned rather than leaking. A session with no
	// leases is a no-op. spec: §7.1.
	ReleaseSession(sessionID string)
	// Release releases the one §4.9 credential lease leaseID names back to
	// its pool. A failed bind attempt calls it for each lease it minted, so
	// the release never reaches a lease a successor attempt for the same
	// session minted. An unknown lease is a no-op. spec: §7.1, §4.9.
	Release(leaseID string)
}

// UserCredentialAssigner materializes a session's §4.9 user-source
// credential leases (the Pre-Authorized Credential Flow). The gateway's
// usercreds.Materializer satisfies it; binder tests substitute a fake.
// MintProto resolves the user's registered credential for the provider
// into a proxy-mode lease, records it in the shared credential-lease
// store, caches the upstream secret for the §4.9 LLM proxy, and returns
// the wire-form lease the adapter materializes into the runtime credential
// file. User leases share the lease store the pool assigner uses, so the
// pool assigner's ReleaseSession releases them at teardown.
//
// spec: §4.9.
type UserCredentialAssigner interface {
	MintProto(ctx context.Context, tenantID, userID, sessionID, spiffeURI, provider string) (*adapterv1.CredentialLease, error)
}

// CredentialAssignmentError reports that a §4.9 credential lease
// assignment failed during Bind for a specific provider/pool, after the
// §4.9 pre-claim availability check had already passed. The gateway
// maps it to the §4.9 race: it releases the claimed pod,
// increments lenny_credential_preclaim_mismatch_total{pool,provider},
// and returns CREDENTIAL_POOL_EXHAUSTED to the client.
type CredentialAssignmentError struct {
	Provider string
	Pool     string
	Err      error
}

func (e *CredentialAssignmentError) Error() string {
	return fmt.Sprintf("podsession: lease %s credential from pool %s: %v", e.Provider, e.Pool, e.Err)
}

func (e *CredentialAssignmentError) Unwrap() error { return e.Err }

// assignCredentials mints the session's §4.9 credential leases and
// pushes them to the pod via the adapter's AssignCredentials RPC, the
// fourth §4.7 session-assignment RPC (after RunSetup, before
// StartSession). It mints one lease per provider named in
// req.CredentialPools, leasing from the pool the caller resolved for
// that provider. It is a no-op when the binder has no credential
// service or the request names no pools, so a session that needs no
// upstream LLM credentials, or a deployment with no credential pools,
// assigns nothing.
//
// The minted leases carry credential material; per §4.7 item 6 the
// payload is excluded from access logs and telemetry. bindAttempt is the
// calling attempt's §4.7.1 token, carried on the AssignCredentials request.
// It returns the identifiers of the leases it minted, on failure as well as
// on success, so a failed attempt releases exactly its own leases.
func (b *Binder) assignCredentials(ctx context.Context, cl *adapterclient.Client, req BindRequest, bindAttempt string) ([]string, error) {
	hasPool := b.Credentials != nil && len(req.CredentialPools) > 0
	hasUser := b.UserCredentials != nil && len(req.UserCredentialProviders) > 0
	if !hasPool && !hasUser {
		return nil, nil
	}
	leases := make(map[string]*adapterv1.CredentialLease, len(req.CredentialPools)+len(req.UserCredentialProviders))
	var minted []string
	if hasPool {
		for provider, pool := range req.CredentialPools {
			lease, err := b.Credentials.AssignProto(pool, req.SessionID, req.PodSpiffeURI, req.TenantID)
			if err != nil {
				// The §4.9 pre-claim check (CredentialRouter) passed for this
				// provider, yet the assignment failed — the race at §4.9. Surface a typed error so the caller can release the pod,
				// increment lenny_credential_preclaim_mismatch_total, and return
				// CREDENTIAL_POOL_EXHAUSTED.
				return minted, &CredentialAssignmentError{Provider: provider, Pool: pool, Err: err}
			}
			minted = append(minted, lease.GetLeaseId())
			// The §4.7 AssignCredentials leases map is keyed by provider, and
			// the adapter writes each runtime credential-file entry under the
			// lease's own Provider field. Stamp it from the resolved provider
			// so both agree on the provider the binder leased for.
			lease.Provider = provider
			leases[provider] = lease
		}
	}
	if hasUser {
		// spec: §4.9 — for each provider the pre-claim
		// resolved to the user source, materialize a proxy-mode lease from
		// the user's registered credential. The lease shares the credential-
		// lease store the pool path uses, so the pod sees a single
		// AssignCredentials set and teardown releases both alike.
		for _, provider := range req.UserCredentialProviders {
			lease, err := b.UserCredentials.MintProto(ctx, req.TenantID, req.UserID, req.SessionID, req.PodSpiffeURI, provider)
			if err != nil {
				return minted, &CredentialAssignmentError{Provider: provider, Pool: "user", Err: err}
			}
			minted = append(minted, lease.GetLeaseId())
			lease.Provider = provider
			leases[provider] = lease
		}
	}
	return minted, cl.AssignCredentials(ctx, req.SessionID, leases, bindAttempt)
}

// releaseCredentials returns the session's §4.9 credential leases to
// their pool at teardown (§7.1 step 23). It is a no-op when the binder
// has no credential service, mirroring assignCredentials so a deployment
// without credential pools tears down cleanly.
func (b *Binder) releaseCredentials(sessionID string) {
	if b.Credentials == nil {
		return
	}
	b.Credentials.ReleaseSession(sessionID)
}
