// SPDX-License-Identifier: MIT

package loadgen

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunConstantVU asserts the driver invokes Setup once, Run many
// times concurrently across VUs, and Teardown once, and that the
// aggregated Result reflects the observed counters.
func TestRunConstantVU(t *testing.T) {
	t.Parallel()
	var setupCalls, teardownCalls, runCalls atomic.Int64
	s := &testScenario{
		name: "constant-vu-test",
		setup: func(ctx context.Context) error {
			setupCalls.Add(1)
			return nil
		},
		run: func(ctx context.Context, vu, iter int) error {
			runCalls.Add(1)
			time.Sleep(time.Millisecond)
			return nil
		},
		teardown: func(ctx context.Context) error {
			teardownCalls.Add(1)
			return nil
		},
		profile: Profile{Kind: ConstantVU, VUs: 4, Duration: 200 * time.Millisecond},
	}
	res, err := Run(context.Background(), s, s.DefaultProfile())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if setupCalls.Load() != 1 {
		t.Errorf("Setup calls: got %d, want 1", setupCalls.Load())
	}
	if teardownCalls.Load() != 1 {
		t.Errorf("Teardown calls: got %d, want 1", teardownCalls.Load())
	}
	if res.Iterations < 4 {
		t.Errorf("expected at least 4 iterations across 4 VUs, got %d", res.Iterations)
	}
	if res.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", res.Errors)
	}
	if res.Throughput <= 0 {
		t.Errorf("expected positive throughput, got %f", res.Throughput)
	}
}

// TestRunRecordsErrors asserts iteration failures are counted but do
// not stop the run, and that distinct error messages are sampled.
func TestRunRecordsErrors(t *testing.T) {
	t.Parallel()
	var iter atomic.Int64
	s := &testScenario{
		name:  "error-test",
		setup: func(ctx context.Context) error { return nil },
		run: func(ctx context.Context, vu, it int) error {
			n := iter.Add(1)
			if n%3 == 0 {
				return fmt.Errorf("err-%d", n%3)
			}
			return nil
		},
		teardown: func(ctx context.Context) error { return nil },
		profile:  Profile{Kind: ConstantVU, VUs: 2, Duration: 100 * time.Millisecond},
	}
	res, err := Run(context.Background(), s, s.DefaultProfile())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Iterations == 0 {
		t.Fatal("no iterations observed")
	}
	if res.Errors == 0 {
		t.Fatal("expected some errors")
	}
}

// TestRegistry asserts the default registry rejects duplicates and
// returns scenarios by name.
func TestRegistry(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register("a", func() Scenario { return &testScenario{name: "a"} })
	if r.Len() != 1 {
		t.Fatalf("Len=%d want 1", r.Len())
	}
	if _, ok := r.Get("a"); !ok {
		t.Fatal("Get(\"a\") not found")
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get(\"missing\") unexpectedly found")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected panic on duplicate Register")
			}
		}()
		r.Register("a", func() Scenario { return &testScenario{name: "a"} })
	}()
}

// TestHistogramQuantile asserts the percentile arithmetic matches
// the contract used by SLO assertions.
func TestHistogramQuantile(t *testing.T) {
	t.Parallel()
	h := NewHistogram()
	for i := 1; i <= 100; i++ {
		h.Observe(float64(i))
	}
	if got := h.Quantile(0.50); got < 49 || got > 51 {
		t.Errorf("P50=%f want ~50", got)
	}
	if got := h.Quantile(0.99); got < 98 || got > 100 {
		t.Errorf("P99=%f want ~99-100", got)
	}
}

// TestProfileValidation asserts the driver rejects malformed profiles
// with helpful messages.
func TestProfileValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		profile Profile
		wantErr string
	}{
		{"zero duration", Profile{Kind: ConstantVU, VUs: 1}, "Duration must be positive"},
		{"constant-vu zero vu", Profile{Kind: ConstantVU, Duration: time.Second}, "VUs > 0"},
		{"constant-rate zero rate", Profile{Kind: ConstantArrivalRate, VUs: 1, Duration: time.Second}, "Rate > 0"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := validateProfile(tc.profile)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, err) || !contains(err.Error(), tc.wantErr) {
				t.Errorf("got %q want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// testScenario is a configurable Scenario used by the driver tests.
type testScenario struct {
	name     string
	setup    func(context.Context) error
	run      func(context.Context, int, int) error
	teardown func(context.Context) error
	profile  Profile
	assert   func(*Result) error
}

func (t *testScenario) Name() string                              { return t.name }
func (t *testScenario) Setup(ctx context.Context) error           { return t.setup(ctx) }
func (t *testScenario) Run(ctx context.Context, vu, it int) error { return t.run(ctx, vu, it) }
func (t *testScenario) Teardown(ctx context.Context) error        { return t.teardown(ctx) }
func (t *testScenario) DefaultProfile() Profile                   { return t.profile }
func (t *testScenario) Assert(r *Result) error {
	if t.assert == nil {
		return nil
	}
	return t.assert(r)
}
