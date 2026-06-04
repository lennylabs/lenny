// SPDX-License-Identifier: MIT

package poolstore_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func bootstrapPool(name string, override *int) poolstore.Pool {
	return poolstore.Pool{
		Name:             name,
		RuntimeRef:       "echo",
		IsolationProfile: isolation.ProfileSandboxed,
		ExecutionMode:    runtimestore.ExecutionModeSession,
		ResourceClass:    "small",
		WarmCount:        3,
		BootstrapMinWarm: override,
	}
}

func intp(v int) *int { return &v }

// spec: §17.8.2 — the bootstrapMinWarm override round-trips through the
// store, and a nil override reads back nil.
func TestBootstrapMinWarmRoundTrip_spec_17_8_2(t *testing.T) {
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), bootstrapPool("with-override", intp(2096))); err != nil {
		t.Fatalf("Create with override: %v", err)
	}
	if err := s.Create(context.Background(), bootstrapPool("no-override", nil)); err != nil {
		t.Fatalf("Create without override: %v", err)
	}

	with, err := s.Get(context.Background(), "with-override")
	if err != nil {
		t.Fatalf("Get with-override: %v", err)
	}
	if with.BootstrapMinWarm == nil || *with.BootstrapMinWarm != 2096 {
		t.Fatalf("BootstrapMinWarm = %v, want 2096", with.BootstrapMinWarm)
	}
	none, err := s.Get(context.Background(), "no-override")
	if err != nil {
		t.Fatalf("Get no-override: %v", err)
	}
	if none.BootstrapMinWarm != nil {
		t.Fatalf("BootstrapMinWarm = %v, want nil", *none.BootstrapMinWarm)
	}
}

// spec: §17.8.2 step 3 — PUT sets/updates the override and the DELETE
// path clears it (Update setting the pointer to nil).
func TestBootstrapMinWarmSetThenClear_spec_17_8_2(t *testing.T) {
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), bootstrapPool("p", nil)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	set, err := s.Update(context.Background(), "p", func(p *poolstore.Pool) error {
		p.BootstrapMinWarm = intp(50)
		return nil
	})
	if err != nil {
		t.Fatalf("Update set: %v", err)
	}
	if set.BootstrapMinWarm == nil || *set.BootstrapMinWarm != 50 {
		t.Fatalf("after set: %v", set.BootstrapMinWarm)
	}
	cleared, err := s.Update(context.Background(), "p", func(p *poolstore.Pool) error {
		p.BootstrapMinWarm = nil
		return nil
	})
	if err != nil {
		t.Fatalf("Update clear: %v", err)
	}
	if cleared.BootstrapMinWarm != nil {
		t.Fatalf("after clear: %v want nil", *cleared.BootstrapMinWarm)
	}
}

// spec: §17.8.2 — a negative override is rejected on Create and Update.
func TestBootstrapMinWarmRejectsNegative_spec_17_8_2(t *testing.T) {
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), bootstrapPool("neg", intp(-1))); err == nil {
		t.Fatalf("Create with negative override must fail")
	}
	if err := s.Create(context.Background(), bootstrapPool("ok", intp(1))); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Update(context.Background(), "ok", func(p *poolstore.Pool) error {
		p.BootstrapMinWarm = intp(-5)
		return nil
	}); err == nil {
		t.Fatalf("Update to negative override must fail")
	}
}

// The Memory store must not share the override pointer with callers, so
// a caller mutating a returned pointer cannot corrupt the stored row.
func TestBootstrapMinWarmPointerIsolation(t *testing.T) {
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), bootstrapPool("p", intp(10))); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := s.Get(context.Background(), "p")
	*got.BootstrapMinWarm = 9999 // mutate the caller's copy
	again, _ := s.Get(context.Background(), "p")
	if *again.BootstrapMinWarm != 10 {
		t.Fatalf("stored override corrupted: %d", *again.BootstrapMinWarm)
	}
}
