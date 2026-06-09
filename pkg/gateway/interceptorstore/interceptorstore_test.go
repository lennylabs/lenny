// SPDX-License-Identifier: MIT

package interceptorstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

func validInterceptor(name string) Interceptor {
	return Interceptor{
		Name:       name,
		Endpoint:   "scanner.acme.svc:9000",
		Priority:   500,
		FailPolicy: interceptor.FailClosed,
		TimeoutMs:  500,
		Phases:     []interceptor.Phase{interceptor.PhasePreDelegation, interceptor.PhasePreMessageDelivery},
	}
}

// spec: §4.8 line 1020 — external interceptors must register above the
// reserved priority ceiling; §4.8 line 1023 — none may target PreAuth.
func TestValidate_spec_4_8(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Interceptor)
		wantErr error
	}{
		{"valid", func(*Interceptor) {}, nil},
		{"priority_at_ceiling", func(ic *Interceptor) { ic.Priority = interceptor.ReservedPriorityCeiling }, interceptor.ErrInvalidPriority},
		{"priority_below_ceiling", func(ic *Interceptor) { ic.Priority = 50 }, interceptor.ErrInvalidPriority},
		{"preauth_phase", func(ic *Interceptor) { ic.Phases = []interceptor.Phase{interceptor.PhasePreAuth} }, interceptor.ErrInvalidPhase},
		{"unknown_phase", func(ic *Interceptor) { ic.Phases = []interceptor.Phase{"Nonsense"} }, nil},
		{"no_phases", func(ic *Interceptor) { ic.Phases = nil }, nil},
		{"bad_name", func(ic *Interceptor) { ic.Name = "Bad Name" }, nil},
		{"empty_endpoint", func(ic *Interceptor) { ic.Endpoint = "" }, nil},
		{"bad_fail_policy", func(ic *Interceptor) { ic.FailPolicy = "fail-sideways" }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ic := validInterceptor("scan")
			tc.mutate(&ic)
			err := Validate(ic)
			switch {
			case tc.name == "valid":
				if err != nil {
					t.Fatalf("valid interceptor rejected: %v", err)
				}
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want %v, got %v", tc.wantErr, err)
				}
			default:
				if err == nil {
					t.Fatalf("%s: expected an error, got nil", tc.name)
				}
			}
		})
	}
}

func TestApplyDefaults(t *testing.T) {
	ic := Interceptor{}
	ApplyDefaults(&ic)
	if ic.FailPolicy != interceptor.FailClosed {
		t.Fatalf("default failPolicy = %q, want fail-closed", ic.FailPolicy)
	}
	if ic.Priority != interceptor.DefaultExternalPriority {
		t.Fatalf("default priority = %d, want %d", ic.Priority, interceptor.DefaultExternalPriority)
	}
}

func TestMemoryCRUD(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	if err := m.Create(ctx, validInterceptor("scan")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.Create(ctx, validInterceptor("scan")); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate create = %v, want ErrAlreadyExists", err)
	}
	got, err := m.Get(ctx, "scan")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Version != 1 {
		t.Fatalf("initial version = %d, want 1", got.Version)
	}
	updated, err := m.Update(ctx, "scan", func(ic *Interceptor) error {
		ic.FailPolicy = interceptor.FailOpen
		return nil
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 || updated.FailPolicy != interceptor.FailOpen {
		t.Fatalf("update = %+v, want version 2 fail-open", updated)
	}
	if _, err := m.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing get = %v, want ErrNotFound", err)
	}
	list, err := m.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v len %d, want 1", err, len(list))
	}
	if err := m.Delete(ctx, "scan"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := m.Delete(ctx, "scan"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("re-delete = %v, want ErrNotFound", err)
	}
}

// spec: §8.3 line 224 (SEC-013) — the cooldown resolver reports the
// server-minted transition timestamp and the cooldown seconds recorded
// at the transition for a weakened interceptor, and reports "no
// cooldown" for a clean or unknown interceptor.
func TestCooldownResolver_spec_8_3_224(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	transition := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	weakened := validInterceptor("scan")
	weakened.FailPolicy = interceptor.FailOpen
	weakened.FailOpenTransitionAt = transition
	weakened.CooldownSecondsAtTransition = 60
	if err := m.Create(ctx, weakened); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.Create(ctx, validInterceptor("clean")); err != nil {
		t.Fatalf("create clean: %v", err)
	}
	r := NewCooldownResolver(m)

	ts, secs, ok := r.FailOpenCooldown(ctx, "scan")
	if !ok || !ts.Equal(transition) || secs != 60 {
		t.Fatalf("weakened cooldown = (%v, %d, %v), want (%v, 60, true)", ts, secs, ok, transition)
	}
	if _, _, ok := r.FailOpenCooldown(ctx, "clean"); ok {
		t.Fatal("clean interceptor reported a cooldown")
	}
	if _, _, ok := r.FailOpenCooldown(ctx, "ghost"); ok {
		t.Fatal("unknown interceptor reported a cooldown")
	}
	if _, _, ok := r.FailOpenCooldown(ctx, ""); ok {
		t.Fatal("empty ref reported a cooldown")
	}
}
