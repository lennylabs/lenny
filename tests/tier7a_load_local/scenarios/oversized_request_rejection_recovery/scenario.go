// SPDX-License-Identifier: MIT

//go:build load_local

// Package oversized_request_rejection_recovery asserts the §13.4
// archive-validator invariants under load. The scenario drives the
// real pkg/upload.ValidateEntry against a stream of mixed valid and
// oversized entries; the §13.4 contract is that every oversized
// entry is rejected with a typed ValidationError and the validator
// recovers immediately to admit the next valid entry. Concurrency
// stress confirms the validator is pure (no shared state, no
// poisoning between calls).
//
// TESTING.md §12.7.a resiliency scenarios.
package oversized_request_rejection_recovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/upload"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "oversized_request_rejection_recovery"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
	allow    upload.RuntimeAllow
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
	s.allow = upload.RuntimeAllow{WorkspaceRoot: "/workspace/current"}
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// 90% oversized, 10% valid — burst pattern that stresses the
	// validator's recovery behaviour.
	oversized := iter%10 != 0
	var entry upload.Entry
	if oversized {
		entry = upload.Entry{
			Path: fmt.Sprintf("payload-%d-%d.bin", vu, iter),
			Kind: upload.KindRegular,
			Size: upload.MaxPerEntrySize + 1,
		}
	} else {
		entry = upload.Entry{
			Path: fmt.Sprintf("payload-%d-%d.bin", vu, iter),
			Kind: upload.KindRegular,
			Size: 1024,
		}
	}
	err := upload.ValidateEntry(entry, s.allow)
	switch {
	case oversized && err != nil:
		var ve *upload.ValidationError
		if errors.As(err, &ve) && ve.Reason == upload.ReasonMaxEntrySize {
			s.counters.Inc("oversized_rejected")
		} else {
			s.counters.Inc("oversized_wrong_reason")
			return fmt.Errorf("§13.4 violated: oversized entry rejected with reason %q (want %q)", reasonOf(err), upload.ReasonMaxEntrySize)
		}
	case !oversized && err == nil:
		s.counters.Inc("valid_accepted")
	case oversized && err == nil:
		s.counters.Inc("oversized_admitted_unexpected")
		return fmt.Errorf("§13.4 violated: oversized entry admitted (size %d > %d)", entry.Size, upload.MaxPerEntrySize)
	default:
		s.counters.Inc("valid_rejected_unexpected")
		return fmt.Errorf("§13.4 violated: valid entry rejected: %v", err)
	}
	return nil
}

func reasonOf(err error) upload.Reason {
	var ve *upload.ValidationError
	if errors.As(err, &ve) {
		return ve.Reason
	}
	return ""
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("oversized_admitted_unexpected") + s.counters.Get("valid_rejected_unexpected") + s.counters.Get("oversized_wrong_reason"); v > 0 {
		return fmt.Errorf("§13.4 violated: %d unexpected validation outcomes", v)
	}
	if s.counters.Get("oversized_rejected") == 0 || s.counters.Get("valid_accepted") == 0 {
		return fmt.Errorf("scenario must exercise both paths")
	}
	return nil
}
