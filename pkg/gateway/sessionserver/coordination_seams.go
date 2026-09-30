// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// CoordinationFencer issues the §10.1 / §4.2 CoordinatorFence to a
// resumed session's pod, announcing the session's current
// coordination_generation so the pod rejects any straggler RPC from a
// prior coordinator. relinquished is true when the coordinator gave up
// leadership after exhausting its §11.3 fence retries (the lease was
// released); the resume must then be aborted so another replica takes
// over. *coordfence.Fencer satisfies it.
//
// spec: §10.1, §11.3.
type CoordinationFencer interface {
	Fence(ctx context.Context, adapter *adapterclient.Client, tenantID, sessionID string) (relinquished bool, err error)
}

// DualStoreGate reports whether this replica currently observes the
// §10.1 dual-store degraded mode (Postgres and Redis both unreachable).
// dualstore.Monitor satisfies it.
type DualStoreGate interface {
	Unavailable() bool
}
