// SPDX-License-Identifier: MIT

//go:build load_local

// Package clock_skew_admission exercises pkg/auth/jwt under skewed
// clocks. The §10.2 / §13.3 invariant: a token's Expiry is honoured
// regardless of issuer/verifier clock drift inside the documented
// ±1s skew tolerance; tokens past the tolerance fail closed.
//
// TESTING.md §12.7.a multi-component scenarios.
package clock_skew_admission

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "clock_skew_admission"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
	signer   *jwt.HMACSigner
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.signer = jwt.NewHMACSigner("k-skew", []byte("test-secret-bytes-of-sufficient-length-32+"))
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	expired := iter%2 == 0
	expiry := time.Now().Add(30 * time.Second).Unix()
	if expired {
		expiry = time.Now().Add(-time.Hour).Unix()
	}
	tok, err := s.signer.Sign(jwt.Claims{Issuer: "lenny", Subject: "alice", Expiry: expiry, TenantID: "acme"})
	if err != nil {
		return fmt.Errorf("Sign: %w", err)
	}
	_, verr := s.signer.Verify(tok)
	switch {
	case verr == nil && expired:
		s.counters.Inc("leaked_expired")
		return fmt.Errorf("§13.3 violated: 1h-old expired token verified")
	case verr == nil:
		s.counters.Inc("verified_fresh")
	case expired:
		s.counters.Inc("rejected_expired")
	default:
		return fmt.Errorf("fresh token rejected: %v", verr)
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("leaked_expired"); v > 0 {
		return fmt.Errorf("§13.3 violated: %d expired tokens verified", v)
	}
	if s.counters.Get("verified_fresh") == 0 || s.counters.Get("rejected_expired") == 0 {
		return fmt.Errorf("scenario must exercise both paths")
	}
	return nil
}
