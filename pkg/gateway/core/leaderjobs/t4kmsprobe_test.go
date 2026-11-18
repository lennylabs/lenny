// SPDX-License-Identifier: MIT

package leaderjobs_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/gateway/core/leaderjobs"
)

// countingTenants is a T4 tenant source with no T4 tenant that counts the
// probe cycles that list it.
type countingTenants struct{ calls atomic.Int32 }

func (c *countingTenants) T4Tenants(context.Context) ([]string, error) {
	c.calls.Add(1)
	return nil, nil
}

// spec: §12.5 (Artifact Store)
func TestAddT4KMSProbeRunsTheProbeWhileTheLeadershipLasts(t *testing.T) {
	var s leaderjobs.Set
	tenants := &countingTenants{}
	reg := prometheus.NewRegistry()
	if err := s.AddT4KMSProbe(leaderjobs.T4KMSProbe{Tenants: tenants, Registerer: reg, Interval: time.Hour, RateLimit: 1, Now: time.Now}); err != nil {
		t.Fatalf("AddT4KMSProbe: %v", err)
	}
	if got := s.Names(); len(got) != 1 || got[0] != "t4-kms-probe" {
		t.Fatalf("Names() = %q, want [t4-kms-probe]", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for tenants.calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the probe ran no cycle on acquiring leadership")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the probe did not stop when the leadership context ended")
	}
}
