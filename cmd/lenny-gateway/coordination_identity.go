// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
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

// coordinationHolder returns the value this replica writes as its
// REG-COORDLEASE holder and as the coordinator_replica of its REG-COORDMIRROR
// rows, and compares against the holder it reads. With a lease store it is
// the §10.1.1 composite `<replica identity>.<process token>@<address>`, and an
// empty or invalid advertise address refuses startup, because a peer replica
// could not reach this replica's forward listener. Without a lease store no
// peer reads the value, so it is the composite when an advertise address is
// set and the bare replica identity otherwise. It is computed once per
// process.
//
// The ops event Source, billing, service.instance.id, and the §25.3 nonce
// checkpoint keep replicaID.
//
// spec: §10.1.1 (Stateless Replicas and Per-Session Coordination) Replica
// address
func coordinationHolder(replicaID, advertiseAddr string, leaseStore bool) (string, error) {
	if advertiseAddr == "" {
		if leaseStore {
			return "", errors.New("§10.1.1 Replica address: --holder-forward-advertise-addr (LENNY_HOLDER_FORWARD_ADVERTISE_ADDR) is required when a coordination lease store (Redis) is configured")
		}
		return replicaID, nil
	}
	holder, err := sessionserver.HolderValue(replicaID, advertiseAddr)
	if err != nil {
		return "", fmt.Errorf("§10.1.1 Replica address: --holder-forward-advertise-addr: %w", err)
	}
	return holder, nil
}

// holderForwardMaterial is the gateway replica certificate material the
// CH-HOLDERFORWARD listener and client present.
type holderForwardMaterial struct {
	cert, key, ca string
}

// complete reports whether every path is set.
func (m holderForwardMaterial) complete() bool {
	return m.cert != "" && m.key != "" && m.ca != ""
}

// validateHolderForwardMaterial refuses startup outside dev mode when a
// coordination lease store is configured and any of the gateway replica
// certificate, key, or CA paths is empty, so a deployment cannot silently
// lose its forward. In dev mode a gateway without the material builds no
// forward listener and every forward is not delivered.
//
// spec: §10.3 (mTLS PKI) NET-072
func validateHolderForwardMaterial(m holderForwardMaterial, devMode, leaseStore bool) error {
	if !leaseStore || devMode || m.complete() {
		return nil
	}
	return errors.New("§10.3 NET-072: a gateway configured with a coordination lease store (Redis) requires the gateway replica certificate material (--adapter-tls-cert, --adapter-tls-key, and --adapter-ca) outside dev mode")
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
