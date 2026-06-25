// SPDX-License-Identifier: MIT

package runtimecapoverride_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

func ptr[T any](v T) *T { return &v }

func seedRuntime(t *testing.T) runtimestore.Store {
	t.Helper()
	s := runtimestore.NewMemory()
	if err := s.Create(context.Background(), runtimestore.Runtime{
		Name: "claude-code",
		Type: runtimestore.TypeAgent,
		Capabilities: &runtimestore.RuntimeCapabilities{
			Interaction: runtimestore.InteractionMultiTurn,
			Injection:   runtimestore.InjectionCapability{Supported: true},
		},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return s
}

func TestMemoryStore_PutGetDeleteList(t *testing.T) {
	ctx := context.Background()
	os := runtimecapoverride.NewMemory()

	if _, ok, err := os.Get(ctx, "acme", "claude-code"); err != nil || ok {
		t.Fatalf("Get on empty: ok=%v err=%v", ok, err)
	}

	ov := runtimestore.CapabilityOverride{Interaction: ptr(runtimestore.InteractionOneShot)}
	if err := os.Put(ctx, "acme", "claude-code", ov); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := os.Get(ctx, "acme", "claude-code")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Interaction == nil || *got.Interaction != runtimestore.InteractionOneShot {
		t.Errorf("Get returned %+v", got)
	}

	// A second tenant is isolated.
	if _, ok, _ := os.Get(ctx, "globex", "claude-code"); ok {
		t.Error("override leaked across tenants")
	}

	list, err := os.List(ctx, "acme")
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v len=%d", err, len(list))
	}

	if err := os.Delete(ctx, "acme", "claude-code"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := os.Get(ctx, "acme", "claude-code"); ok {
		t.Error("override present after delete")
	}
	// Deleting a missing override is not an error.
	if err := os.Delete(ctx, "acme", "claude-code"); err != nil {
		t.Errorf("Delete missing: %v", err)
	}
}

func TestMemoryStore_GetClonesPointerState(t *testing.T) {
	ctx := context.Background()
	os := runtimecapoverride.NewMemory()
	_ = os.Put(ctx, "acme", "claude-code", runtimestore.CapabilityOverride{
		SDKWarmBlockingPaths: ptr([]string{"a"}),
	})
	got, _, _ := os.Get(ctx, "acme", "claude-code")
	(*got.SDKWarmBlockingPaths)[0] = "tampered"
	again, _, _ := os.Get(ctx, "acme", "claude-code")
	if (*again.SDKWarmBlockingPaths)[0] != "a" {
		t.Error("store shares slice state with caller")
	}
}

// spec: §5.1 line 49 — ResolveForTenant overlays the tenant override on
// top of the resolved platform-default runtime.
func TestResolveForTenant_OverlaysOverride_spec_5_1_49(t *testing.T) {
	ctx := context.Background()
	rs := seedRuntime(t)
	os := runtimecapoverride.NewMemory()
	_ = os.Put(ctx, "acme", "claude-code", runtimestore.CapabilityOverride{
		InjectionSupported: ptr(false),
	})

	// acme sees injection disabled.
	rt, err := runtimecapoverride.ResolveForTenant(ctx, rs, os, "acme", "claude-code")
	if err != nil {
		t.Fatalf("ResolveForTenant acme: %v", err)
	}
	if rt.InjectionSupported() {
		t.Error("acme override (injection off) not applied")
	}

	// globex (no override) sees the platform default (injection on).
	rt2, err := runtimecapoverride.ResolveForTenant(ctx, rs, os, "globex", "claude-code")
	if err != nil {
		t.Fatalf("ResolveForTenant globex: %v", err)
	}
	if !rt2.InjectionSupported() {
		t.Error("globex should inherit the platform default (injection on)")
	}
}

func TestResolveForTenant_NilStoreOrEmptyTenant(t *testing.T) {
	ctx := context.Background()
	rs := seedRuntime(t)
	// Nil override store: plain resolve.
	rt, err := runtimecapoverride.ResolveForTenant(ctx, rs, nil, "acme", "claude-code")
	if err != nil || !rt.InjectionSupported() {
		t.Fatalf("nil store: err=%v injection=%v", err, rt.InjectionSupported())
	}
	// Empty tenant: plain resolve even with a populated store.
	os := runtimecapoverride.NewMemory()
	_ = os.Put(ctx, "acme", "claude-code", runtimestore.CapabilityOverride{InjectionSupported: ptr(false)})
	rt2, err := runtimecapoverride.ResolveForTenant(ctx, rs, os, "", "claude-code")
	if err != nil || !rt2.InjectionSupported() {
		t.Fatalf("empty tenant: err=%v injection=%v", err, rt2.InjectionSupported())
	}
}

func TestResolveForTenant_UnknownRuntimePropagatesError(t *testing.T) {
	ctx := context.Background()
	rs := seedRuntime(t)
	os := runtimecapoverride.NewMemory()
	if _, err := runtimecapoverride.ResolveForTenant(ctx, rs, os, "acme", "nope"); err == nil {
		t.Error("expected an error resolving an unknown runtime")
	}
}

type erroringStore struct{ runtimecapoverride.Store }

func (erroringStore) Get(context.Context, string, string) (runtimestore.CapabilityOverride, bool, error) {
	return runtimestore.CapabilityOverride{}, false, errors.New("db down")
}

// spec: §5.1 line 49 — F-5.1.20: a non-not-found override-store read
// error propagates from ResolveForTenant rather than being swallowed, so
// the injection gate can fail closed on the tenant-narrowing path instead
// of admitting injection against the un-overlaid base runtime. The
// returned runtime is the zero value on a propagated error.
func TestResolveForTenant_OverrideStoreErrorPropagates_spec_5_1_49(t *testing.T) {
	ctx := context.Background()
	rs := seedRuntime(t)
	rt, err := runtimecapoverride.ResolveForTenant(ctx, rs, erroringStore{}, "acme", "claude-code")
	if err == nil {
		t.Fatal("expected the transient override-store read error to propagate")
	}
	if rt.InjectionSupported() {
		t.Error("expected the zero runtime (not the un-overlaid base) on a propagated error")
	}
}

// notFoundStore reports no override (ok=false, err=nil) for every key,
// the genuine not-found case ResolveForTenant must keep degrading open.
type notFoundStore struct{ runtimecapoverride.Store }

func (notFoundStore) Get(context.Context, string, string) (runtimestore.CapabilityOverride, bool, error) {
	return runtimestore.CapabilityOverride{}, false, nil
}

// spec: §5.1 line 49 — a genuine override not-found (ok=false, err=nil)
// returns the un-overlaid runtime, the only outcome that yields a usable
// runtime with err==nil. This is the degrade-open path the injection gate
// keeps when no tenant override is recorded.
func TestResolveForTenant_OverrideNotFoundDegradesOpen_spec_5_1_49(t *testing.T) {
	ctx := context.Background()
	rs := seedRuntime(t)
	rt, err := runtimecapoverride.ResolveForTenant(ctx, rs, notFoundStore{}, "acme", "claude-code")
	if err != nil {
		t.Fatalf("a genuine override not-found must not propagate an error: %v", err)
	}
	if !rt.InjectionSupported() {
		t.Error("expected the un-overlaid platform default when no override is recorded")
	}
}
