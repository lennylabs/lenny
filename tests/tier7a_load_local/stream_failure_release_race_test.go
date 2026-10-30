// SPDX-License-Identifier: MIT

//go:build load_local

package tier7a_load_local_test

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// streamFailureRaceRuns is how many times the race below runs.
const streamFailureRaceRuns = 12

// spec: 5.2 (Pool Configuration and Execution Modes), 28.5.1
// (Gateway-to-pod)
// diagnosis: an Internal end of a session's CH-ATTACH stream raced against
// the release of its binding on a recycling pool left the two out of step:
// the stream-failure handler ran without the binding being marked crashed,
// the binding was marked without the handler running, or the pod was
// recycled although its binding was marked crashed (or retired although it
// was not). A run that both reports the failure and recycles the pod hands
// a pod whose stream broke to the next session.
func TestStreamFailureRacingReleaseRetiresExactlyMarkedPods_spec_28_5_1(t *testing.T) {
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 1, Pods: streamFailureRaceRuns + 4, Recycling: true})
	r := h.Replicas[0]
	var handled sync.Map
	r.Executor.SetStreamFailureHandler(func(tenantID, sessionID, sandboxName string, cause error) {
		handled.Store(sessionID, true)
		r.Server.ReportAttachStreamFailure(tenantID, sessionID, sandboxName, cause)
	})
	var marked, retired atomic.Int64
	for run := 0; run < streamFailureRaceRuns; run++ {
		id := h.StartSession(r)
		if code, body := h.Send(r, id, "open-stream"); code != http.StatusOK {
			t.Fatalf("run %d: message = %d %v", run, code, body)
		}
		bind, ok := r.Registry.Get(id)
		if !ok {
			t.Fatalf("run %d: no binding", run)
		}
		pod := bind.SandboxName
		race(run%2,
			func() { h.Cluster.PodOf(pod).FailAttach() },
			func() { _ = r.Executor.Release(context.Background(), id, executor.DispositionCompleted) })
		h.Settle()
		time.Sleep(50 * time.Millisecond)
		_, wasHandled := handled.Load(id)
		crashed := bind.Crashed()
		if wasHandled != crashed {
			t.Errorf("run %d: stream-failure handler called = %v, binding marked crashed = %v; want them equal", run, wasHandled, crashed)
		}
		phase, exists, err := h.Cluster.ClaimPhase(context.Background(), pod)
		if err != nil {
			t.Fatalf("run %d: read claim: %v", run, err)
		}
		recycled := exists && phase == string(claimstate.Recycling)
		if crashed && recycled {
			t.Errorf("run %d: the binding was marked crashed and the pod was recycled", run)
		}
		if !crashed && exists && !recycled {
			t.Errorf("run %d: a clean release left the claim %q, want recycling", run, phase)
		}
		if crashed {
			marked.Add(1)
			if !exists {
				retired.Add(1)
			}
		}
	}
	if marked.Load() != retired.Load() {
		t.Errorf("%d runs marked the binding crashed but %d retired the pod", marked.Load(), retired.Load())
	}
	t.Logf("%d of %d runs reported the failure before the release", marked.Load(), streamFailureRaceRuns)
}
