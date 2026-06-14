// SPDX-License-Identifier: MIT

//go:build load_local

// Package idempotency_cache_eviction drives the inproc gateway with
// repeated POSTs sharing the same Idempotency-Key. The §11.5
// invariant: the gateway returns the cached response for replays;
// the second-through-Nth call hits the cache.
//
// TESTING.md §12.7.a multi-component scenarios.
package idempotency_cache_eviction

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/inproc"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
)

var pooledClient = &http.Client{
	Transport: &http.Transport{MaxIdleConns: 200, MaxIdleConnsPerHost: 200, IdleConnTimeout: 30 * time.Second},
	Timeout:   5 * time.Second,
}

func init() {
	loadgen.Register("idempotency_cache_eviction", func() loadgen.Scenario { return &Scenario{} })
}

type Scenario struct {
	env      *inproc.Env
	hits     atomic.Int64
	failures atomic.Int64
}

func (s *Scenario) Name() string { return "idempotency_cache_eviction" }

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.env = inproc.New(inproc.Config{})
	return s.env.Start(ctx)
}

func (s *Scenario) Teardown(ctx context.Context) error {
	return s.env.Stop(ctx)
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	body := []byte(`{"runtimeRef":"echo"}`)
	req, _ := http.NewRequestWithContext(ctx, "POST", s.env.GatewayURL()+"/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "shared-key")
	resp, err := pooledClient.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			s.failures.Add(1)
		}
		return err
	}
	// Drain + Close so the connection returns to the pool. Without
	// the io.Copy, partial bodies can leave the connection in
	// "abandoned" state and the next Do() opens a fresh socket,
	// exhausting the loopback ephemeral port range.
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		s.failures.Add(1)
		return fmt.Errorf("status=%d", resp.StatusCode)
	}
	s.hits.Add(1)
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	r.AddCustom("hits", float64(s.hits.Load()))
	r.AddCustom("failures", float64(s.failures.Load()))
	r.AddCustom("idem_hits", float64(s.env.IdempotencyHits()))
	r.AddCustom("sessions", float64(s.env.SessionCount()))
	if s.failures.Load() > 0 {
		return fmt.Errorf("§11.5 violated: %d failed sessions", s.failures.Load())
	}
	// Exactly one session must have been created across the run; every
	// other POST was an idempotency cache hit.
	if s.env.SessionCount() != 1 {
		return fmt.Errorf("§11.5 violated: %d sessions created for one key", s.env.SessionCount())
	}
	if s.env.IdempotencyHits() == 0 {
		return fmt.Errorf("§11.5 violated: zero cache hits across %d POSTs", s.hits.Load())
	}
	return nil
}
