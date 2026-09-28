// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// resolveReplicaID returns this gateway replica's §10.1 coordination
// identity: the LENNY_REPLICA_ID override, or the hostname plus a
// random suffix so two replicas sharing a host still differ.
func resolveReplicaID() string {
	if id := os.Getenv("LENNY_REPLICA_ID"); id != "" {
		return id
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "gateway"
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%x", host, b)
}

// sessionGenerationReader adapts the session store to
// coordfence.GenerationReader so the §10.1 CoordinatorFence driver reads
// (and re-reads, after a stale rejection) the session's authoritative
// §4.2 coordination_generation.
type sessionGenerationReader struct{ store sessionstore.Store }

func (r sessionGenerationReader) CoordinationGeneration(ctx context.Context, tenantID, sessionID string) (int64, error) {
	row, err := r.store.Get(ctx, tenantID, sessionID)
	if err != nil {
		return 0, err
	}
	return row.CoordinationGeneration, nil
}
