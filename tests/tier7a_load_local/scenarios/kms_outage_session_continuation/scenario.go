// SPDX-License-Identifier: MIT

//go:build load_local

// Package kms_outage_session_continuation models the §13.1 KMS
// outage contract: cached envelope keys keep already-decrypted
// sessions running during a KMS outage window. Invariant: every
// session whose envelope key is in cache continues to serve; only
// sessions that need a fresh decrypt fail.
//
// TESTING.md §12.7.a resiliency scenarios.
package kms_outage_session_continuation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "kms_outage_session_continuation"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// kmsClient models a KMS with an outage window. While outaged,
// uncached operations fail; cached lookups still succeed.
type kmsClient struct {
	mu      sync.RWMutex
	outage  bool
	cache   map[string][]byte
}

var errKMSDown = errors.New("kms unreachable")

func (k *kmsClient) decrypt(envelope string) ([]byte, error) {
	k.mu.RLock()
	cached, ok := k.cache[envelope]
	outage := k.outage
	k.mu.RUnlock()
	if ok {
		return cached, nil
	}
	if outage {
		return nil, errKMSDown
	}
	// "Real" decrypt: synthesise a cached entry.
	k.mu.Lock()
	k.cache[envelope] = []byte("plaintext-" + envelope)
	k.mu.Unlock()
	return []byte("plaintext-" + envelope), nil
}

func (k *kmsClient) outageBegin() { k.mu.Lock(); k.outage = true; k.mu.Unlock() }
func (k *kmsClient) outageEnd()   { k.mu.Lock(); k.outage = false; k.mu.Unlock() }

type Scenario struct {
	counters *scenkit.Counters
	kms      *kmsClient
	outageOnce sync.Once

	kmsOutageActive atomic.Bool
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 8, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 32, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 128, Duration: 1 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.kms = &kmsClient{cache: make(map[string][]byte, 256)}
	// Pre-warm the cache for envelopes used by the scenario.
	for i := 0; i < 16; i++ {
		_, _ = s.kms.decrypt(fmt.Sprintf("env-cached-%d", i))
	}
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Trigger outage at iter 25.
	if iter == 25 {
		s.outageOnce.Do(func() {
			s.kmsOutageActive.Store(true)
			s.kms.outageBegin()
			s.counters.Inc("outage_started")
		})
	}
	// Half the time, query a cached envelope (should succeed under
	// outage). The other half, query a fresh one (should fail under
	// outage).
	var key string
	cached := iter%2 == 0
	if cached {
		key = fmt.Sprintf("env-cached-%d", vu%16)
	} else {
		key = fmt.Sprintf("env-fresh-%d-%d", vu, iter)
	}
	_, err := s.kms.decrypt(key)
	outage := s.kmsOutageActive.Load()
	switch {
	case cached && err == nil:
		s.counters.Inc("cached_ok")
	case cached && err != nil:
		s.counters.Inc("cached_failed_unexpected")
		return fmt.Errorf("§13.1 violated: cached envelope decrypt failed during outage: %v", err)
	case !cached && outage && errors.Is(err, errKMSDown):
		s.counters.Inc("fresh_failed_during_outage")
	case !cached && !outage && err == nil:
		s.counters.Inc("fresh_ok_before_outage")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("cached_failed_unexpected"); v > 0 {
		return fmt.Errorf("§13.1 violated: %d cached envelope failures during outage", v)
	}
	if s.counters.Get("cached_ok") == 0 || s.counters.Get("fresh_failed_during_outage") == 0 {
		return fmt.Errorf("scenario must exercise both paths")
	}
	return nil
}
