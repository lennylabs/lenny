// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// livenessRuntime is a recycleRuntime that also reports whether it serves the
// pod's next session, so a test can set the liveness the scrub driver samples.
type livenessRuntime struct {
	recycleRuntime
	liveMu sync.Mutex
	live   bool
}

func (r *livenessRuntime) ServesNextSession() bool {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	return r.live
}

func (r *livenessRuntime) setLive(live bool) {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	r.live = live
}

// endingOnClearOps is a scrub Ops double whose step 4 scratch clear ends the
// runtime, standing in for a sidecar runtime that exits when the /tmp and
// /dev/shm mounts it shares with the adapter are cleared.
type endingOnClearOps struct {
	*fakePodScrubOps
	rt *livenessRuntime
}

func (o endingOnClearOps) ClearContents(dir string) error {
	o.rt.setLive(false)
	return o.fakePodScrubOps.ClearContents(dir)
}

// recycleOnce starts a session on s, recycles it under pod "pod-live", waits
// for the async scrub, and returns the single report it emitted.
func recycleOnce(t *testing.T, s *Server, reporter *recordingPodScrubReporter, done <-chan struct{}) podScrubReport {
	t.Helper()
	startRecycleSession(t, s, "sess-1")
	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		UnconditionalTeardown: true,
		SessionId:             &adapterv1.SessionId{Value: "sess-1"},
		Recycle:               &adapterv1.RecycleScrub{PodId: "pod-live"},
	}); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	waitScrubDone(t, done)
	reports := reporter.snapshot()
	if len(reports) != 1 {
		t.Fatalf("ReportPodScrub calls = %d, want exactly 1", len(reports))
	}
	if reports[0].outcome != gatewaycontrol.PodScrubSucceeded {
		t.Fatalf("outcome = %v, want PodScrubSucceeded", reports[0].outcome)
	}
	return reports[0]
}

// TestPodScrubReportsRuntimeLiveFromTheRuntime_spec_5_2 asserts that the
// whole-pod scrub report carries runtime_live as the runtime states it: true
// for a runtime that serves the next session, false for one that does not, and
// false for a runtime that implements no liveness method, which retires the pod
// rather than placing the next session on a runtime nothing confirmed.
//
// spec: 5.2 (Pod retirement policy), 4.7 (ReportPodScrub)
func TestPodScrubReportsRuntimeLiveFromTheRuntime_spec_5_2(t *testing.T) {
	cases := []struct {
		name string
		rt   RuntimeProcess
		want bool
	}{
		{name: "live_runtime", rt: &livenessRuntime{live: true}, want: true},
		{name: "ended_runtime", rt: &livenessRuntime{live: false}, want: false},
		{name: "runtime_without_method", rt: &recycleRuntime{}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, reporter, _, done := recycleServer(t)
			s.Runtime = tc.rt
			if got := recycleOnce(t, s, reporter, done).runtimeLive; got != tc.want {
				t.Errorf("reported runtime_live = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPodScrubSamplesRuntimeLiveAfterTheScrub_spec_5_2 asserts the liveness
// sample follows the scrub: a runtime that is live when the recycle starts and
// ends while step 4 clears the scratch mounts it shares is reported as not
// live. A sample taken before the scrub would report it live and the gateway
// would reserve a pod whose next session cannot start.
//
// spec: 5.2 (Pod retirement policy), 4.7 (ReportPodScrub)
func TestPodScrubSamplesRuntimeLiveAfterTheScrub_spec_5_2(t *testing.T) {
	s, reporter, ops, done := recycleServer(t)
	rt := &livenessRuntime{live: true}
	s.Runtime = rt
	s.ScrubOps = endingOnClearOps{fakePodScrubOps: ops, rt: rt}
	if got := recycleOnce(t, s, reporter, done).runtimeLive; got {
		t.Error("reported runtime_live = true for a runtime the scrub ended, want false")
	}
}

// TestRuntimeServesNextSessionNilRuntimeIsNotLive_spec_5_2 asserts that a
// server with no runtime wired reports not live (fail closed).
//
// spec: 5.2 (Pod retirement policy)
func TestRuntimeServesNextSessionNilRuntimeIsNotLive_spec_5_2(t *testing.T) {
	s := New("test")
	if s.runtimeServesNextSession() {
		t.Error("runtimeServesNextSession() = true with no runtime, want false")
	}
}

// TestInProcessRuntimeServesNextSessionAfterClose_spec_5_2 asserts the
// embedded runtime's liveness: it cannot serve the next session while a
// session is bound, and it can once that session's Close unbinds it, because
// the loop runs in the adapter process that lives as long as the pod.
//
// spec: 5.2 (Pod retirement policy), 4.7.10 (Runtime process lifetime)
func TestInProcessRuntimeServesNextSessionAfterClose_spec_5_2(t *testing.T) {
	// The loop drains its input until Close closes the pipe.
	rt := NewInProcessRuntime(func(_ context.Context, in io.Reader, _ io.Writer) error {
		_, err := io.Copy(io.Discard, in)
		return err
	})
	if !rt.ServesNextSession() {
		t.Error("ServesNextSession() = false before any session, want true")
	}
	if err := rt.Start(context.Background(), "sess-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if rt.ServesNextSession() {
		t.Error("ServesNextSession() = true while sess-1 is bound, want false")
	}
	if err := rt.Close(context.Background(), "sess-1"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !rt.ServesNextSession() {
		t.Error("ServesNextSession() = false after Close, want true")
	}
	var sdk RuntimeProcess = NewSDKWarmInProcessRuntime(nil)
	if _, ok := sdk.(nextSessionRuntime); !ok {
		t.Error("SDKWarmInProcessRuntime does not inherit ServesNextSession")
	}
}
