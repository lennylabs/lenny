// SPDX-License-Identifier: MIT

package main

import (
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/semanticcache"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
)

// §12.1 mandatory-erasure contract, compile-enforced at the gateway
// binary boundary. §12.1 line 5 requires every store role interface to
// expose DeleteByUser and DeleteByTenant "enforced at compile time by Go
// interface satisfaction"; these assertions are that enforcement made
// concrete against the single named contract in pkg/gateway/erasure.
// A store interface that loses either erasure method stops satisfying
// the contract here, so the gateway binary fails to compile — the
// "cannot be wired into the platform" guarantee §12.1 / §12.8 promise.
//
// The pluggable roles (MemoryStore, SemanticCache), which a deployer may
// replace with a custom backend, satisfy erasure.Eraser exactly (the
// §9.4 / §12.1 error-returning signature). The first-party Postgres
// stores satisfy erasure.CountingEraser, the superset that additionally
// returns the deleted-row count for the §12.8 erasure receipt.
//
// EvictionStateStore is the documented exception: its DeleteByUser
// carries the user's session-id list because eviction rows are
// session-keyed (§12.8 step 9), so it is compile-checked against its own
// evictionstatestore.Store interface rather than this shared contract.
var (
	_ erasure.CountingEraser = sessionstore.Store(nil)
	_ erasure.CountingEraser = interactionstore.Store(nil)
	_ erasure.CountingEraser = quotastore.QuotaStore(nil)
	_ erasure.CountingEraser = auditstore.EventStore(nil)
	_ erasure.CountingEraser = billingstore.Store(nil)
	_ erasure.CountingEraser = evalstore.Store(nil)
	_ erasure.CountingEraser = leasestore.LeaseStore(nil)

	_ erasure.StoreEraser = memorystore.Store(nil)
	_ erasure.StoreEraser = semanticcache.Store(nil)
)
