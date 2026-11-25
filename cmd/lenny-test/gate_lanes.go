// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// This file holds the layout of the gate's tiers (change 0172, GS-3): the
// gate runs its tiers in phases, each phase runs its lanes at the same
// time, and each lane runs its tiers one after another. The tiers that
// start containers (component and integration) share a lane, so their
// containers never start at the same time as each other, and the layout
// keeps the unit tier, which compiles and runs the most code, apart from
// them. Every tier runs whatever another tier reports. The harness is
// contributor tooling, so this file carries no specification annotation.

// The gate layouts. gateLayoutUnitAlone is decision 5 of change 0172: the
// static and contract tiers run beside the component and integration
// tiers, then the unit tier runs alone, then the documentation gates.
// gateLayoutSerial runs every tier one after another, as the gate did
// before the change. Decision 7 makes the concurrent layout the default
// when its validation run is clean. The validation run of 2026-11-24
// (gate run d47d33b8ed80054999c769e1) recorded two container-start
// timeouts, so the bound went back to the owner (open decision GS-11) and
// the default stays serial until the owner settles it; `--layout
// unit-alone` runs the concurrent layout.
const (
	gateLayoutUnitAlone = "unit-alone"
	gateLayoutSerial    = "serial"
	defaultGateLayout   = gateLayoutSerial
)

// gatePhase is one phase of a layout: its lanes run at the same time, and
// each lane is the tiers it runs in order.
type gatePhase [][]string

// gateLayouts maps each layout name to its phases. Each layout holds every
// tier the gate can run exactly once, which TestGateLayoutsHoldEveryTierOnce
// pins.
var gateLayouts = map[string][]gatePhase{
	gateLayoutUnitAlone: {
		{{tierStatic, tierContract}, {tierComponent, tierIntegration}},
		{{tierUnit}},
		{{tierDocs}},
	},
	gateLayoutSerial: {
		{{tierStatic, tierUnit, tierComponent, tierContract, tierIntegration, tierDocs}},
	},
}

// gateLayoutNames returns the layout names in order, for usage text.
func gateLayoutNames() []string {
	names := make([]string, 0, len(gateLayouts))
	for name := range gateLayouts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// parseGateLayout returns the phases of a named layout, or an error that
// names the layouts the gate knows.
func parseGateLayout(name string) ([]gatePhase, error) {
	phases, ok := gateLayouts[name]
	if !ok {
		return nil, fmt.Errorf("--layout %q is not a gate layout; use one of %s", name, strings.Join(gateLayoutNames(), ", "))
	}
	return phases, nil
}

// gateParallelTiers are the tiers whose `go test -p` the gate can set:
// the tiers it takes from the plan. The static tier runs its own checks,
// and the documentation gates run as one `go test` invocation of their
// own.
var gateParallelTiers = map[string]bool{tierUnit: true, tierComponent: true, tierContract: true, tierIntegration: true}

// parseGoTestP parses `--go-test-p`: a comma-separated list of
// tier=count pairs, such as `component=2,integration=2`. An empty value
// sets nothing, so every tier keeps the go command's default -p, which is
// the number of CPUs the process sees. A tier outside gateParallelTiers, a
// count that is not a whole number of at least 1, and a tier named twice
// are refused.
func parseGoTestP(value string) (map[string]int, error) {
	out := map[string]int{}
	if strings.TrimSpace(value) == "" {
		return out, nil
	}
	for _, pair := range strings.Split(value, ",") {
		tier, count, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok {
			return nil, fmt.Errorf("--go-test-p %q: want tier=count pairs, such as component=2", pair)
		}
		if !gateParallelTiers[tier] {
			return nil, fmt.Errorf("--go-test-p: %q is not a tier whose go test -p the gate sets; use unit, component, contract, or integration", tier)
		}
		n, err := strconv.Atoi(count)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("--go-test-p: the count for %s is %q; want a whole number of at least 1", tier, count)
		}
		if _, dup := out[tier]; dup {
			return nil, fmt.Errorf("--go-test-p names %s twice", tier)
		}
		out[tier] = n
	}
	return out, nil
}

// formatGoTestP renders a parsed `--go-test-p` value in tier order, so
// the host half passes the same value to the gate container and the
// verdict command reads the same each run.
func formatGoTestP(p map[string]int) string {
	var parts []string
	for _, tier := range allTiers() {
		if n, ok := p[tier]; ok {
			parts = append(parts, fmt.Sprintf("%s=%d", tier, n))
		}
	}
	return strings.Join(parts, ",")
}

// runPhases runs the gate's tiers in the layout's phases and returns
// once every lane of the last phase has ended. The static and
// documentation tiers run through runFixed; every other tier runs from
// its plan, and a tier the plan did not select is skipped. A tier that
// shares its phase with another lane gets the `go test -p` that goTestP
// names for it, if any. A planned tier that no lane of the layout names
// is recorded as not run, so a tier added to the gate's plan without a
// place in every layout keeps the verdict from passing rather than being
// dropped.
func (g gateRunner) runPhases(v *verdict, phases []gatePhase, plans map[string]tierPlan, goTestP map[string]int, runFixed func(name string)) {
	for name := range plans {
		if !layoutHolds(phases, name) {
			g.notRun(v, name, fmt.Errorf("the gate's layout has no lane for the %s tier", name))
		}
	}
	for _, phase := range phases {
		var wg sync.WaitGroup
		for _, lane := range phase {
			wg.Add(1)
			go func(lane []string, overlapping bool) {
				defer wg.Done()
				for _, name := range lane {
					switch name {
					case tierStatic, tierDocs:
						runFixed(name)
					default:
						p, ok := plans[name]
						if !ok {
							continue
						}
						if n := goTestP[name]; overlapping && n > 0 {
							p.goTestP = n
						}
						g.runPlannedTier(v, p)
					}
				}
			}(lane, len(phase) > 1)
		}
		wg.Wait()
	}
}

// layoutHolds reports whether a lane of the phases names the tier.
func layoutHolds(phases []gatePhase, tier string) bool {
	for _, phase := range phases {
		for _, lane := range phase {
			if contains(lane, tier) {
				return true
			}
		}
	}
	return false
}

// layoutOverlaps reports whether the tier shares a phase with another
// lane in the phases, which is when `--go-test-p` applies to it.
func layoutOverlaps(phases []gatePhase, tier string) bool {
	for _, phase := range phases {
		if len(phase) < 2 {
			continue
		}
		for _, lane := range phase {
			if contains(lane, tier) {
				return true
			}
		}
	}
	return false
}

// checkGoTestP refuses a `--go-test-p` count for a tier that never shares
// a phase with another lane in the layout, because the count would never
// apply and the verdict would record a value the run did not use.
func checkGoTestP(layout string, phases []gatePhase, p map[string]int) error {
	for _, tier := range allTiers() {
		if _, ok := p[tier]; ok && !layoutOverlaps(phases, tier) {
			return fmt.Errorf("--go-test-p: the %s tier runs alone in the %s layout, so a go test -p for it would never apply", tier, layout)
		}
	}
	return nil
}

// withGoTestP appends `-p=N` to a tier's go test flags when the plan
// sets a go test -p (gateRunner.runPhases), and returns the flags as they
// are otherwise.
func withGoTestP(flags []string, p tierPlan) []string {
	if p.goTestP <= 0 {
		return flags
	}
	return append(append([]string{}, flags...), "-p="+strconv.Itoa(p.goTestP))
}

// logTierEnd writes the progress line for a tier that ended, naming the
// tier, so the lines of lanes that run at the same time read apart.
func logTierEnd(name, status string, dur time.Duration) {
	fmt.Fprintf(os.Stderr, "lenny-test gate: the %s tier ended: %s after %s\n", name, status, dur.Round(time.Second))
}
