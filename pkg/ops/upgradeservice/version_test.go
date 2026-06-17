// SPDX-License-Identifier: MIT

package upgradeservice_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
)

func staticSource(name, required, current string) upgradeservice.VersionSource {
	return upgradeservice.NewFuncVersionSource(name, required, func(context.Context) (string, error) {
		return current, nil
	})
}

func errSource(name, required string, err error) upgradeservice.VersionSource {
	return upgradeservice.NewFuncVersionSource(name, required, func(context.Context) (string, error) {
		return "", err
	})
}

func componentByName(r upgradeservice.VersionReport, name string) (upgradeservice.ComponentVersion, bool) {
	for _, c := range r.Components {
		if c.Name == name {
			return c, true
		}
	}
	return upgradeservice.ComponentVersion{}, false
}

// spec: §25.8 Version Aggregation — every component at the required
// version reports no drift.
func TestAggregateNoDrift(t *testing.T) {
	gaugeVal := -1
	agg := upgradeservice.NewVersionAggregator(upgradeservice.VersionAggregatorOptions{
		PlatformVersion: "1.5.0",
		Sources: []upgradeservice.VersionSource{
			staticSource("ops", "1.5.0", "1.5.0"),
			staticSource("gateway", "1.5.0", "1.5.0"),
			staticSource("controllers", "1.5.0", "1.5.0"),
		},
		Gauge: func(c int) { gaugeVal = c },
	})
	rep := agg.Aggregate(context.Background())
	if rep.VersionDrift {
		t.Errorf("VersionDrift = true, want false: %+v", rep)
	}
	if rep.DriftCount != 0 || gaugeVal != 0 {
		t.Errorf("DriftCount=%d gauge=%d, want 0/0", rep.DriftCount, gaugeVal)
	}
	if rep.RequiredVersion != "1.5.0" {
		t.Errorf("RequiredVersion = %q, want 1.5.0", rep.RequiredVersion)
	}
}

// spec: §25.8 — a component whose current version differs from required
// is flagged drift and the gauge counts it.
func TestAggregateDriftFlaggedAndCounted(t *testing.T) {
	var gaugeVal int
	agg := upgradeservice.NewVersionAggregator(upgradeservice.VersionAggregatorOptions{
		PlatformVersion: "1.5.0",
		Sources: []upgradeservice.VersionSource{
			staticSource("ops", "1.5.0", "1.5.0"),
			staticSource("gateway", "1.5.0", "1.4.0"),     // behind
			staticSource("controllers", "1.5.0", "1.6.0"), // ahead
		},
		Gauge: func(c int) { gaugeVal = c },
	})
	rep := agg.Aggregate(context.Background())
	if !rep.VersionDrift || rep.DriftCount != 2 || gaugeVal != 2 {
		t.Fatalf("drift=%v count=%d gauge=%d, want true/2/2", rep.VersionDrift, rep.DriftCount, gaugeVal)
	}
	gw, _ := componentByName(rep, "gateway")
	if !gw.Drift || gw.RequiredAction == "" {
		t.Errorf("gateway component = %+v, want drift with requiredAction", gw)
	}
	ops, _ := componentByName(rep, "ops")
	if ops.Drift {
		t.Errorf("ops component should not drift: %+v", ops)
	}
}

// spec: §25.8 Degradation — an unreachable source degrades its component
// to unavailable with a warning and does not fail the report or count as
// drift.
func TestAggregatePartialDegradation(t *testing.T) {
	agg := upgradeservice.NewVersionAggregator(upgradeservice.VersionAggregatorOptions{
		PlatformVersion: "1.5.0",
		Sources: []upgradeservice.VersionSource{
			staticSource("ops", "1.5.0", "1.5.0"),
			errSource("gateway", "1.5.0", errors.New("connection refused")),
		},
	})
	rep := agg.Aggregate(context.Background())
	if rep.VersionDrift || rep.DriftCount != 0 {
		t.Errorf("an unavailable component must not count as drift: %+v", rep)
	}
	gw, ok := componentByName(rep, "gateway")
	if !ok || gw.Available || gw.Error == "" {
		t.Errorf("gateway component = %+v, want unavailable with error", gw)
	}
	if len(rep.DegradationWarnings) != 1 {
		t.Errorf("DegradationWarnings = %v, want one entry", rep.DegradationWarnings)
	}
}

// spec: §25.8 — components are reported in stable (name-sorted) order.
func TestAggregateComponentsSorted(t *testing.T) {
	agg := upgradeservice.NewVersionAggregator(upgradeservice.VersionAggregatorOptions{
		PlatformVersion: "1.5.0",
		Sources: []upgradeservice.VersionSource{
			staticSource("postgres-schema", "", "49"),
			staticSource("controllers", "1.5.0", "1.5.0"),
			staticSource("gateway", "1.5.0", "1.5.0"),
		},
	})
	rep := agg.Aggregate(context.Background())
	want := []string{"controllers", "gateway", "postgres-schema"}
	for i, name := range want {
		if rep.Components[i].Name != name {
			t.Errorf("Components[%d] = %q, want %q", i, rep.Components[i].Name, name)
		}
	}
}

// spec: §25.8 — a source with no required value is reported for
// introspection but never flagged as drift (the postgres-schema case
// until the embedded required-schema constant lands).
func TestAggregateNoRequiredNeverDrifts(t *testing.T) {
	agg := upgradeservice.NewVersionAggregator(upgradeservice.VersionAggregatorOptions{
		PlatformVersion: "1.5.0",
		Sources: []upgradeservice.VersionSource{
			staticSource("postgres-schema", "", "49"),
		},
	})
	rep := agg.Aggregate(context.Background())
	if rep.VersionDrift {
		t.Errorf("a no-required source must not drift: %+v", rep)
	}
	sc, _ := componentByName(rep, "postgres-schema")
	if !sc.Available || sc.Current != "49" || sc.Drift {
		t.Errorf("postgres-schema = %+v, want available current=49 no-drift", sc)
	}
}
