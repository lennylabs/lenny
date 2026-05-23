// SPDX-License-Identifier: MIT

//go:build load_local

// Package auth_jwt_verify_throughput exercises pkg/auth/jwt.HMACSigner
// Verify at sustained throughput. The §10.2 invariant: a valid token
// verifies in microseconds; a tampered token always rejects; the
// hot path is safe under concurrent goroutine access.
//
// TESTING.md §12.7.a component-isolated benches.
package auth_jwt_verify_throughput

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "auth_jwt_verify_throughput"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
	signer   *jwt.HMACSigner
	token    string
	tamper   string
}

func (s *Scenario) Name() string { return name }
// RampProfiles enumerates ascending VU counts for capacity discovery
// under LENNY_TIER7A_CAPACITY=1.
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 64, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 128, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 256, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 512, Duration: 2 * time.Second},
	}
}

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.signer = jwt.NewHMACSigner("k-1", []byte("test-secret-bytes-of-sufficient-length-32+"))
	tok, err := s.signer.Sign(jwt.Claims{
		Issuer: "lenny", Subject: "alice@acme.com",
		Audience: []string{"lenny.api"},
		Expiry:   time.Now().Add(time.Hour).Unix(),
		TenantID: "acme", JWTID: "load-1",
	})
	if err != nil {
		return fmt.Errorf("Sign: %w", err)
	}
	s.token = tok
	// Flip a character in the middle of the signature segment. The
	// last byte of a base64url-encoded HMAC-SHA256 signature encodes
	// only 4 effective bits (the other 2 are padding); some
	// decoders tolerate non-zero values in those padding bits and
	// decode to the same byte sequence, so flipping the very last
	// char is not always a real tamper. Flipping a middle char is.
	mid := len(tok) - 8
	if mid < len(tok)-1 && mid > 0 {
		flip := byte('A')
		if tok[mid] == 'A' {
			flip = 'B'
		}
		s.tamper = tok[:mid] + string(flip) + tok[mid+1:]
	} else {
		s.tamper = tok + "X"
	}
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	if iter%2 == 0 {
		if _, err := s.signer.Verify(s.token); err != nil {
			return fmt.Errorf("valid Verify rejected: %v", err)
		}
		s.counters.Inc("verified")
		return nil
	}
	if _, err := s.signer.Verify(s.tamper); err == nil {
		s.counters.Inc("leaks")
		return fmt.Errorf("§10.2 violated: tampered token verified")
	}
	s.counters.Inc("rejected")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("leaks"); v > 0 {
		return fmt.Errorf("§10.2 violated: %d tampered tokens verified", v)
	}
	if s.counters.Get("verified") == 0 || s.counters.Get("rejected") == 0 {
		return fmt.Errorf("scenario must exercise both paths")
	}
	return nil
}
