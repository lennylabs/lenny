// SPDX-License-Identifier: MIT

package auditstore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/auditstore/auditbatch"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
)

// AppendBatch is the §12.3 line 81 batched-insert flush callback for the
// opt-in T2 audit batching buffer (auditbatch.FlushFunc). It groups the
// buffered items by tenant chain and seals each group under one
// per-tenant advisory lock in a single transaction, reusing the same
// prev_hash chain construction as the synchronous Append: sealAndInsert
// reads the chain tail on each call, so iterating it within one locked
// transaction chains the batch correctly. The write runs on the §12.3
// line 79 dedicated audit sync write pool when one is wired.
//
// A whole tenant group fails atomically (one transaction): the buffer
// reports the loss and drops the batch, the accepted T2 data-loss
// tradeoff of batching (§12.3 lines 81-83).
//
// spec: §12.3 line 81 (T2 batched inserts); §11.7 item 3 (per-tenant
// advisory lock).
func (s *Store) AppendBatch(ctx context.Context, items []auditbatch.Item) error {
	if len(items) == 0 {
		return nil
	}
	// Preserve enqueue order within a tenant chain.
	byTenant := map[string][]auditbatch.Item{}
	var order []string
	for _, it := range items {
		if _, seen := byTenant[it.TenantID]; !seen {
			order = append(order, it.TenantID)
		}
		byTenant[it.TenantID] = append(byTenant[it.TenantID], it)
	}
	cfg := s.lockCfg.withDefaults()
	for _, tenantID := range order {
		pool, err := s.writeShard(ctx, tenantID)
		if err != nil {
			return err
		}
		group := byTenant[tenantID]
		err = pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
			if err := acquireAuditLock(ctx, tx, tenantID, cfg, s.metrics); err != nil {
				return err
			}
			for _, it := range group {
				if _, err := sealAndInsert(ctx, tx, tenantID, it.EventType, it.Payload, it.At); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("auditstore: append batch for tenant %s (%d items): %w", tenantID, len(group), err)
		}
	}
	return nil
}
