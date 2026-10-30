// SPDX-License-Identifier: MIT

package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	quotacheckpointpg "github.com/lennylabs/lenny/pkg/gateway/quota/quotacheckpoint/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotaerasure"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotafailopen"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
)

// buildQuotaEraser composes the §12.2 QuotaStore erasure ("Redis +
// Postgres") across its wired backends so the §12.8 step-6 quota erasure
// clears the whole role: the Redis token counter, the Postgres
// token_usage_checkpoint, and the in-memory fail-open accumulator the §11.2
// line-48 MAX rule restores from. It returns nil when no backend is wired,
// so the caller skips the quota step rather than wiring a no-op.
//
// spec: §12.8 step 6 (Redis + Postgres); §12.2 (QuotaStore role).
func buildQuotaEraser(counter *quotastore.Counter, pgPool *pgxpool.Pool, accum *quotafailopen.Accumulator) *quotaerasure.Composite {
	var backends []quotaerasure.Backend
	if counter != nil {
		backends = append(backends, quotaerasure.Backend{Name: "redis_counter", User: counter, Tenant: counter})
	}
	if pgPool != nil {
		cp := quotacheckpointpg.New(pgPool)
		backends = append(backends, quotaerasure.Backend{Name: "postgres_checkpoint", User: cp, Tenant: cp})
	}
	if accum != nil {
		backends = append(backends, quotaerasure.Backend{Name: "failopen_accumulator", User: accum, Tenant: accum})
	}
	if len(backends) == 0 {
		return nil
	}
	return quotaerasure.New(backends...)
}
