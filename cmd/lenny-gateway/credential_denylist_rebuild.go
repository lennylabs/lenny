// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore"
)

// revokedPoolLister enumerates the pool store's revoked credentials for the
// §4.9 startup deny-list rebuild.
type revokedPoolLister interface {
	RevokedCredentials(ctx context.Context) ([]credentialpoolstore.RevokedCredential, error)
}

// revokedUserLister enumerates the token store's revoked user credentials for
// the §4.9 startup deny-list rebuild.
type revokedUserLister interface {
	RevokedCredentials(ctx context.Context) ([]credentialstore.RevokedUserCredential, error)
}

// leaseCounter reports how many active leases the store holds against a
// credential, distinguishing a definitive zero from an unanswerable query so
// the rebuild can fail closed.
type leaseCounter interface {
	LeasesByCredentialCount(ctx context.Context, key credential.CredentialKey, activeAsOf time.Time) (int, error)
}

// denyResetter commits the authoritative §4.9 deny-list Reset.
type denyResetter interface {
	Reset(keys []credential.CredentialKey)
}

// rebuildCredentialDenyListKeys builds the §4.9 startup deny-list union across
// the pool and token stores and bounds it to credentials that still have an
// active lease. Both listing queries fail closed: a non-nil error from either
// aborts the whole rebuild so no Reset is ever committed on a partial union.
// The per-key lease-existence filter also fails closed: a candidate key is
// retained when the store reports an active lease or when the count query
// errors, so a transient Postgres or KMS fault over-approximates the deny list
// rather than dropping a still-revoked credential.
//
// spec: §4.9; :1694-1695 (active-lease bound).
func rebuildCredentialDenyListKeys(ctx context.Context, pools revokedPoolLister, users revokedUserLister, leases leaseCounter, now time.Time) ([]credential.CredentialKey, error) {
	revokedPools, err := pools.RevokedCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("§4.9 deny-list rebuild (pool term): %w", err)
	}
	keys := make([]credential.CredentialKey, 0, len(revokedPools))
	for _, rc := range revokedPools {
		keys = append(keys, credential.CredentialKey{
			Source:       credential.SourcePool,
			PoolID:       rc.PoolName,
			CredentialID: rc.CredentialID,
		})
	}
	revokedUsers, err := users.RevokedCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("§4.9 deny-list rebuild (user term): %w", err)
	}
	for _, ru := range revokedUsers {
		keys = append(keys, credential.CredentialKey{
			Source:        credential.SourceUser,
			TenantID:      ru.TenantID,
			CredentialRef: ru.CredentialRef,
		})
	}
	// spec: §4.9 — retain only keys with an active lease;
	// fail closed by keeping the key when the existence query errors.
	active := keys[:0]
	for _, k := range keys {
		n, cerr := leases.LeasesByCredentialCount(ctx, k, now)
		if cerr != nil || n > 0 {
			active = append(active, k)
		}
	}
	return active, nil
}

// runCredentialDenyListRebuild runs the §4.9 credential deny-list startup
// rebuild off the synchronous startup path, committing the authoritative Reset
// and flipping credDenyRebuilt so /readyz admits the replica once its deny list
// is complete.
//
// spec: §4.9; §10.4 readiness precedence.
func (w *gatewayWiring) runCredentialDenyListRebuild() {
	rebuildCredentialDenyList(w.watchdogCtx, w.credentialPools, w.credentials, w.llmLeases,
		w.credDeny, func() { w.credDenyRebuilt.Store(true) }, clockinject.Now)
}

// rebuildCredentialDenyList re-runs both listing queries with bounded backoff
// until both succeed, commits one authoritative Reset on the complete filtered
// union, and invokes committed so /readyz admits the replica. Until the Reset
// commits the replica stays out of the §10.4 ready set (fail closed): a non-nil
// error from either listing query aborts the commit rather than resetting on a
// partial union, so a transient boot-time Postgres or KMS fault never leaves a
// retained revoked lease resolvable with a dropped store's deny entries. The
// loop exits on ctx cancellation.
//
// spec: §4.9; §10.4 readiness precedence.
func rebuildCredentialDenyList(ctx context.Context, pools revokedPoolLister, users revokedUserLister, leases leaseCounter, deny denyResetter, committed func(), now func() time.Time) {
	const (
		initialBackoff = time.Second
		maxBackoff     = 30 * time.Second
	)
	backoff := initialBackoff
	for {
		keys, err := rebuildCredentialDenyListKeys(ctx, pools, users, leases, now())
		if err == nil {
			deny.Reset(keys)
			committed()
			if len(keys) > 0 {
				log.Printf("lenny-gateway: §4.9 credential deny list rebuilt with %d revoked credential(s)", len(keys))
			}
			return
		}
		log.Printf("lenny-gateway: §4.9 credential deny-list startup rebuild failed, retrying in %s: %v", backoff, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}
