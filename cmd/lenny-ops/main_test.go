// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// TestNoopElectorNeverLeads confirms the §25.4 fallback Elector — used
// when lenny-ops has no Kubernetes connection — never grants
// leadership, so the leader-only background loops stay idle.
func TestNoopElectorNeverLeads(t *testing.T) {
	var elector opsservice.Elector = noopElector{}
	if elector.IsLeader() {
		t.Error("noopElector.IsLeader() = true, want false")
	}

	started := false
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		elector.Run(
			ctx,
			func(context.Context) { started = true },
			func() {},
		)
		close(done)
	}()
	// Run must block until the context is cancelled and never invoke the
	// leader-acquired callback.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("noopElector.Run did not return after context cancellation")
	}
	if started {
		t.Error("noopElector invoked the leader-acquired callback, want never")
	}
}

// TestEmptySourcesYieldNothing confirms the §25.5 cold-start sources —
// used until the Redis stream and subscription cache are wired — yield
// no events and no subscriptions, so the webhook worker delivers
// nothing rather than panicking.
func TestEmptySourcesYieldNothing(t *testing.T) {
	events, err := emptyEventSource{}.Poll(context.Background())
	if err != nil || len(events) != 0 {
		t.Errorf("emptyEventSource.Poll = %v, %v; want no events, nil", events, err)
	}
	if subs := (emptySubscriptionSource{}).Subscriptions(); len(subs) != 0 {
		t.Errorf("emptySubscriptionSource.Subscriptions = %v, want none", subs)
	}
}

// TestEnvHelpers covers the flag-default helpers.
func TestEnvHelpers(t *testing.T) {
	t.Setenv("LENNY_OPS_TEST_STR", "value")
	if got := envOr("LENNY_OPS_TEST_STR", "fallback"); got != "value" {
		t.Errorf("envOr with a set var = %q, want value", got)
	}
	if got := envOr("LENNY_OPS_TEST_UNSET", "fallback"); got != "fallback" {
		t.Errorf("envOr with an unset var = %q, want fallback", got)
	}

	t.Setenv("LENNY_OPS_TEST_INT", "4096")
	if got := envInt64("LENNY_OPS_TEST_INT", 0); got != 4096 {
		t.Errorf("envInt64 with a valid var = %d, want 4096", got)
	}
	t.Setenv("LENNY_OPS_TEST_BADINT", "not-a-number")
	if got := envInt64("LENNY_OPS_TEST_BADINT", 99); got != 99 {
		t.Errorf("envInt64 with a malformed var = %d, want the fallback 99", got)
	}
}
