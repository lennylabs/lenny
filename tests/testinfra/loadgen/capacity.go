// SPDX-License-Identifier: MIT

package loadgen

import (
	"context"
	"fmt"
	"time"
)

// RampableScenario is the optional interface scenarios implement to
// opt into capacity discovery. The harness runs each profile
// returned by RampProfiles() in order; the scenario's Assert is the
// pass/fail gate. The last profile that passed is the discovered
// "knee" — the load level at which the component remained healthy.
type RampableScenario interface {
	Scenario
	// RampProfiles returns load profiles in ascending order. The
	// driver tries each in turn; on the first failure, the previous
	// profile is the knee. An empty slice signals the scenario does
	// not support capacity discovery.
	RampProfiles() []Profile
}

// CapacityResult is the outcome of a single ramp.
type CapacityResult struct {
	Scenario       string
	Knee           Profile // last profile that passed
	KneeResult     *Result // the Result that passed
	KneeFound      bool    // false when even the smallest profile failed
	Breaking       Profile // first profile that failed
	BreakingResult *Result
	BreakingError  error
}

// FindCapacityKnee runs each profile from s.RampProfiles() in order
// and returns the knee — the last profile that passed s.Assert. The
// context bounds the entire ramp.
func FindCapacityKnee(ctx context.Context, s RampableScenario) (*CapacityResult, error) {
	profiles := s.RampProfiles()
	if len(profiles) == 0 {
		return &CapacityResult{Scenario: s.Name()}, fmt.Errorf("loadgen.FindCapacityKnee: %s declares no ramp profiles", s.Name())
	}
	out := &CapacityResult{Scenario: s.Name()}
	for _, p := range profiles {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		result, runErr := runRamp(ctx, s, p)
		if runErr != nil {
			out.Breaking = p
			out.BreakingResult = result
			out.BreakingError = runErr
			return out, nil
		}
		assertErr := s.Assert(result)
		if assertErr != nil {
			out.Breaking = p
			out.BreakingResult = result
			out.BreakingError = assertErr
			return out, nil
		}
		out.Knee = p
		out.KneeResult = result
		out.KneeFound = true
	}
	return out, nil
}

// runRamp runs one profile with a fresh scenario instance.
// Each ramp profile gets its own Setup/Teardown cycle so per-run
// state does not leak across profiles.
func runRamp(ctx context.Context, s RampableScenario, p Profile) (*Result, error) {
	runCtx, cancel := context.WithTimeout(ctx, p.Duration+10*time.Second)
	defer cancel()
	return Run(runCtx, s, p)
}
