// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// TestStartSetupOutputStoreErrorStillCommitsRunning_spec_7_5 pins the /start
// success exit across a setup-output persist that fails: the store error is
// logged, the persist leaves the row as it was, and the starting → running
// commit that follows still applies. The persist answered 500 before the
// /start path moved through starting.
//
// spec: §7.5 (setup output), §15.1 (/start), §7.2 (starting → running)
func TestStartSetupOutputStoreErrorStillCommitsRunning_spec_7_5(t *testing.T) {
	mem := memstore.New()
	if err := mem.Create(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateStarting,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hooks := &exitHookStore{Store: mem, hooks: []*exitHook{{state: session.StateStarting, mode: hookFailNoCommit}}}
	srv := New(hooks, Options{})
	srv.persistStartSetupOutput(context.Background(), "acme", "s", &podsession.BindResult{
		SetupOutputs: []*adapterv1.SetupCommandOutput{{ExitCode: 1}},
	})
	row, err := mem.Get(context.Background(), "acme", "s")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(row.SetupOutput) != 0 {
		t.Errorf("SetupOutput = %v, want none after the failed persist", row.SetupOutput)
	}
	updated, err := srv.commitStart(context.Background(), "acme", "s")
	if err != nil || updated.State != session.StateRunning {
		t.Fatalf("commitStart = %q, %v; want running", updated.State, err)
	}
}

// TestStartSetupOutputNothingToPersist_spec_7_5 pins the exclusive-pool
// launch, which captures no setup output: the persist writes nothing.
//
// spec: §7.5 (setup output)
func TestStartSetupOutputNothingToPersist_spec_7_5(t *testing.T) {
	hooks := &exitHookStore{Store: memstore.New(), hooks: []*exitHook{{state: session.StateStarting, mode: hookFailNoCommit}}}
	srv := New(hooks, Options{})
	srv.persistStartSetupOutput(context.Background(), "acme", "s", nil)
	srv.persistStartSetupOutput(context.Background(), "acme", "s", &podsession.BindResult{})
	if hooks.hooks[0].fired {
		t.Error("the persist wrote to the store with no setup output")
	}
}
