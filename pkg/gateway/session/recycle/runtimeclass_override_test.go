// SPDX-License-Identifier: MIT

package recycle_test

import (
	"context"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/recycle"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// TestRecycleSeamsLabelRuntimeClassThroughTheOverride_spec_16_1 pins that the
// RuntimeClassNameOverrides option of the per-release session-count retirer
// and of the pod inspector reaches the §16.1 runtime_class label of
// lenny_gateway_pod_retirement_total and of the recycle disposition metrics.
// Under an `isolation.runtimeClassNames` override both name the overridden
// RuntimeClass, the name the controller stamps on the pod, and with no
// override both keep the default name.
//
// spec: 16.1 (Metrics) the runtime_class label on the recycle series, 17.5 (the
// per-profile RuntimeClass name override), 5.3 (Isolation Profiles).
//
// diagnosis: a failure means the gateway's recycle series label runtime_class
// by the default profile mapping while the controller's series carry the
// overridden name, so a query grouped by runtime_class splits one class.
func TestRecycleSeamsLabelRuntimeClassThroughTheOverride_spec_16_1(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[isolation.Profile]string
		want      string
	}{
		{name: "override", overrides: isolation.RuntimeClassOverrides("", "runsc", ""), want: "runsc"},
		{name: "no override", overrides: nil, want: "gvisor"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/session-count retirer", func(t *testing.T) {
			pod := agentPod("pod-1", "agents", "true", time.Unix(0, 0))
			cl := fake.NewClientBuilder().WithObjects(pod).Build()
			sink := &recordingSink{}
			pool := perReleasePool("agents", 4, runtimestore.MicrovmScrubStandard, 3, 0)
			pool.RuntimeRef = "rt-1"
			r, err := recycle.NewSessionCountRetirer(recycle.SessionCountRetirerOptions{
				Client:    cl,
				Namespace: testNS,
				Pools:     fakePoolReader{pools: map[string]poolstore.Pool{"agents": pool}},
				Runtimes: fakeRuntimeReader{runtimes: map[string]runtimestore.Runtime{
					"rt-1": {IsolationProfile: isolation.ProfileSandboxed},
				}},
				Metrics:                   sink,
				Now:                       func() time.Time { return time.Unix(100, 0) },
				RuntimeClassNameOverrides: tc.overrides,
			})
			if err != nil {
				t.Fatalf("NewSessionCountRetirer: %v", err)
			}
			if err := r.RetireOnSessionCount(context.Background(), "pod-1", 3); err != nil {
				t.Fatalf("RetireOnSessionCount: %v", err)
			}
			want := "session_count_limit/agents/" + tc.want
			if len(sink.retirements) != 1 || sink.retirements[0] != want {
				t.Errorf("retirements = %v, want one %q", sink.retirements, want)
			}
		})
		t.Run(tc.name+"/pod inspector", func(t *testing.T) {
			now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
			pool := recyclingPool("agents", "rt", "", &runtimestore.RecyclePolicy{
				Enabled: true, MaxSessionsPerPod: 50,
				RuntimeProcess: runtimestore.RuntimeProcessKeep,
			})
			cl := inspectorClient(t, agentPod("pod-1", "agents", "true", now), recyclingClaim("pod-1"))
			insp, err := recycle.NewPodInspector(recycle.PodInspectorOptions{
				Client: cl, Namespace: testNS,
				Pools: fakePoolReader{pools: map[string]poolstore.Pool{"agents": pool}},
				Runtimes: fakeRuntimeReader{runtimes: map[string]runtimestore.Runtime{
					"rt": {Name: "rt", IsolationProfile: isolation.ProfileSandboxed},
				}},
				Now:                       func() time.Time { return now },
				RuntimeClassNameOverrides: tc.overrides,
			})
			if err != nil {
				t.Fatalf("NewPodInspector: %v", err)
			}
			policy, found, err := insp.InspectForRecycle(context.Background(), "pod-1")
			if err != nil || !found {
				t.Fatalf("InspectForRecycle = (found=%v, err=%v), want (true, nil)", found, err)
			}
			if policy.RuntimeClass != tc.want {
				t.Errorf("RuntimeClass = %q, want %q", policy.RuntimeClass, tc.want)
			}
		})
	}
}
