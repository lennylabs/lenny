// SPDX-License-Identifier: MIT

package store_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/eventbus"
	"github.com/lennylabs/lenny/pkg/podregistry"
	"github.com/lennylabs/lenny/pkg/platform/store"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// TestSharedIDTypesAreSingleDefinition asserts the §12.6 shared ID
// types resolve to the platform/store definitions through the consuming
// packages' aliases, so there is one canonical type rather than two
// independent definitions a caller must cast between. The assignments
// below compile only because the package-level names are aliases (=),
// not distinct named types.
//
// spec: §12.6 lines 369-373.
func TestSharedIDTypesAreSingleDefinition_spec_12_6_369(t *testing.T) {
	var (
		tenant  store.TenantID  = "acme"
		session store.SessionID = "sess-1"
		pod     store.PodID     = "pod-1"
		pool    store.PoolID    = "pool-1"
		cluster store.ClusterID = "cluster-1"
	)

	// storerouter and eventbus tenant ids are the same type: no cast.
	var srTenant storerouter.TenantID = tenant
	var ebTenant eventbus.TenantID = tenant
	if string(srTenant) != "acme" || string(ebTenant) != "acme" {
		t.Fatalf("tenant alias round-trip: sr=%q eb=%q", srTenant, ebTenant)
	}

	// storerouter session id is the shared type.
	var srSession storerouter.SessionID = session
	if string(srSession) != "sess-1" {
		t.Fatalf("session alias round-trip: %q", srSession)
	}

	// podregistry pod/pool/cluster ids are the shared types.
	var prPod podregistry.PodID = pod
	var prPool podregistry.PoolID = pool
	var prCluster podregistry.ClusterID = cluster
	if string(prPod) != "pod-1" || string(prPool) != "pool-1" || string(prCluster) != "cluster-1" {
		t.Fatalf("podregistry alias round-trip: pod=%q pool=%q cluster=%q", prPod, prPool, prCluster)
	}
}

// TestRedisConcernEnumSharedValues asserts the §12.6 RedisConcern enum
// is the shared definition and the storerouter aliases carry the spec's
// wire values.
//
// spec: §12.6 lines 375-389.
func TestRedisConcernEnumSharedValues_spec_12_6_375(t *testing.T) {
	cases := map[storerouter.RedisConcern]string{
		store.RedisConcernCoordination: "coordination",
		store.RedisConcernQuota:        "quota",
		store.RedisConcernCachePubSub:  "cache_pubsub",
		store.RedisConcernDelegation:   "delegation",
		store.RedisConcernSessionData:  "session_data",
	}
	for concern, want := range cases {
		if string(concern) != want {
			t.Errorf("RedisConcern %v = %q, want %q", concern, string(concern), want)
		}
	}
	// The alias and the canonical const are interchangeable.
	if storerouter.RedisConcernCachePubSub != store.RedisConcernCachePubSub {
		t.Error("storerouter.RedisConcernCachePubSub diverged from the shared definition")
	}
}

// TestStoreTypeEnumSharedValues asserts the §12.6 StoreType enum is the
// shared definition with the spec's wire values.
//
// spec: §12.6 lines 391-400.
func TestStoreTypeEnumSharedValues_spec_12_6_391(t *testing.T) {
	cases := map[storerouter.StoreType]string{
		store.StoreTypeSession: "session",
		store.StoreTypeTenant:  "tenant",
		store.StoreTypeBilling: "billing",
		store.StoreTypeAudit:   "audit",
	}
	for st, want := range cases {
		if string(st) != want {
			t.Errorf("StoreType %v = %q, want %q", st, string(st), want)
		}
	}
}

// TestSubscriptionAliasSatisfiedByEventbus asserts the eventbus
// Subscription handle satisfies the shared §12.6 Subscription interface,
// so a caller depending on store.Subscription accepts an eventbus
// subscription without a wrapper.
//
// spec: §12.6 lines 411-414.
func TestSubscriptionAliasSatisfiedByEventbus_spec_12_6_411(t *testing.T) {
	var _ store.Subscription = eventbus.Subscription(nil)
	var _ eventbus.Subscription = store.Subscription(nil)
}
