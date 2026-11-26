// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/idempotency/idempotencymw"
	idempgstore "github.com/lennylabs/lenny/pkg/idempotency/idempotencymw/pgstore"
	"github.com/lennylabs/lenny/pkg/storage/erasure"
)

// idempotencyRecordName is the §12.8 erasure store name of the idempotency
// records on the user-erasure and tenant-deletion paths.
const idempotencyRecordName = "idempotency_records"

// idempotencyRecordStore is the §11.5 idempotency record store the gateway
// wires: the Store the REST middleware and the MCP hook drive, and the
// deletions the §12.8 erasers run. *idempotencymw.MemoryStore and
// *pgstore.Store satisfy it.
type idempotencyRecordStore interface {
	idempotencymw.Store
	DeleteByUser(ctx context.Context, tenantID, subject string) (int, error)
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

// idempotencyStore returns the gateway's one idempotency record store,
// building it on first use: the Postgres store under --postgres-dsn, and
// the in-memory store otherwise. The HTTP surface and the erasure wiring
// are built in different steps, and each reads the store here, so both
// address the same records whichever step runs first.
func (w *gatewayWiring) idempotencyStore() idempotencyRecordStore {
	if w.idempotencyRecords == nil {
		if w.pgPool != nil {
			w.idempotencyRecords = idempgstore.New(w.pgPool)
		} else {
			w.idempotencyRecords = idempotencymw.NewMemoryStore()
		}
	}
	return w.idempotencyRecords
}

// idempotencyUserEraser is the user-erasure entry that deletes the erased
// user's idempotency records, under every bound session, before the
// SessionStore entry.
//
// spec: §12.8 (Compliance Interfaces)
func (w *gatewayWiring) idempotencyUserEraser() erasure.Eraser {
	return erasure.Eraser{Name: idempotencyRecordName, DeleteByUser: w.idempotencyStore().DeleteByUser}
}

// idempotencyTenantEraser is the tenant-deletion Phase 4 entry that
// deletes the tenant's idempotency records.
//
// spec: §12.8 (Compliance Interfaces)
func (w *gatewayWiring) idempotencyTenantEraser() namedTenantEraser {
	return namedTenantEraser{idempotencyRecordName, w.idempotencyStore().DeleteByTenant}
}

// idempotencyPrincipal returns the record scope of a request that carries
// an idempotency key: the tenant, the subject, and the bound session of the
// principal the auth middleware attached to the request context. The REST
// idempotency middleware and the MCP idempotency hook both resolve the
// scope through it, so a key sent over either path is filed under one
// scope. The subject is the principal's Subject, which is the owner the
// session surfaces give a session the request creates. A request with no
// principal resolves to an empty scope, which both callers refuse.
//
// spec: §11.5 (Idempotency)
func idempotencyPrincipal(r *http.Request) (tenantID, subject, sessionID string) {
	p, ok := authmw.FromContext(r.Context())
	if !ok {
		return "", "", ""
	}
	return p.TenantID, p.Subject, p.SessionID
}
