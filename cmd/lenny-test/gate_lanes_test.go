// SPDX-License-Identifier: MIT

package main

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// The gate's lanes are contributor tooling (change 0172, GS-3), so these
// cases carry no specification annotation.

// laneLog records the start and end of each tier a fake executor runs,
// in the order they happen, from executors that run at the same time.
type laneLog struct {
	mu     sync.Mutex
	events []string
	plans  map[string]tierPlan
	// hooks run in a tier's executor right after its start is logged.
	hooks map[string]func()
}

func (l *laneLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *laneLog) index(event string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, e := range l.events {
		if e == event {
			return i
		}
	}
	return -1
}

// laneRunner returns a runner whose every tier logs its start and end and
// sleeps for its entry in sleeps, with a plan that selects the unit,
// component, contract, and integration tiers. status gives a tier's
// status, pass by default.
func laneRunner(l *laneLog, sleeps map[string]time.Duration, status map[string]string) gateRunner {
	g := stubGateRunner()
	l.plans = map[string]tierPlan{}
	tier := func(name string) func() (string, string, *tierResult) {
		return func() (string, string, *tierResult) {
			l.add("start " + name)
			if hook := l.hooks[name]; hook != nil {
				hook()
			}
			time.Sleep(sleeps[name])
			l.add("end " + name)
			st := verdictstatus.Pass
			if s, ok := status[name]; ok {
				st = s
			}
			return st, "", &tierResult{Total: 1, Passed: 1}
		}
	}
	g.static = func(string, string, string) (string, string, *tierResult) { return tier(tierStatic)() }
	g.docs = tier(tierDocs)
	for _, name := range gateChangedTiers {
		name := name
		g.exec[name] = func(p tierPlan) (string, string, *tierResult) {
			l.mu.Lock()
			l.plans[name] = p
			l.mu.Unlock()
			return tier(name)()
		}
	}
	g.plan = func(string, int, bool) (resolvedSelector, error) {
		return resolvedSelector{tiers: []tierPlan{
			{name: tierStatic},
			{name: tierUnit, pkgs: []string{"./pkg/a"}},
			{name: tierComponent},
			{name: tierContract},
			{name: tierIntegration},
		}}, nil
	}
	return g
}

func runLaneGate(g gateRunner, layout string, goTestP map[string]int) *verdict {
	return g.run(gateOptions{root: ".", base: "main", rdepsDepth: defaultRdepsDepth, layout: layout, goTestP: goTestP, environment: "lenny-gate:test"})
}

// TestGateLayoutsHoldEveryTierOnce checks that each layout runs every
// tier the gate can run exactly once, that the unit-alone layout is the
// one decision 5 of change 0172 chose, that the static-first layout is
// the one decision 15 chose, that static-first is the default (decision
// 19), and that the serial layout, the way back of decision 7, still
// parses.
func TestGateLayoutsHoldEveryTierOnce(t *testing.T) {
	want := append(append([]string{tierStatic}, gateChangedTiers...), tierDocs)
	for name, phases := range gateLayouts {
		seen := map[string]int{}
		for _, phase := range phases {
			for _, lane := range phase {
				for _, tier := range lane {
					seen[tier]++
				}
			}
		}
		for _, tier := range want {
			if seen[tier] != 1 {
				t.Errorf("layout %s runs the %s tier %d time(s)", name, tier, seen[tier])
			}
		}
		if len(seen) != len(want) {
			t.Errorf("layout %s runs tiers %v; want %v", name, seen, want)
		}
	}
	unitAlone := []gatePhase{
		{{tierStatic, tierContract}, {tierComponent, tierIntegration}},
		{{tierUnit}},
		{{tierDocs}},
	}
	if !reflect.DeepEqual(gateLayouts[gateLayoutUnitAlone], unitAlone) {
		t.Errorf("unit-alone = %v; want %v", gateLayouts[gateLayoutUnitAlone], unitAlone)
	}
	staticFirst := []gatePhase{
		{{tierStatic}},
		{{tierContract}, {tierComponent, tierIntegration}},
		{{tierUnit}},
		{{tierDocs}},
	}
	if !reflect.DeepEqual(gateLayouts[gateLayoutStaticFirst], staticFirst) {
		t.Errorf("static-first = %v; want %v", gateLayouts[gateLayoutStaticFirst], staticFirst)
	}
	if defaultGateLayout != gateLayoutStaticFirst {
		t.Errorf("default layout %s; want static-first (decision 19)", defaultGateLayout)
	}
	if _, err := parseGateLayout(gateLayoutSerial); err != nil {
		t.Errorf("parseGateLayout(serial), the way back of decision 7: %v", err)
	}
}

// barrier makes the static and component executors each wait until the
// other has started, so they finish only when the two lanes run at the
// same time. A layout that ran them one after another would make the
// first wait out the timeout, which the caller reports.
func barrier(l *laneLog, timeout time.Duration) *bool {
	staticUp, componentUp := make(chan struct{}), make(chan struct{})
	timedOut := false
	var mu sync.Mutex
	wait := func(own, other chan struct{}) func() {
		return func() {
			close(own)
			select {
			case <-other:
			case <-time.After(timeout):
				mu.Lock()
				timedOut = true
				mu.Unlock()
			}
		}
	}
	l.hooks = map[string]func(){tierStatic: wait(staticUp, componentUp), tierComponent: wait(componentUp, staticUp)}
	return &timedOut
}

// TestGateLanesRunAtTheSameTimeAndEachInOrder checks the unit-alone
// layout: the static tier and the component tier run at the same time, each lane
// runs its tiers in order, the unit tier starts after the first phase's
// lanes have both ended, and the documentation gates run last.
func TestGateLanesRunAtTheSameTimeAndEachInOrder(t *testing.T) {
	l := &laneLog{}
	timedOut := barrier(l, 10*time.Second)
	v := runLaneGate(laneRunner(l, nil, nil), gateLayoutUnitAlone, nil)
	if *timedOut {
		t.Fatalf("the static and component tiers did not run at the same time: %v", l.events)
	}
	if v.Verdict != verdictstatus.VerdictPass {
		t.Fatalf("verdict %s; tiers %+v", v.Verdict, v.Tiers)
	}
	before := func(a, b string) {
		t.Helper()
		if ia, ib := l.index(a), l.index(b); ia < 0 || ib < 0 || ia > ib {
			t.Errorf("%q at %d, %q at %d; want the first before the second in %v", a, ia, b, ib, l.events)
		}
	}
	before("end static", "start contract")
	before("end component", "start integration")
	before("end contract", "start unit")
	before("end integration", "start unit")
	before("end unit", "start docs")
}

// TestGateStaticFirstRunsTheStaticTierAlone checks the static-first
// layout: no other tier starts before the static tier ends, the contract
// tier runs at the same time as the component tier, the container lane
// runs its tiers in order, and the unit tier and then the documentation
// gates follow.
func TestGateStaticFirstRunsTheStaticTierAlone(t *testing.T) {
	l := &laneLog{}
	contractUp, componentUp := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	timedOut := false
	wait := func(own, other chan struct{}) func() {
		return func() {
			close(own)
			select {
			case <-other:
			case <-time.After(10 * time.Second):
				mu.Lock()
				timedOut = true
				mu.Unlock()
			}
		}
	}
	l.hooks = map[string]func(){tierContract: wait(contractUp, componentUp), tierComponent: wait(componentUp, contractUp)}
	v := runLaneGate(laneRunner(l, map[string]time.Duration{tierStatic: 50 * time.Millisecond}, nil), gateLayoutStaticFirst, map[string]int{tierContract: 2, tierComponent: 2})
	if timedOut {
		t.Fatalf("the contract and component tiers did not run at the same time: %v", l.events)
	}
	if v.Verdict != verdictstatus.VerdictPass {
		t.Fatalf("verdict %s; tiers %+v", v.Verdict, v.Tiers)
	}
	if len(l.events) < 2 || l.events[0] != "start static" || l.events[1] != "end static" {
		t.Errorf("events %v; want the static tier to start and end before any other tier starts", l.events)
	}
	before := func(a, b string) {
		t.Helper()
		if ia, ib := l.index(a), l.index(b); ia < 0 || ib < 0 || ia > ib {
			t.Errorf("%q at %d, %q at %d; want the first before the second in %v", a, ia, b, ib, l.events)
		}
	}
	before("end component", "start integration")
	before("end contract", "start unit")
	before("end integration", "start unit")
	before("end unit", "start docs")
	if l.plans[tierContract].goTestP != 2 || l.plans[tierComponent].goTestP != 2 {
		t.Errorf("the go test -p of the overlapping tiers: contract %d, component %d; want 2", l.plans[tierContract].goTestP, l.plans[tierComponent].goTestP)
	}
	if err := checkGoTestP(gateLayoutStaticFirst, gateLayouts[gateLayoutStaticFirst], map[string]int{tierUnit: 2}); err == nil {
		t.Error("--go-test-p unit=2 with the static-first layout was accepted; the unit tier runs alone there")
	}
	if _, err := parseGateLayout(gateLayoutStaticFirst); err != nil {
		t.Errorf("parseGateLayout(static-first): %v", err)
	}
}

// TestGateSerialLayoutRunsEveryTierInOrder checks the serial layout: each
// tier starts after the one before it ends, in the gate's order.
func TestGateSerialLayoutRunsEveryTierInOrder(t *testing.T) {
	l := &laneLog{}
	runLaneGate(laneRunner(l, nil, nil), gateLayoutSerial, nil)
	var want []string
	for _, tier := range []string{tierStatic, tierUnit, tierComponent, tierContract, tierIntegration, tierDocs} {
		want = append(want, "start "+tier, "end "+tier)
	}
	if !reflect.DeepEqual(l.events, want) {
		t.Errorf("serial events = %v; want %v", l.events, want)
	}
}

// TestGateFailingTierStopsNoLane checks that a tier that fails, in either
// lane, stops neither its own lane nor the other lane nor a later phase,
// and that the verdict records every tier with its own status.
func TestGateFailingTierStopsNoLane(t *testing.T) {
	l := &laneLog{}
	status := map[string]string{tierStatic: verdictstatus.Fail, tierComponent: verdictstatus.Fail}
	v := runLaneGate(laneRunner(l, nil, status), gateLayoutUnitAlone, nil)
	if v.Verdict != verdictstatus.VerdictFail {
		t.Errorf("verdict %s; want FAIL", v.Verdict)
	}
	for _, tier := range []string{tierStatic, tierUnit, tierComponent, tierContract, tierIntegration, tierDocs} {
		if l.index("end "+tier) < 0 {
			t.Errorf("the %s tier did not run after a failure: %v", tier, l.events)
		}
		want := verdictstatus.Pass
		if s, ok := status[tier]; ok {
			want = s
		}
		if got := v.Tiers[tier].Status; got != want {
			t.Errorf("tier %s status %q; want %q", tier, got, want)
		}
	}
}

// TestGateRecordsEachTierDurationAndTheWallTime checks that each tier
// keeps its own duration, and that the wall time covers the longest path
// through the phases and is shorter than the tiers' sum when lanes
// overlap. The barrier makes the overlap certain, and the sleeps after it
// leave a margin of 250 ms between the wall time and the sum.
func TestGateRecordsEachTierDurationAndTheWallTime(t *testing.T) {
	l := &laneLog{}
	timedOut := barrier(l, 10*time.Second)
	sleeps := map[string]time.Duration{
		tierStatic:      250 * time.Millisecond,
		tierComponent:   250 * time.Millisecond,
		tierIntegration: 50 * time.Millisecond,
	}
	v := runLaneGate(laneRunner(l, sleeps, nil), gateLayoutUnitAlone, map[string]int{tierComponent: 2})
	if *timedOut {
		t.Fatalf("the lanes did not overlap: %v", l.events)
	}
	var sum int64
	for tier, d := range sleeps {
		got := v.Tiers[tier].DurationMS
		if got < d.Milliseconds() {
			t.Errorf("tier %s duration %d ms; want at least %d", tier, got, d.Milliseconds())
		}
		sum += got
	}
	if v.Gate.WallTimeMS < 300 || v.Gate.WallTimeMS >= sum {
		t.Errorf("wall time %d ms; want at least the 300 ms of the longest lane and less than the tiers' sum %d ms", v.Gate.WallTimeMS, sum)
	}
	if v.Gate.Layout != gateLayoutUnitAlone || !reflect.DeepEqual(v.Gate.GoTestP, map[string]int{tierComponent: 2}) {
		t.Errorf("gate layout %q go test -p %v", v.Gate.Layout, v.Gate.GoTestP)
	}
	if !strings.Contains(v.Command, "--layout unit-alone --go-test-p component=2") {
		t.Errorf("command %q lacks the layout and the go test -p", v.Command)
	}
	var b strings.Builder
	printGateSummary(&b, v, "x")
	if !strings.Contains(b.String(), "layout:      unit-alone, go test -p component=2; the tiers took") {
		t.Errorf("summary lacks the layout line:\n%s", b.String())
	}
}

// TestGateReadsTheStateAfterEveryLaneEnds checks that the second read of
// HEAD and the tree, which catches a tier that moved them, happens after
// every tier of every lane has ended.
func TestGateReadsTheStateAfterEveryLaneEnds(t *testing.T) {
	l := &laneLog{}
	sleeps := map[string]time.Duration{tierIntegration: 50 * time.Millisecond}
	g := laneRunner(l, sleeps, nil)
	var mu sync.Mutex
	var endedAtRead []int
	g.state = func() (string, bool) {
		l.mu.Lock()
		ended := 0
		for _, e := range l.events {
			if strings.HasPrefix(e, "end ") {
				ended++
			}
		}
		l.mu.Unlock()
		mu.Lock()
		endedAtRead = append(endedAtRead, ended)
		mu.Unlock()
		return "abc123", false
	}
	runLaneGate(g, gateLayoutUnitAlone, nil)
	if !reflect.DeepEqual(endedAtRead, []int{0, 6}) {
		t.Errorf("tiers ended at each state read = %v; want 0 before the run and all 6 after it", endedAtRead)
	}
}

// TestGateGoTestPReachesOnlyOverlappingTiers checks that --go-test-p
// reaches a tier's plan while the tier shares its phase with another lane,
// and leaves a tier that runs alone, and every tier of the serial layout,
// at the go command's default.
func TestGateGoTestPReachesOnlyOverlappingTiers(t *testing.T) {
	p := map[string]int{tierUnit: 3, tierComponent: 2, tierIntegration: 4}
	l := &laneLog{}
	runLaneGate(laneRunner(l, nil, nil), gateLayoutUnitAlone, p)
	want := map[string]int{tierUnit: 0, tierComponent: 2, tierContract: 0, tierIntegration: 4}
	for tier, n := range want {
		if got := l.plans[tier].goTestP; got != n {
			t.Errorf("unit-alone: the %s tier's go test -p = %d; want %d", tier, got, n)
		}
	}
	l = &laneLog{}
	runLaneGate(laneRunner(l, nil, nil), gateLayoutSerial, p)
	for tier := range want {
		if got := l.plans[tier].goTestP; got != 0 {
			t.Errorf("serial: the %s tier's go test -p = %d; want the default", tier, got)
		}
	}
}

// TestTierExecutorsPassTheGoTestP checks that a plan's go test -p reaches
// the go command ahead of the packages, and that a plan without one adds
// no -p.
func TestTierExecutorsPassTheGoTestP(t *testing.T) {
	t.Setenv("LENNY_COVER_PROFILE", "")
	calls := recordGoTest(t)
	runUnitTier(tierPlan{name: tierUnit, goTestP: 3})
	runUnitTier(tierPlan{name: tierUnit, pkgs: []string{"./pkg/a"}, funcs: map[string][]string{"./pkg/b": {"TestB"}}, goTestP: 2})
	runContractTier(tierPlan{name: tierContract, pkgs: []string{"./tests/contract/x"}, goTestP: 2})
	runIntegrationTier(tierPlan{name: tierIntegration, goTestP: 2})
	runUnitTier(tierPlan{name: tierUnit})
	if len(*calls) != 6 {
		t.Fatalf("invocations = %v", *calls)
	}
	for i, call := range (*calls)[:5] {
		args := strings.Join(call, " ")
		pi := strings.Index(args, " -p=")
		pkg := strings.Index(args, " ./")
		if pi < 0 || pkg < 0 || pi > pkg {
			t.Errorf("invocation %d = %s; want -p ahead of the packages", i, args)
		}
	}
	if last := strings.Join((*calls)[5], " "); strings.Contains(last, "-p=") {
		t.Errorf("a plan without a go test -p passed one: %s", last)
	}
}

// TestParseGoTestPAndLayoutRefuseBadValues checks the parsing of the
// layout and --go-test-p, and that the host half passes the checked
// values to the gate container.
func TestParseGoTestPAndLayoutRefuseBadValues(t *testing.T) {
	got, err := parseGoTestP(" component=2, integration=1 ")
	if err != nil || !reflect.DeepEqual(got, map[string]int{tierComponent: 2, tierIntegration: 1}) {
		t.Errorf("parseGoTestP = %v, %v", got, err)
	}
	if got, err := parseGoTestP(""); err != nil || len(got) != 0 {
		t.Errorf("an empty value = %v, %v; want no entries", got, err)
	}
	for _, bad := range []string{"component", "static=2", "docs=1", "nightly=1", "unit=0", "unit=-1", "unit=x", "unit=2,unit=3"} {
		if _, err := parseGoTestP(bad); err == nil {
			t.Errorf("parseGoTestP(%q) succeeded", bad)
		}
	}
	if _, err := parseGateLayout("parallel"); err == nil || !strings.Contains(err.Error(), "serial, static-first, unit-alone") {
		t.Errorf("an unknown layout = %v; want an error naming the layouts", err)
	}
	if _, err := gateRunOptions(gateFlagValues{base: "main", rdeps: -1, layout: "parallel"}); err == nil {
		t.Error("gateRunOptions accepted an unknown layout")
	}
	if _, err := gateRunOptions(gateFlagValues{base: "main", rdeps: -1, layout: gateLayoutSerial, goTestP: "static=1"}); err == nil {
		t.Error("gateRunOptions accepted a go test -p for the static tier")
	}
	for _, bad := range []struct{ layout, p string }{
		{gateLayoutSerial, "component=2"},
		{gateLayoutUnitAlone, "unit=2"},
	} {
		if _, err := gateRunOptions(gateFlagValues{base: "main", rdeps: -1, layout: bad.layout, goTestP: bad.p}); err == nil || !strings.Contains(err.Error(), "runs alone") {
			t.Errorf("--layout %s --go-test-p %s = %v; want a refusal for a tier that runs alone", bad.layout, bad.p, err)
		}
	}
	o, err := gateRunOptions(gateFlagValues{base: "main", rdeps: -1, all: true, layout: gateLayoutUnitAlone, goTestP: "integration=2,component=1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--base", "main", "--rdeps-depth", "-1", "--layout", "unit-alone", "--go-test-p", "component=1,integration=2", "--all"}
	if got := gatePassArgs(o); !reflect.DeepEqual(got, want) {
		t.Errorf("pass args = %v; want %v", got, want)
	}
}

// TestGateRunnerWithAnUnknownLayoutFails checks that a runner handed a
// layout runGate would refuse runs every tier serially and fails, rather
// than passing on a layout it does not know.
func TestGateRunnerWithAnUnknownLayoutFails(t *testing.T) {
	l := &laneLog{}
	v := runLaneGate(laneRunner(l, nil, nil), "parallel", nil)
	if v.Verdict != verdictstatus.VerdictFail || !strings.Contains(v.Tiers[tierStatic].Detail, "not a gate layout") {
		t.Errorf("verdict %s, static %+v", v.Verdict, v.Tiers[tierStatic])
	}
}

// TestGateRecordsAPlannedTierNoLaneRunsAsNotRun checks that a tier the
// plan holds and the layout has no lane for is recorded as not run, which
// keeps the verdict from passing, rather than being dropped.
func TestGateRecordsAPlannedTierNoLaneRunsAsNotRun(t *testing.T) {
	l := &laneLog{}
	g := laneRunner(l, nil, nil)
	g.plan = func(string, int, bool) (resolvedSelector, error) {
		return resolvedSelector{tiers: []tierPlan{{name: tierUnit}, {name: tierSecurity}}}, nil
	}
	v := runLaneGate(g, gateLayoutUnitAlone, nil)
	if v.Verdict == verdictstatus.VerdictPass {
		t.Fatalf("a planned tier without a lane left the verdict at PASS: %+v", v.Tiers)
	}
	if got := v.Tiers[tierSecurity].Status; got != verdictstatus.Unverified {
		t.Errorf("security tier %q; want unverified", got)
	}
	if len(v.Gate.NotRun) != 1 || !strings.Contains(v.Gate.NotRun[0], "has no lane for the security tier") {
		t.Errorf("not-run list %q", v.Gate.NotRun)
	}
	if l.index("end unit") < 0 {
		t.Errorf("the unit tier did not run: %v", l.events)
	}
}

// TestComponentTierFlagsPassTheGoTestP checks the component tier's flags:
// its tag and timeout always, and -p only when the plan sets one.
func TestComponentTierFlagsPassTheGoTestP(t *testing.T) {
	with := strings.Join(componentTierFlags(tierPlan{name: tierComponent, goTestP: 2}), " ")
	if !strings.Contains(with, "-tags=component") || !strings.HasSuffix(with, "-p=2") {
		t.Errorf("component flags with a go test -p = %s", with)
	}
	if without := strings.Join(componentTierFlags(tierPlan{name: tierComponent}), " "); strings.Contains(without, "-p=") {
		t.Errorf("component flags without one = %s", without)
	}
}
