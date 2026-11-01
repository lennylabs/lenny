// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
)

// ErrLennyWrittenRefWithoutWriter reports that tenant deletion Phase 4
// found a pool of the tenant, active or soft-deleted, that carries a
// Lenny-written reference while no credential Secret writer is configured.
// Deleting the rows would remove the only record that names the Secret, so
// the phase fails and the deletion job retries it.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 6.
var ErrLennyWrittenRefWithoutWriter = errors.New("tenant carries a Lenny-written credential Secret reference and no credential Secret writer is configured")

// TenantCredentialPoolEraser returns the tenant deletion Phase 4 step for
// the CredentialPoolStore and the tenant's Lenny-written credential
// Secrets. It returns the number of pool rows deleted.
//
// With a writer, the step deletes the tenant's pool rows and then issues one
// delete-collection of every Secret whose lenny.dev/tenant-hash label
// matches the tenant, which also reaches an orphan no stored reference
// names. A failure of either call is returned, so Phase 4 stops and is
// retried from the start; the retried row delete finds no rows and the
// delete-collection runs again.
//
// With a nil writer, the step reads every pool of the tenant, soft-deleted
// ones included, and fails while any of them carries a Lenny-written
// reference in credNS. A tenant with none has its rows deleted and no
// delete-collection runs.
//
// spec: §12.8 (Compliance Interfaces), §4.9 (Credential Storage Topology) —
// Credential values submitted through the admin API rules 3, 5, and 6.
func TenantCredentialPoolEraser(pools credentialpoolstore.Store, writer CredentialSecretWriter, credNS string) func(ctx context.Context, tenant string) (int, error) {
	if writer == nil {
		return eraseWithoutWriter(pools, credential.ResolveNamespace(credNS))
	}
	return func(ctx context.Context, tenant string) (int, error) {
		n, err := pools.DeleteByTenant(ctx, tenant)
		if err != nil {
			return n, fmt.Errorf("delete credential pool rows: %w", err)
		}
		if err := writer.DeleteTenant(ctx, tenant); err != nil {
			return n, fmt.Errorf("delete Lenny-written credential Secrets: %w", err)
		}
		return n, nil
	}
}

// eraseWithoutWriter is the rule 6 tenant-deletion step for a gateway with
// no credential Secret writer.
func eraseWithoutWriter(pools credentialpoolstore.Store, credNS string) func(ctx context.Context, tenant string) (int, error) {
	return func(ctx context.Context, tenant string) (int, error) {
		if err := refuseLennyWrittenRefs(ctx, pools, tenant, credNS); err != nil {
			return 0, err
		}
		n, err := pools.DeleteByTenant(ctx, tenant)
		if err != nil {
			return n, fmt.Errorf("delete credential pool rows: %w", err)
		}
		return n, nil
	}
}

// refuseLennyWrittenRefs returns ErrLennyWrittenRefWithoutWriter, naming the
// pool, when any pool of the tenant, soft-deleted pools included, carries a
// reference whose Secret name is Lenny-written. A reference ParseSecretRef
// refuses names no Secret in credNS, so the gateway cannot have written it.
func refuseLennyWrittenRefs(ctx context.Context, pools credentialpoolstore.Store, tenant, credNS string) error {
	rows, err := pools.List(ctx, tenant, credentialpoolstore.ListFilter{IncludeDeleted: true})
	if err != nil {
		return fmt.Errorf("list credential pools: %w", err)
	}
	for _, p := range rows {
		for _, c := range p.Credentials {
			name, err := credential.ParseSecretRef(c.SecretRef, credNS)
			if err == nil && credential.IsManaged(name) {
				return fmt.Errorf("pool %q: %w", p.Name, ErrLennyWrittenRefWithoutWriter)
			}
		}
	}
	return nil
}
