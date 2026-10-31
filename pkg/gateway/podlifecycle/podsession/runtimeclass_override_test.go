// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// runscOverrides is the §17.5 override set of a cluster that publishes gVisor
// as `runsc` and Kata as `kata-qemu`, built by the helper the controller and
// the gateway share.
func runscOverrides() map[isolation.Profile]string {
	return isolation.RuntimeClassOverrides("", "runsc", "kata-qemu")
}

// TestBinderLabelsRuntimeClassThroughTheOverride_spec_16_1 pins that under an
// `runtimeClasses.profiles.<profile>.name` override the binder labels runtime_class with
// the overridden RuntimeClass name, the name the controller stamps on the
// pod's runtimeClassName, on both gateway warm-pool series:
// lenny_warmpool_claims_total and the setup_command_failed count of
// lenny_warmpool_warmup_failure_total, on the exclusive and the slot bind. A
// profile the override leaves unset keeps its default name.
//
// spec: 16.1 (Metrics) the warm pod claims and warm-up failure rows, 17.5 (the
// per-profile RuntimeClass name override), 5.3 (Isolation Profiles).
func TestBinderLabelsRuntimeClassThroughTheOverride_spec_16_1(t *testing.T) {
	newAdapter := func(t *testing.T) *adapter.Server {
		srv := adapter.New("adapter-test")
		srv.WorkspaceBase = t.TempDir()
		srv.Runtime = &fakeRuntime{}
		return srv
	}

	t.Run("claims counter", func(t *testing.T) {
		cases := []struct {
			profile isolation.Profile
			want    string
		}{
			{isolation.ProfileSandboxed, "runsc"},
			{isolation.ProfileMicrovm, "kata-qemu"},
			{isolation.ProfileStandard, "runc"},
		}
		for _, tc := range cases {
			t.Run(string(tc.profile), func(t *testing.T) {
				ns := testNamespace(t)
				sb := idleSandbox(ns, "sbx-1", "10.244.1.7")
				sb.Spec.IsolationProfile = string(tc.profile)
				binder := newBinder(ns, k8sClient(t, sb), adapterDialer(t, newAdapter(t)))
				binder.RuntimeClassNameOverrides = runscOverrides()
				var got []string
				binder.ClaimAccepted = func(_, runtimeClass string) { got = append(got, runtimeClass) }
				res, err := binder.Bind(context.Background(), podsession.BindRequest{
					Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
				})
				if err != nil {
					t.Fatalf("Bind: %v", err)
				}
				defer res.Adapter.Close()
				if len(got) != 1 || got[0] != tc.want {
					t.Errorf("claims runtime_class = %v, want [%s]", got, tc.want)
				}
			})
		}
	})

	t.Run("setup failure on the exclusive bind", func(t *testing.T) {
		ns := testNamespace(t)
		sb := idleSandbox(ns, "sbx-1", "10.244.1.7")
		sb.Spec.IsolationProfile = string(isolation.ProfileSandboxed)
		binder := newBinder(ns, k8sClient(t, sb), adapterDialer(t, newAdapter(t)))
		binder.RuntimeClassNameOverrides = runscOverrides()
		_, err := binder.Bind(context.Background(), podsession.BindRequest{
			Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
			Plan: failingSetupPlan(),
		})
		assertSetupFailureRuntimeClass(t, err, "runsc")
	})

	t.Run("setup failure on the slot bind", func(t *testing.T) {
		ns := testNamespace(t)
		c := k8sClient(t, concurrentIdleSandbox(ns, "sbx-1", "10.244.1.7"))
		binder := newSlotBinder(t, c, adapterDialer(t, newAdapter(t)))
		binder.RuntimeClassNameOverrides = runscOverrides()
		_, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
			Pool: testPool, IsolationProfile: string(isolation.ProfileSandboxed),
			SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
			MaxConcurrentSessions: 8, Plan: failingSetupPlan(),
		})
		assertSetupFailureRuntimeClass(t, err, "runsc")
	})
}

// assertSetupFailureRuntimeClass checks that err is a SetupCommandFailure
// labeled with testPool and want.
func assertSetupFailureRuntimeClass(t *testing.T, err error, want string) {
	t.Helper()
	var sf *podsession.SetupCommandFailure
	if !errors.As(err, &sf) {
		t.Fatalf("bind error = %v, want a *SetupCommandFailure", err)
	}
	if sf.Pool != testPool || sf.RuntimeClass != want {
		t.Errorf("failure labels = (%q, %q), want (%q, %s)", sf.Pool, sf.RuntimeClass, testPool, want)
	}
}
