// SPDX-License-Identifier: MIT

package poolstore_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
)

// TestDeliveryModeFieldsRoundTrip_spec_4_9 pins the §4.9 credential-delivery
// combination fields (deliveryMode, spiffeBinding, and the two deployer opt-in
// acknowledgments) through the in-memory store's create, get, update, and list
// paths. A field silently dropped on write or read would make the
// pool-registration and admission layers inspect an empty combination, the
// exact stale-value defect the model carry closes.
//
// spec: §4.9 (warm-pool admin/store model carries the credential-delivery
// combination).
func TestDeliveryModeFieldsRoundTrip_spec_4_9(t *testing.T) {
	ctx := context.Background()
	m := poolstore.NewMemory()

	// A pool that sets none of the four fields reads back with the empty /
	// false defaults, so an unset pool is distinguishable from one that opted
	// in.
	if err := m.Create(ctx, poolstore.Pool{Name: "bare", RuntimeRef: "rt"}); err != nil {
		t.Fatalf("create bare pool: %v", err)
	}
	bare, err := m.Get(ctx, "bare")
	if err != nil {
		t.Fatalf("get bare pool: %v", err)
	}
	if bare.DeliveryMode != "" || bare.SpiffeBinding != "" ||
		bare.AllowDirectModeStandardIsolation || bare.AllowProxyModeSpiffeBindingDisabled {
		t.Errorf("bare pool carried non-default credential-delivery fields: %+v", bare)
	}

	// A pool that sets all four fields preserves every value through create
	// and get.
	set := poolstore.Pool{
		Name:                                "set",
		RuntimeRef:                          "rt",
		DeliveryMode:                        "proxy",
		SpiffeBinding:                       "disabled",
		AllowDirectModeStandardIsolation:    true,
		AllowProxyModeSpiffeBindingDisabled: true,
	}
	if err := m.Create(ctx, set); err != nil {
		t.Fatalf("create set pool: %v", err)
	}
	got, err := m.Get(ctx, "set")
	if err != nil {
		t.Fatalf("get set pool: %v", err)
	}
	if got.DeliveryMode != "proxy" || got.SpiffeBinding != "disabled" ||
		!got.AllowDirectModeStandardIsolation || !got.AllowProxyModeSpiffeBindingDisabled {
		t.Errorf("credential-delivery fields lost on create/get: %+v", got)
	}

	// Update must persist a changed combination.
	updated, err := m.Update(ctx, "set", func(p *poolstore.Pool) error {
		p.DeliveryMode = "direct"
		p.SpiffeBinding = "enabled"
		p.AllowDirectModeStandardIsolation = false
		p.AllowProxyModeSpiffeBindingDisabled = false
		return nil
	})
	if err != nil {
		t.Fatalf("update set pool: %v", err)
	}
	if updated.DeliveryMode != "direct" || updated.SpiffeBinding != "enabled" ||
		updated.AllowDirectModeStandardIsolation || updated.AllowProxyModeSpiffeBindingDisabled {
		t.Errorf("credential-delivery fields not persisted on update: %+v", updated)
	}
	reGot, err := m.Get(ctx, "set")
	if err != nil {
		t.Fatalf("re-get set pool: %v", err)
	}
	if reGot.DeliveryMode != "direct" || reGot.SpiffeBinding != "enabled" {
		t.Errorf("updated credential-delivery fields not readable: %+v", reGot)
	}

	// List surfaces the fields on every row.
	pools, err := m.List(ctx, poolstore.ListFilter{})
	if err != nil {
		t.Fatalf("list pools: %v", err)
	}
	byName := map[string]poolstore.Pool{}
	for _, p := range pools {
		byName[p.Name] = p
	}
	if byName["set"].DeliveryMode != "direct" || byName["set"].SpiffeBinding != "enabled" {
		t.Errorf("list dropped credential-delivery fields: %+v", byName["set"])
	}
	if byName["bare"].DeliveryMode != "" || byName["bare"].SpiffeBinding != "" {
		t.Errorf("list surfaced non-default fields for the bare pool: %+v", byName["bare"])
	}
}
