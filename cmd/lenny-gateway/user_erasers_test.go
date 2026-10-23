// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
)

// eraseCalls records the erasure calls the user-path doubles receive.
type eraseCalls struct{ calls []string }

func (c *eraseCalls) add(store, tenantID, scope string) {
	c.calls = append(c.calls, store+":"+tenantID+":"+scope)
}

type recMirror struct {
	coordlease.Store
	rec *eraseCalls
}

func (r recMirror) DeleteByUser(_ context.Context, tenantID, _ string, sessionIDs []string) error {
	r.rec.add("coordination_mirror", tenantID, strings.Join(sessionIDs, "+"))
	return nil
}

type recManifests struct {
	partialmanifeststore.Store
	rec *eraseCalls
}

func (r recManifests) DeleteByUser(_ context.Context, tenantID, _ string, sessionIDs []string) error {
	r.rec.add("checkpoint_manifests", tenantID, strings.Join(sessionIDs, "+"))
	return nil
}

type recRetention struct {
	checkpointretention.Store
	rec *eraseCalls
}

func (r recRetention) DeleteByUser(_ context.Context, tenantID, _ string, sessionIDs []string) error {
	r.rec.add("checkpoint_retention", tenantID, strings.Join(sessionIDs, "+"))
	return nil
}

type recConnectorCreds struct {
	connectorcredstore.Store
	rec *eraseCalls
}

func (r recConnectorCreds) DeleteByUser(_ context.Context, tenantID, userID string) (int, error) {
	r.rec.add("connector_tokens", tenantID, userID)
	return 1, nil
}

type recCredentials struct {
	credentialstore.Store
	rec *eraseCalls
}

func (r recCredentials) DeleteByUser(_ context.Context, tenantID, userID string) (int, error) {
	r.rec.add("registered_credentials", tenantID, userID)
	return 2, nil
}

// TestUserErasureReachesEveryWiredStore_spec_12_8 runs user erasure over
// the gateway's user-path builders with recording doubles and asserts the
// checkpoint and coordination-mirror adapters run once per listed session
// with that session alone, both TokenStore stores run once for the user, and
// the receipt tallies the tokens entry as their sum.
//
// spec: 12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence), 12.8 (Compliance Interfaces: Storage backends in erasure scope), 10.1.8
func TestUserErasureReachesEveryWiredStore_spec_12_8(t *testing.T) {
	rec := &eraseCalls{}
	w := inMemoryEraserWiring()
	w.coordMirror = recMirror{rec: rec}
	w.partialManifests = recManifests{rec: rec}
	w.checkpointRetention = recRetention{rec: rec}
	w.connectorCreds = recConnectorCreds{rec: rec}
	w.credentials = recCredentials{rec: rec}

	cfg := erasure.Config{
		Sessions: func(context.Context, string, string) ([]string, error) {
			return []string{"s1", "s2"}, nil
		},
		SessionScoped: w.buildSessionScopedErasers(),
		UserScoped:    w.buildUserErasers(nil, nil, credentialpoolstore.NewMemory(), nil),
	}
	if err := erasure.ValidateOrder(cfg); err != nil {
		t.Fatalf("ValidateOrder: %v", err)
	}
	res, err := erasure.New(cfg).DeleteByUser(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	want := []string{
		"coordination_mirror:acme:s1", "coordination_mirror:acme:s2",
		"checkpoint_manifests:acme:s1", "checkpoint_manifests:acme:s2",
		"checkpoint_retention:acme:s1", "checkpoint_retention:acme:s2",
		"connector_tokens:acme:alice", "registered_credentials:acme:alice",
	}
	if strings.Join(rec.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", rec.calls, want)
	}
	if res.Deleted["tokens"] != 3 {
		t.Errorf("tokens tally = %d, want 3 (connector tokens plus registered credentials)", res.Deleted["tokens"])
	}
	for _, unrun := range []string{"leases", "checkpoint_meta", "issued_tokens", "audit"} {
		if _, ok := res.Deleted[unrun]; ok {
			t.Errorf("receipt names %q, whose backend is absent in this posture", unrun)
		}
	}
}
