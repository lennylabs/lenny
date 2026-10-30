// SPDX-License-Identifier: MIT

package loadgen

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// rampScenario is a Scenario that fails when VUs exceeds maxVUs.
type rampScenario struct {
	maxVUs   int
	profiles []Profile
	calls    atomic.Int64
}

func (r *rampScenario) Name() string                     { return "ramp-test" }
func (r *rampScenario) DefaultProfile() Profile          { return r.profiles[0] }
func (r *rampScenario) Setup(_ context.Context) error    { return nil }
func (r *rampScenario) Teardown(_ context.Context) error { return nil }
func (r *rampScenario) Run(_ context.Context, _, _ int) error {
	r.calls.Add(1)
	return nil
}
func (r *rampScenario) RampProfiles() []Profile { return r.profiles }
func (r *rampScenario) Assert(res *Result) error {
	if res.Profile.VUs > r.maxVUs {
		return fmt.Errorf("over capacity at vu=%d", res.Profile.VUs)
	}
	return nil
}

func TestFindCapacityKneeFindsKnee(t *testing.T) {
	r := &rampScenario{
		maxVUs: 8,
		profiles: []Profile{
			{Kind: ConstantVU, VUs: 2, Duration: 100 * time.Millisecond},
			{Kind: ConstantVU, VUs: 4, Duration: 100 * time.Millisecond},
			{Kind: ConstantVU, VUs: 8, Duration: 100 * time.Millisecond},
			{Kind: ConstantVU, VUs: 16, Duration: 100 * time.Millisecond},
			{Kind: ConstantVU, VUs: 32, Duration: 100 * time.Millisecond},
		},
	}
	result, err := FindCapacityKnee(context.Background(), r)
	if err != nil {
		t.Fatalf("FindCapacityKnee: %v", err)
	}
	if !result.KneeFound {
		t.Fatal("expected knee to be found")
	}
	if result.Knee.VUs != 8 {
		t.Errorf("knee VUs=%d want 8", result.Knee.VUs)
	}
	if result.Breaking.VUs != 16 {
		t.Errorf("breaking VUs=%d want 16", result.Breaking.VUs)
	}
}

func TestFindCapacityKneeNoKneeWhenSmallestFails(t *testing.T) {
	r := &rampScenario{
		maxVUs: 1,
		profiles: []Profile{
			{Kind: ConstantVU, VUs: 2, Duration: 100 * time.Millisecond},
			{Kind: ConstantVU, VUs: 4, Duration: 100 * time.Millisecond},
		},
	}
	result, err := FindCapacityKnee(context.Background(), r)
	if err != nil {
		t.Fatalf("FindCapacityKnee: %v", err)
	}
	if result.KneeFound {
		t.Fatal("expected KneeFound=false when smallest profile fails")
	}
	if result.Breaking.VUs != 2 {
		t.Errorf("breaking VUs=%d want 2", result.Breaking.VUs)
	}
}

func TestFindCapacityKneeEmptyProfilesIsError(t *testing.T) {
	r := &rampScenario{maxVUs: 1, profiles: nil}
	_, err := FindCapacityKnee(context.Background(), r)
	if err == nil {
		t.Fatal("expected error for empty profile list")
	}
}
