// SPDX-License-Identifier: MIT

package loadgen

import (
	"context"
	"time"
)

// Scenario is a single tier-7a load test. Each scenario lives in its
// own subpackage under tests/tier7a_load_local/scenarios/ and
// registers itself with the default Registry via Register().
type Scenario interface {
	// Name uniquely identifies the scenario within the registry.
	Name() string

	// Setup builds whatever in-process services this scenario needs:
	// miniredis, fakekube, embedded Postgres, runtime stubs, etc.
	// It must be safe to call concurrently with other scenarios.
	Setup(ctx context.Context) error

	// Run executes a single virtual-user iteration. The driver calls
	// Run concurrently per the active profile. vu is the worker index
	// (1..N), iter is the per-worker iteration counter (1..M). A nil
	// return is a successful iteration; a non-nil return is recorded
	// as a failure for the iteration but does not stop the run.
	Run(ctx context.Context, vu int, iter int) error

	// Teardown releases everything Setup acquired. It is called
	// regardless of Setup or Run outcome.
	Teardown(ctx context.Context) error

	// DefaultProfile returns the scenario's canonical load profile.
	// The driver uses this when the caller does not override.
	DefaultProfile() Profile

	// Assert validates the aggregated Result against the scenario's
	// SLO. A nil return is a pass; a non-nil return fails the test
	// with the returned error as the diagnosis.
	Assert(r *Result) error
}

// ProfileKind selects the load shape.
type ProfileKind int

const (
	// ConstantVU runs N virtual users concurrently for Duration.
	ConstantVU ProfileKind = iota

	// ConstantArrivalRate dispatches Rate iterations per second
	// across a worker pool of size VUs for Duration.
	ConstantArrivalRate

	// RampingVU steps the VU count through RampStages.
	RampingVU
)

// RampStage is one entry in a RampingVU profile.
type RampStage struct {
	Duration time.Duration
	Target   int
}

// Profile describes the load shape the driver applies.
type Profile struct {
	Kind       ProfileKind
	VUs        int
	Rate       int
	Duration   time.Duration
	RampStages []RampStage
}

// WithDuration returns a copy of p with Duration set to d.
func (p Profile) WithDuration(d time.Duration) Profile {
	p.Duration = d
	return p
}

// WithVUs returns a copy of p with VUs set to n.
func (p Profile) WithVUs(n int) Profile {
	p.VUs = n
	return p
}
