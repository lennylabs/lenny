// SPDX-License-Identifier: MIT

//go:build load_local

// Package oversized_payload_rejection exercises pkg/idempotency.Key
// validation against payloads at and past the §11.5 128-character
// cap.
//
// TESTING.md §12.7.a multi-component scenarios.
package oversized_payload_rejection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "oversized_payload_rejection"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error    { return nil }
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	wantReject := iter%2 == 0
	keyValue := fmt.Sprintf("k-%d-%d", vu, iter)
	if wantReject {
		keyValue = strings.Repeat("x", idempotency.MaxKeyLength+1+(iter%32))
	}
	key := idempotency.Key{TenantID: "acme", Value: keyValue}
	err := key.Validate()
	switch {
	case err == nil && wantReject:
		s.counters.Inc("leaks")
		return fmt.Errorf("§11.5 violated: oversized key %d chars accepted", len(keyValue))
	case err == nil:
		s.counters.Inc("accepted")
	default:
		var tooLong *idempotency.KeyTooLongError
		if !errors.As(err, &tooLong) && wantReject {
			s.counters.Inc("leaks")
			return fmt.Errorf("§11.5 violated: oversized key rejected with wrong error %T", err)
		}
		s.counters.Inc("rejected")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("leaks"); v > 0 {
		return fmt.Errorf("§11.5 violated: %d oversized keys leaked through validation", v)
	}
	if s.counters.Get("accepted") == 0 || s.counters.Get("rejected") == 0 {
		return fmt.Errorf("scenario must exercise both paths")
	}
	return nil
}
