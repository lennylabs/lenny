// SPDX-License-Identifier: MIT

package prodsource

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/probe"
)

// TestProbeConnectivity runs the named probes and projects their results
// onto the §25.6 connectivity dependency list, ordered by name with the
// failure detail preserved. spec: §25.6 line 2906. F-25.6.1.
func TestProbeConnectivity_spec_25_6_2906(t *testing.T) {
	c := NewProbeConnectivity(map[string]probe.Func{
		"redis":    func(context.Context) error { return errors.New("dial timeout") },
		"postgres": func(context.Context) error { return nil },
	}, time.Second)
	deps, err := c.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("want 2 deps, got %d", len(deps))
	}
	// Ordered by name: postgres then redis.
	if deps[0].Name != "postgres" || !deps[0].Reachable {
		t.Fatalf("postgres dep wrong: %+v", deps[0])
	}
	if deps[1].Name != "redis" || deps[1].Reachable || deps[1].Detail != "dial timeout" {
		t.Fatalf("redis dep wrong: %+v", deps[1])
	}
}
