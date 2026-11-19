// SPDX-License-Identifier: MIT

package leaderjobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/api/admintoken/reclaimer"
	"github.com/lennylabs/lenny/pkg/gateway/api/leaderjobs"
)

type noSecret struct{}

func (noSecret) Get(context.Context, string, string) (map[string][]byte, bool, error) {
	return nil, false, nil
}

type noRevoke struct{}

func (noRevoke) DurableRevoke(context.Context, string, string, time.Time) error { return nil }

// TestAdminTokenReclaimerBuildsTheSweepInsideTheAPIFront checks that the
// job refuses the inputs the reclaimer refuses, keeps the configured
// cadence, and returns from Run when the leader context ends.
//
// spec: GW-R2
func TestAdminTokenReclaimerBuildsTheSweepInsideTheAPIFront(t *testing.T) {
	cfg := reclaimer.Config{Namespace: "lenny-system", SecretName: "admin-token", Tenant: "platform", Interval: time.Hour}
	if _, err := leaderjobs.NewAdminTokenReclaimer(cfg, nil, noRevoke{}, time.Now); err == nil {
		t.Fatal("NewAdminTokenReclaimer accepted a nil secret reader")
	}
	job, err := leaderjobs.NewAdminTokenReclaimer(cfg, noSecret{}, noRevoke{}, time.Now)
	if err != nil {
		t.Fatalf("NewAdminTokenReclaimer: %v", err)
	}
	if job.Interval() != time.Hour {
		t.Errorf("Interval = %v, want 1h", job.Interval())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { job.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the leader context ended")
	}
}
