// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// failingSetupPlan is a plan whose one setup command exits non-zero, so the
// adapter's RunSetup aborts the bind.
func failingSetupPlan() *adapterv1.WorkspacePlan {
	return &adapterv1.WorkspacePlan{SetupCommands: []*adapterv1.SetupCommand{{Cmd: "exit 3"}}}
}

// A setup-command failure on the exclusive bind and on the slot bind carries
// the pool and the RuntimeClass of the pod the setup ran on, which label the
// gateway's setup_command_failed count of lenny_warmpool_warmup_failure_total.
// The exclusive bind maps the bound Sandbox's isolation profile, and the slot
// bind maps the profile its request carries from the resolved pool.
//
// spec: 16.1 (Metrics), 7.5 (Setup Commands), 5.3 (Isolation Profiles)
// diagnosis: the gateway would count setup_command_failed with an empty pool or runtime_class label, so the counter could not be grouped by pool. Check the SetupCommandFailure construction in bindprepare.go and slotbinder.go.
func TestSetupCommandFailureCarriesPoolAndRuntimeClass_spec_16_1(t *testing.T) {
	newAdapter := func(t *testing.T) *adapter.Server {
		srv := adapter.New("adapter-test")
		srv.WorkspaceBase = t.TempDir()
		srv.Runtime = &fakeRuntime{}
		return srv
	}
	cases := []struct {
		name string
		bind func(t *testing.T) error
	}{
		{name: "exclusive bind", bind: func(t *testing.T) error {
			ns := testNamespace(t)
			sb := idleSandbox(ns, "sbx-1", "10.244.1.7")
			sb.Spec.IsolationProfile = string(isolation.ProfileSandboxed)
			binder := newBinder(ns, k8sClient(t, sb), adapterDialer(t, newAdapter(t)))
			_, err := binder.Bind(context.Background(), podsession.BindRequest{
				Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
				Plan: failingSetupPlan(),
			})
			return err
		}},
		{name: "slot bind", bind: func(t *testing.T) error {
			ns := testNamespace(t)
			c := k8sClient(t, concurrentIdleSandbox(ns, "sbx-1", "10.244.1.7"))
			binder := newSlotBinder(t, c, adapterDialer(t, newAdapter(t)))
			_, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
				Pool: testPool, IsolationProfile: string(isolation.ProfileSandboxed),
				SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
				MaxConcurrentSessions: 8, Plan: failingSetupPlan(),
			})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.bind(t)
			var sf *podsession.SetupCommandFailure
			if !errors.As(err, &sf) {
				t.Fatalf("bind error = %v, want a *SetupCommandFailure", err)
			}
			if sf.Pool != testPool || sf.RuntimeClass != "gvisor" {
				t.Errorf("failure labels = (%q, %q), want (%q, gvisor)", sf.Pool, sf.RuntimeClass, testPool)
			}
		})
	}
}
