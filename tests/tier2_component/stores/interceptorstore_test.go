//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4.8 external-interceptor registry, exercising
// the Postgres-backed pkg/gateway/policy/interceptor/interceptorstore/pgstore
// against a real container with the production migrations applied. The
// registry is the persistent, cross-replica registration surface of the
// §4.8 interceptor chain: the deployer-supplied RequestInterceptor
// configurations (endpoint, priority, failPolicy, timeoutMs, phases) the
// gateway loads and the delegation service reads per invocation. Covers
// the CRUD round-trip including the jsonb phase array, the §4.8
// priority-ceiling and PreAuth-phase registration rejections, the
// sentinel errors, the SELECT ... FOR UPDATE mutate path with the §15.1
// version counter and strict-monotonic UpdatedAt, the name-sorted List,
// and the §8.3 SEC-013 server-minted cooldown fields round-tripping into
// the CooldownResolver.
package stores_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore"
	interceptorpg "github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore/pgstore"
)

// interceptorName returns a fresh unique §4.8 interceptor name. The name
// pattern is ^[a-z0-9][a-z0-9_-]{0,127}$, which a UUID hex prefix
// satisfies.
func interceptorName(t *testing.T) string {
	t.Helper()
	return "ic-" + newUUID(t)[:8]
}

// sampleInterceptor builds a valid §4.8 external-interceptor registration
// with a two-phase set so the jsonb phase array has more than one element
// to round-trip.
func sampleInterceptor(name string) interceptorstore.Interceptor {
	return interceptorstore.Interceptor{
		Name:       name,
		Endpoint:   "pii-scanner.acme.svc.cluster.local:50053",
		Priority:   400,
		FailPolicy: interceptor.FailClosed,
		TimeoutMs:  250,
		Phases:     []interceptor.Phase{interceptor.PhasePreDelegation, interceptor.PhasePostAgentOutput},
	}
}

// spec: 4.8 (external-interceptor registry: registration fields,
// priority-ceiling and PreAuth-phase rejection), 8.3 (SEC-013
// server-minted cooldown fields), 15.1 (optimistic-concurrency version).
// diagnosis: the Postgres-backed §4.8 interceptor registry in
// pkg/gateway/policy/interceptor/interceptorstore/pgstore did not behave
// as specified. Create/Get must round-trip the registration fields
// including the jsonb phase array and the SEC-013 server-minted
// transition fields; the §4.8 priority-ceiling and PreAuth-phase
// registration rejections and the sentinel errors must hold; the
// SELECT ... FOR UPDATE mutate path must increment the §15.1 version and
// strictly advance UpdatedAt; List must sort by name; and the persisted
// cooldown fields must drive the §8.3 CooldownResolver.
func TestInterceptorStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := interceptorpg.New(pg.Pool)
	ctx := context.Background()

	t.Run("create and get round-trip preserves the registration fields and jsonb phases", func(t *testing.T) {
		want := sampleInterceptor(interceptorName(t))
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, want.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Name != want.Name || got.Endpoint != want.Endpoint {
			t.Errorf("identity round-trip: got name=%q endpoint=%q, want name=%q endpoint=%q",
				got.Name, got.Endpoint, want.Name, want.Endpoint)
		}
		if got.Priority != want.Priority {
			t.Errorf("Priority = %d, want %d", got.Priority, want.Priority)
		}
		if got.FailPolicy != want.FailPolicy {
			t.Errorf("FailPolicy = %q, want %q", got.FailPolicy, want.FailPolicy)
		}
		if got.TimeoutMs != want.TimeoutMs {
			t.Errorf("TimeoutMs = %d, want %d", got.TimeoutMs, want.TimeoutMs)
		}
		if !slices.Equal(got.Phases, want.Phases) {
			t.Errorf("Phases jsonb round-trip: got %v, want %v", got.Phases, want.Phases)
		}
		// §15.1 — the optimistic-concurrency counter starts at 1.
		if got.Version != 1 {
			t.Errorf("Version on create = %d, want 1", got.Version)
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Error("Create must stamp CreatedAt and UpdatedAt")
		}
		// A freshly registered interceptor has never weakened, so the
		// SEC-013 transition fields are unset.
		if !got.FailOpenTransitionAt.IsZero() || got.CooldownSecondsAtTransition != 0 {
			t.Errorf("fresh interceptor has a recorded transition: at=%v seconds=%d",
				got.FailOpenTransitionAt, got.CooldownSecondsAtTransition)
		}
	})

	t.Run("duplicate name is rejected with ErrAlreadyExists", func(t *testing.T) {
		ic := sampleInterceptor(interceptorName(t))
		if err := store.Create(ctx, ic); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		if err := store.Create(ctx, ic); !errors.Is(err, interceptorstore.ErrAlreadyExists) {
			t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("4.8 registration constraints are enforced before insert", func(t *testing.T) {
		// §4.8 — external interceptors must register at priority > 100;
		// the gateway rejects priority ≤ 100.
		lowPriority := sampleInterceptor(interceptorName(t))
		lowPriority.Priority = interceptor.ReservedPriorityCeiling
		if err := store.Create(ctx, lowPriority); !errors.Is(err, interceptor.ErrInvalidPriority) {
			t.Errorf("Create at priority %d: got %v, want ErrInvalidPriority", lowPriority.Priority, err)
		}
		// §4.8 — the PreAuth phase is built-in only; an external
		// registration that targets it is rejected.
		preAuth := sampleInterceptor(interceptorName(t))
		preAuth.Phases = []interceptor.Phase{interceptor.PhasePostAuth, interceptor.PhasePreAuth}
		if err := store.Create(ctx, preAuth); !errors.Is(err, interceptor.ErrInvalidPhase) {
			t.Errorf("Create targeting PreAuth: got %v, want ErrInvalidPhase", err)
		}
		// A missing endpoint and a malformed name are structural
		// violations rejected before any row is written.
		noEndpoint := sampleInterceptor(interceptorName(t))
		noEndpoint.Endpoint = ""
		if err := store.Create(ctx, noEndpoint); err == nil {
			t.Error("Create without an endpoint should be rejected")
		}
		for _, bad := range []string{"", "With Space", "UPPER", "-leading"} {
			if err := store.Create(ctx, sampleInterceptor(bad)); err == nil {
				t.Errorf("Create with name %q: expected a validation error", bad)
			}
		}
		// None of the rejected registrations left a row behind.
		for _, ic := range []interceptorstore.Interceptor{lowPriority, preAuth, noEndpoint} {
			if _, err := store.Get(ctx, ic.Name); !errors.Is(err, interceptorstore.ErrNotFound) {
				t.Errorf("rejected registration %q was persisted: %v", ic.Name, err)
			}
		}
	})

	t.Run("get and delete of a missing interceptor return ErrNotFound", func(t *testing.T) {
		name := interceptorName(t)
		if _, err := store.Get(ctx, name); !errors.Is(err, interceptorstore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
		if err := store.Delete(ctx, name); !errors.Is(err, interceptorstore.ErrNotFound) {
			t.Errorf("Delete missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("update mutates in a transaction, advances updated_at, bumps version, and re-validates", func(t *testing.T) {
		name := interceptorName(t)
		if err := store.Create(ctx, sampleInterceptor(name)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		before, _ := store.Get(ctx, name)
		// Guarantee a wall-clock tick so the strict-monotonic UpdatedAt
		// assertion is not defeated by microsecond-granularity equality.
		time.Sleep(2 * time.Millisecond)

		updated, err := store.Update(ctx, name, func(ic *interceptorstore.Interceptor) error {
			ic.Priority = 700
			ic.FailPolicy = interceptor.FailOpen
			ic.Phases = []interceptor.Phase{interceptor.PhasePreLLMRequest}
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.Priority != 700 || updated.FailPolicy != interceptor.FailOpen {
			t.Errorf("Update result not applied: %+v", updated)
		}
		// §15.1 — the version increments by exactly one on a successful
		// update.
		if updated.Version != before.Version+1 {
			t.Errorf("Version = %d, want %d", updated.Version, before.Version+1)
		}
		if !updated.UpdatedAt.After(before.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: before=%v after=%v", before.UpdatedAt, updated.UpdatedAt)
		}
		if !updated.CreatedAt.Equal(before.CreatedAt) {
			t.Errorf("Update must preserve CreatedAt: before=%v after=%v", before.CreatedAt, updated.CreatedAt)
		}
		persisted, _ := store.Get(ctx, name)
		if persisted.Priority != 700 || !slices.Equal(persisted.Phases, []interceptor.Phase{interceptor.PhasePreLLMRequest}) {
			t.Errorf("Update not persisted: %+v", persisted)
		}

		// The §4.8 constraints are re-validated on the update path: a
		// mutate that drops priority to the reserved ceiling is rejected
		// and does not advance the stored row.
		if _, err := store.Update(ctx, name, func(ic *interceptorstore.Interceptor) error {
			ic.Priority = interceptor.ReservedPriorityCeiling
			return nil
		}); !errors.Is(err, interceptor.ErrInvalidPriority) {
			t.Errorf("Update violating the priority ceiling: got %v, want ErrInvalidPriority", err)
		}
		// A mutate error aborts the write and leaves the row untouched.
		sentinel := errors.New("caller aborted")
		if _, err := store.Update(ctx, name, func(*interceptorstore.Interceptor) error {
			return sentinel
		}); !errors.Is(err, sentinel) {
			t.Errorf("Update with an aborting mutate: got %v, want the mutate error", err)
		}
		after, _ := store.Get(ctx, name)
		if after.Version != persisted.Version {
			t.Errorf("aborted updates advanced the version: got %d, want %d", after.Version, persisted.Version)
		}
	})

	t.Run("update of a missing interceptor returns ErrNotFound", func(t *testing.T) {
		if _, err := store.Update(ctx, interceptorName(t), func(*interceptorstore.Interceptor) error {
			return nil
		}); !errors.Is(err, interceptorstore.ErrNotFound) {
			t.Errorf("Update missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("list returns rows sorted by name and delete removes them", func(t *testing.T) {
		// List is platform-scoped and returns every registered
		// interceptor, so seed a fresh store to bound the assertion.
		_, isoPG := startStore(t)
		isoStore := interceptorpg.New(isoPG.Pool)
		names := []string{"ic-charlie", "ic-alice", "ic-bob"}
		for _, n := range names {
			if err := isoStore.Create(ctx, sampleInterceptor(n)); err != nil {
				t.Fatalf("Create %q: %v", n, err)
			}
		}
		got, err := isoStore.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		gotNames := make([]string, len(got))
		for i, ic := range got {
			gotNames[i] = ic.Name
		}
		wantSorted := []string{"ic-alice", "ic-bob", "ic-charlie"}
		if !slices.Equal(gotNames, wantSorted) {
			t.Errorf("List order: got %v, want %v", gotNames, wantSorted)
		}
		if err := isoStore.Delete(ctx, "ic-bob"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := isoStore.Get(ctx, "ic-bob"); !errors.Is(err, interceptorstore.ErrNotFound) {
			t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
		}
	})

	t.Run("SEC-013 server-minted cooldown fields round-trip and drive the CooldownResolver", func(t *testing.T) {
		name := interceptorName(t)
		if err := store.Create(ctx, sampleInterceptor(name)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		// §8.3 SEC-013 — the gateway mints the transition timestamp and
		// records the cooldown seconds in force at that transition when a
		// fail-closed → fail-open weakening is persisted. Model that write
		// through the mutate path (the admin API mints these fields; the
		// wire body never sets them).
		transitionAt := time.Now().UTC().Truncate(time.Microsecond)
		const cooldownSeconds = 120
		if _, err := store.Update(ctx, name, func(ic *interceptorstore.Interceptor) error {
			ic.FailPolicy = interceptor.FailOpen
			ic.FailOpenTransitionAt = transitionAt
			ic.CooldownSecondsAtTransition = cooldownSeconds
			return nil
		}); err != nil {
			t.Fatalf("weakening Update: %v", err)
		}

		// The fields survive the Postgres round-trip.
		got, _ := store.Get(ctx, name)
		if !got.FailOpenTransitionAt.Equal(transitionAt) {
			t.Errorf("FailOpenTransitionAt round-trip: got %v, want %v", got.FailOpenTransitionAt, transitionAt)
		}
		if got.CooldownSecondsAtTransition != cooldownSeconds {
			t.Errorf("CooldownSecondsAtTransition = %d, want %d", got.CooldownSecondsAtTransition, cooldownSeconds)
		}

		// §8.3 rule 1 — the registry is the single source of truth read
		// per invocation. The CooldownResolver resolves the pinned
		// transition and the cooldown seconds recorded at transition time.
		resolver := interceptorstore.NewCooldownResolver(store)
		at, seconds, ok := resolver.FailOpenCooldown(ctx, name)
		if !ok {
			t.Fatal("FailOpenCooldown: got ok=false for a weakened interceptor")
		}
		if !at.Equal(transitionAt) || seconds != cooldownSeconds {
			t.Errorf("FailOpenCooldown = (%v, %d), want (%v, %d)", at, seconds, transitionAt, cooldownSeconds)
		}

		// An interceptor with no recorded transition resolves to "no
		// cooldown in force".
		fresh := interceptorName(t)
		if err := store.Create(ctx, sampleInterceptor(fresh)); err != nil {
			t.Fatalf("Create fresh: %v", err)
		}
		if _, _, ok := resolver.FailOpenCooldown(ctx, fresh); ok {
			t.Error("FailOpenCooldown for a never-weakened interceptor: got ok=true, want false")
		}
		// An unknown interceptor resolves to no cooldown, never an error.
		if _, _, ok := resolver.FailOpenCooldown(ctx, interceptorName(t)); ok {
			t.Error("FailOpenCooldown for an unknown interceptor: got ok=true, want false")
		}
	})
}
