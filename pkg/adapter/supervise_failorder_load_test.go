// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage of the order between a CH-SUPERVISE link
// failure and the not-live state during the whole-pod scrub: the failure
// handler is held back by a randomized interval after the read loop detects a
// closed connection, and the scrub's report and a start issued meanwhile must
// still treat the runtime as not live.
//
// spec: §4.7.10 (Runtime supervisor, Liveness), §5.2 (Supervised scrub
// sequence; Pod retirement policy, Runtime not live), §28.5.3 (CH-SUPERVISE,
// Degradation).
package adapter

import (
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// linkFailureScrubRounds is the number of randomized handler delays each
// lifetime runs.
const linkFailureScrubRounds = 6

// spec: 4.7.10 (Runtime supervisor, Liveness), 5.2 (Supervised scrub
// sequence; Pod retirement policy, Runtime not live)
// diagnosis: a link failure the read loop detected during the scrub released
// the scrub's request before the not-live state was set, so ReportPodScrub
// carried runtime_live true for a pod whose supervisor was gone.
func TestScrubReportsNotLiveWhenTheLinkFailureHandlerLags_spec_4_7_10(t *testing.T) {
	checkNoGoroutineLeak(t)
	cases := map[RuntimeLifetime]string{
		// The first end of a restart pod's scrub meets a closed connection.
		LifetimeRestart: superviseEnd,
		// The cleanup of a keep pod's scrub meets a closed connection.
		LifetimeKeep: superviseCleanup,
	}
	for lifetime, failing := range cases {
		for round := 0; round < linkFailureScrubRounds; round++ {
			delay := time.Duration(rand.Intn(150)) * time.Millisecond
			t.Run(string(lifetime), func(t *testing.T) {
				runLaggingLinkFailureScrub(t, lifetime, failing, delay)
			})
		}
	}
}

func runLaggingLinkFailureScrub(t *testing.T, lifetime RuntimeLifetime, failing string, delay time.Duration) {
	t.Helper()
	e := newExitsFixture(t, lifetime, LaunchSession)
	delayLinkFailureHandler(e.rt, e.link, delay)
	e.sup.react(failing, fakeReaction{closeConn: true})
	e.s.ScrubOps = nopScrubOps{}
	reporter := &recordingPodScrubReporter{}
	e.s.PodScrubReporter = reporter
	done := make(chan struct{})
	e.s.SetScrubDoneHook(func() { close(done) })

	// A start issued once the link has failed, while the handler is held
	// back, is refused.
	startErr := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for !e.link.Failed() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		startErr <- e.rt.Start(ctx, "b")
	}()

	e.s.startPodScrub(&adapterv1.RecycleScrub{PodId: "pod-lag", CleanupCommands: []string{"/bin/true"}, CleanupTimeoutSeconds: 1})
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the scrub did not finish")
	}
	reports := reporter.snapshot()
	if len(reports) != 1 {
		t.Fatalf("ReportPodScrub calls = %d, want 1", len(reports))
	}
	if reports[0].runtimeLive {
		t.Fatalf("handler delay %s: ReportPodScrub carried runtime_live true after a link failure", delay)
	}
	if err := <-startErr; !errors.Is(err, errRuntimeNotLive) {
		t.Fatalf("handler delay %s: start after the link failed = %v, want errRuntimeNotLive", delay, err)
	}
	if nl, cause := e.rt.NotLive(); !nl || cause != CauseLinkFailure {
		t.Fatalf("not-live = %v (%s), want the link-failure cause", nl, cause)
	}
}
