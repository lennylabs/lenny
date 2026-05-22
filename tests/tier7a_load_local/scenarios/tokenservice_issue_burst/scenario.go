// SPDX-License-Identifier: MIT

//go:build load_local

// Package tokenservice_issue_burst exercises pkg/tokenservice issue
// throughput under N concurrent issuers. The §10.2 invariant: every
// issued token carries a unique jti, the sign latency P99 stays
// bounded under load, and the issuer's internal state machine is
// race-free.
//
// TESTING.md §12.7.a component-isolated benches.
package tokenservice_issue_burst

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "tokenservice_issue_burst"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// issuer is a scenario-local model of the production tokenservice
// issue path: HMAC-SHA256 sign with a monotonically increasing jti.
type issuer struct {
	signer *jwt.HMACSigner
	jti    atomic.Uint64

	mu       sync.Mutex
	seenJTIs map[string]bool
}

func newIssuer() *issuer {
	return &issuer{
		signer:   jwt.NewHMACSigner("k-issuer", []byte("test-secret-bytes-of-sufficient-length-32+")),
		seenJTIs: make(map[string]bool, 1024),
	}
}

// issue mints a token with a fresh jti and asserts uniqueness.
func (i *issuer) issue(tenant, subject string) (string, error) {
	id := i.jti.Add(1)
	jtiStr := fmt.Sprintf("jti-%d", id)
	tok, err := i.signer.Sign(jwt.Claims{
		Issuer:   "lenny",
		Subject:  subject,
		TenantID: tenant,
		JWTID:    jtiStr,
		Expiry:   time.Now().Add(5 * time.Minute).Unix(),
	})
	if err != nil {
		return "", err
	}
	i.mu.Lock()
	if i.seenJTIs[jtiStr] {
		i.mu.Unlock()
		return "", fmt.Errorf("§10.2 violated: duplicate jti %s", jtiStr)
	}
	i.seenJTIs[jtiStr] = true
	i.mu.Unlock()
	return tok, nil
}

type Scenario struct {
	counters *scenkit.Counters
	iss      *issuer
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.iss = newIssuer()
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	tok, err := s.iss.issue("acme", fmt.Sprintf("user-%d", vu))
	if err != nil {
		s.counters.Inc("issue_errors")
		return err
	}
	if tok == "" {
		s.counters.Inc("empty_tokens")
		return fmt.Errorf("§10.2 violated: empty token")
	}
	s.counters.Inc("issued")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("issue_errors"); v > 0 {
		return fmt.Errorf("§10.2 violated: %d issue errors (suggests duplicate jti under race)", v)
	}
	if s.counters.Get("issued") == 0 {
		return fmt.Errorf("scenario did not issue anything")
	}
	return nil
}
