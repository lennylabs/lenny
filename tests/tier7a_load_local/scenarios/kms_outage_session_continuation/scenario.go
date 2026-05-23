// SPDX-License-Identifier: MIT

//go:build load_local

// Package kms_outage_session_continuation asserts the §12.9 / §4.9.1
// envelope-encryption continuity invariant: a tenant KMS key that is
// disabled (lifecycle state pending_deletion) MUST surface as
// ErrKeyUnavailable through ProbeAvailability — yet DEKs already
// wrapped under that key MUST still unwrap so in-flight sessions
// continue to read their workspace state.
//
// The scenario drives the real pkg/tenantkms.Lifecycle on top of
// pkg/kms.Local. Setup provisions a T4 tenant key and wraps a batch
// of DEKs (the "session" envelopes). At runtime half the iterations
// flip the key through DisableKey to model an outage; both before
// and after the outage, every iteration unwraps an existing DEK and
// also calls ProbeAvailability. The assertion confirms that the
// outage was observable AND no unwrap failed.
//
// TESTING.md §12.7.a resiliency scenarios.
package kms_outage_session_continuation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/tenantkms"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "kms_outage_session_continuation"

const (
	tenantID   = "acme"
	envelopes  = 256
)

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters

	local       *kms.Local
	manager     *tenantkms.LocalManager
	lifecycle   *tenantkms.Lifecycle
	wrappedPool []kms.WrappedDEK
	expectedDEK [][]byte

	disableOnce sync.Once
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 16, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 64, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 256, Duration: 1 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	local, err := kms.NewLocalRandom()
	if err != nil {
		return fmt.Errorf("kms.NewLocalRandom: %w", err)
	}
	s.local = local
	s.manager = tenantkms.NewLocalManager(local)
	s.lifecycle = tenantkms.New(s.manager)

	if _, err := s.lifecycle.EnsureForTenant(ctx, tenantID, tenantkms.WorkspaceTierT4); err != nil {
		return fmt.Errorf("EnsureForTenant: %w", err)
	}

	// Pre-wrap N DEKs to model the workspace envelopes that exist on
	// disk before the outage. Each VU unwraps one of these per iteration.
	alias := tenantkms.AliasFor(tenantID)
	s.wrappedPool = make([]kms.WrappedDEK, envelopes)
	s.expectedDEK = make([][]byte, envelopes)
	for i := 0; i < envelopes; i++ {
		dek := make([]byte, kms.DEKSize)
		for j := range dek {
			dek[j] = byte((i + j) & 0xff)
		}
		w, err := local.WrapDEK(ctx, alias, dek)
		if err != nil {
			return fmt.Errorf("seed WrapDEK[%d]: %w", i, err)
		}
		s.wrappedPool[i] = w
		s.expectedDEK[i] = dek
	}
	// Halfway through the run, simulate the KMS outage by disabling
	// the key. From this point, ProbeAvailability returns
	// ErrKeyUnavailable, but the pre-wrapped DEKs still unwrap.
	time.AfterFunc(500*time.Millisecond, func() {
		s.disableOnce.Do(func() {
			_, _ = s.manager.DisableKey(context.Background(), alias)
		})
	})
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	alias := tenantkms.AliasFor(tenantID)

	// Probe — the §12.5 fail-closed signal during an outage. Both
	// outcomes are valid at different points in the run; the
	// assertion later requires at least one of each.
	switch err := s.lifecycle.ProbeAvailability(ctx, tenantID, tenantkms.WorkspaceTierT4); {
	case err == nil:
		s.counters.Inc("probe_ok")
	case errors.Is(err, tenantkms.ErrKeyUnavailable):
		s.counters.Inc("probe_unavailable")
	default:
		s.counters.Inc("probe_unexpected")
		return fmt.Errorf("§12.5 violated: probe returned unexpected error: %v", err)
	}

	// Unwrap an existing wrapped DEK — the "session continuation" path.
	// It MUST succeed regardless of the outage flag.
	idx := iter % envelopes
	dek, err := s.local.UnwrapDEK(ctx, alias, s.wrappedPool[idx])
	if err != nil {
		s.counters.Inc("unwrap_failed")
		return fmt.Errorf("§12.9 violated: cached envelope decrypt failed during outage: %v", err)
	}
	if !equalBytes(dek, s.expectedDEK[idx]) {
		s.counters.Inc("unwrap_corrupted")
		return fmt.Errorf("§12.9 violated: unwrapped DEK[%d] differs from the wrapped plaintext", idx)
	}
	s.counters.Inc("unwrap_ok")
	return nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if s.counters.Get("unwrap_failed") > 0 {
		return fmt.Errorf("§12.9 violated: %d unwrap failures during simulated outage (session continuity broken)", s.counters.Get("unwrap_failed"))
	}
	if s.counters.Get("unwrap_corrupted") > 0 {
		return fmt.Errorf("§12.9 violated: unwrapped DEK plaintext differs from original")
	}
	if s.counters.Get("probe_unavailable") == 0 {
		return fmt.Errorf("§12.5 violated: ProbeAvailability did not report ErrKeyUnavailable after key disable (outage was not observable)")
	}
	if s.counters.Get("probe_ok") == 0 {
		return fmt.Errorf("scenario did not exercise the pre-outage healthy-probe path")
	}
	return nil
}
