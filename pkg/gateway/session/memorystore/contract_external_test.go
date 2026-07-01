// SPDX-License-Identifier: MIT

package memorystore_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore/memorystoretest"
)

// TestMemoryStoreTenantIsolation is the §9.4 line 204 named integration
// test. It runs the published contract helper against the default
// in-process backend. The Postgres default backend runs the same helper
// in the tier-2 component suite, and the gateway runs the erasure half of
// the contract against the wired backend at startup via
// memorystore.ValidateMemoryStoreErasure. spec: §9.4 line 204.
func TestMemoryStoreTenantIsolation(t *testing.T) {
	memorystoretest.ValidateMemoryStoreIsolation(t, memorystore.NewInMemory(0, nil))
}
