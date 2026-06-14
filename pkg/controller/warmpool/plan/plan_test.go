// SPDX-License-Identifier: MIT

package plan_test

import (
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/controller/warmpool/plan"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

func idle(names ...string) []plan.Pod {
	pods := make([]plan.Pod, len(names))
	for i, n := range names {
		pods[i] = plan.Pod{Name: n, Phase: state.Idle}
	}
	return pods
}

func TestCompute(t *testing.T) {
	tests := []struct {
		name string
		in   plan.Inputs
		want plan.Plan
	}{
		{
			name: "empty pool creates up to minWarm",
			in:   plan.Inputs{MinWarm: 3, MaxWarm: 10},
			want: plan.Plan{Create: 3},
		},
		{
			name: "pool already at target is left alone",
			in:   plan.Inputs{MinWarm: 3, MaxWarm: 10, Pods: idle("a", "b", "c")},
			want: plan.Plan{WarmCount: 3, ReadyCount: 3},
		},
		{
			name: "pool below target creates the gap",
			in:   plan.Inputs{MinWarm: 3, MaxWarm: 10, Pods: idle("a")},
			want: plan.Plan{Create: 2, WarmCount: 1, ReadyCount: 1},
		},
		{
			name: "pool above target drains the excess idle pods",
			in:   plan.Inputs{MinWarm: 3, MaxWarm: 10, Pods: idle("a", "b", "c", "d", "e")},
			want: plan.Plan{Drain: []string{"a", "b"}, WarmCount: 5, ReadyCount: 5},
		},
		{
			name: "maxWarm caps the create count below minWarm",
			in:   plan.Inputs{MinWarm: 10, MaxWarm: 5},
			want: plan.Plan{Create: 5},
		},
		{
			name: "warming pods count as warm but not ready",
			in: plan.Inputs{MinWarm: 3, MaxWarm: 10, Pods: []plan.Pod{
				{Name: "w1", Phase: state.Warming},
				{Name: "w2", Phase: state.SDKConnecting},
				{Name: "i1", Phase: state.Idle},
			}},
			want: plan.Plan{WarmCount: 3, ReadyCount: 1},
		},
		{
			name: "warming pods reduce the create count",
			in: plan.Inputs{MinWarm: 3, MaxWarm: 10, Pods: []plan.Pod{
				{Name: "w1", Phase: state.Warming},
				{Name: "w2", Phase: state.Warming},
			}},
			want: plan.Plan{Create: 1, WarmCount: 2},
		},
		{
			name: "claimed pods are not warm inventory",
			in: plan.Inputs{MinWarm: 3, MaxWarm: 10, Pods: []plan.Pod{
				{Name: "c1", Phase: state.Claimed},
				{Name: "i1", Phase: state.Idle},
			}},
			want: plan.Plan{Create: 2, WarmCount: 1, ReadyCount: 1},
		},
		{
			// With one session mode a pod serving concurrent sessions
			// projects the coarse claimed phase (the former slot_active
			// value collapsed into claimed), so it is not warm inventory:
			// the planner counts neither pod as warm and creates the gap.
			name: "concurrently-occupied claimed pods are not warm inventory",
			in: plan.Inputs{MinWarm: 2, MaxWarm: 10, Pods: []plan.Pod{
				{Name: "c1", Phase: state.Claimed},
				{Name: "c2", Phase: state.Claimed},
			}},
			want: plan.Plan{Create: 2},
		},
		{
			// A claimed pod is never a drain candidate even above target:
			// only idle pods drain, so the planner sheds idle and leaves
			// the claimed pod (whose claim carries live sessions) untouched.
			name: "claimed pod is not a drain candidate even above target",
			in: plan.Inputs{MinWarm: 1, MaxWarm: 10, Pods: []plan.Pod{
				{Name: "c1", Phase: state.Claimed},
				{Name: "c2", Phase: state.Claimed},
				{Name: "i1", Phase: state.Idle},
			}},
			want: plan.Plan{WarmCount: 1, ReadyCount: 1},
		},
		{
			name: "occupied, draining, and terminal pods are ignored",
			in: plan.Inputs{MinWarm: 2, MaxWarm: 10, Pods: []plan.Pod{
				{Name: "i1", Phase: state.Idle},
				{Name: "d1", Phase: state.Draining},
				{Name: "f1", Phase: state.Failed},
				{Name: "t1", Phase: state.Terminated},
				{Name: "r1", Phase: state.Reserved},
			}},
			want: plan.Plan{Create: 1, WarmCount: 1, ReadyCount: 1},
		},
		{
			name: "cold pool with minWarm zero creates nothing",
			in:   plan.Inputs{MinWarm: 0, MaxWarm: 5},
			want: plan.Plan{},
		},
		{
			name: "over target drains idle only, leaving warming pods",
			in: plan.Inputs{MinWarm: 3, MaxWarm: 10, Pods: []plan.Pod{
				{Name: "i1", Phase: state.Idle},
				{Name: "i2", Phase: state.Idle},
				{Name: "i3", Phase: state.Idle},
				{Name: "i4", Phase: state.Idle},
				{Name: "w1", Phase: state.Warming},
				{Name: "w2", Phase: state.Warming},
			}},
			want: plan.Plan{Drain: []string{"i1", "i2", "i3"}, WarmCount: 6, ReadyCount: 4},
		},
		{
			name: "drain is capped at the idle pod count",
			in: plan.Inputs{MinWarm: 0, MaxWarm: 10, Pods: []plan.Pod{
				{Name: "i1", Phase: state.Idle},
				{Name: "i2", Phase: state.Idle},
				{Name: "w1", Phase: state.Warming},
				{Name: "w2", Phase: state.Warming},
				{Name: "w3", Phase: state.Warming},
			}},
			want: plan.Plan{Drain: []string{"i1", "i2"}, WarmCount: 5, ReadyCount: 2},
		},
		{
			name: "negative bounds are clamped to zero",
			in:   plan.Inputs{MinWarm: -5, MaxWarm: -3, Pods: idle("a")},
			want: plan.Plan{Drain: []string{"a"}, WarmCount: 1, ReadyCount: 1},
		},
		{
			name: "minWarm above maxWarm targets maxWarm",
			in:   plan.Inputs{MinWarm: 8, MaxWarm: 3},
			want: plan.Plan{Create: 3},
		},
		{
			name: "minWarm above maxWarm drains down to maxWarm",
			in:   plan.Inputs{MinWarm: 8, MaxWarm: 3, Pods: idle("a", "b", "c", "d", "e")},
			want: plan.Plan{Drain: []string{"a", "b"}, WarmCount: 5, ReadyCount: 5},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := plan.Compute(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Compute() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestComputeDrainIsDeterministic confirms the drain list is stable
// regardless of the order pods are observed in, so successive
// reconcile passes do not retire a different arbitrary pod each time.
func TestComputeDrainIsDeterministic(t *testing.T) {
	forward := plan.Inputs{MinWarm: 1, MaxWarm: 10, Pods: idle("delta", "alpha", "charlie", "bravo")}
	reverse := plan.Inputs{MinWarm: 1, MaxWarm: 10, Pods: idle("bravo", "charlie", "alpha", "delta")}

	gotForward := plan.Compute(forward)
	gotReverse := plan.Compute(reverse)

	if !reflect.DeepEqual(gotForward, gotReverse) {
		t.Fatalf("drain plan depends on pod input order: %+v vs %+v", gotForward, gotReverse)
	}
	want := []string{"alpha", "bravo", "charlie"}
	if !reflect.DeepEqual(gotForward.Drain, want) {
		t.Errorf("Drain = %v, want %v (sorted ascending)", gotForward.Drain, want)
	}
}
