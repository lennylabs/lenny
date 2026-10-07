// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the CH-ATTACH stream-failure
// report.
//
// A stream the adapter ends with INTERNAL is reported on the executor's
// reader goroutine as the session's runtime_crash, and the failure funnel
// releases the session's binding with the failed disposition. In production
// that report can run at the same moment as the session watchdog's expiry,
// whose terminal pipeline releases the same binding, and as a delivery that
// reaches the session while its stream is ending. This case drives the three
// together under the race detector and asserts the invariants a race would
// break: exactly one release reaches the pod, no binding and no cached stream
// survive, the row is in the state of whichever edge won, and no stream
// goroutine outlives the race.
//
// spec: §28.5.1 (Gateway-to-pod, CH-ATTACH Degradation.), §7.3 (Retry and
// Resume), §6.2 (Pod State Machine).
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// streamFailMessage is the message text on which failingAttachAdapter ends
// the session's stream with INTERNAL.
const streamFailMessage = "fail"

// failingAttachAdapter echoes every message as "echo:<text>" and ends the
// stream with INTERNAL on the message streamFailMessage, as an adapter whose
// runtime faulted. It counts the Shutdown each session's release sends.
type failingAttachAdapter struct {
	adapterv1.UnimplementedAdapterServer

	mu        sync.Mutex
	shutdowns map[string]int
}

func (a *failingAttachAdapter) Shutdown(_ context.Context, req *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.shutdowns == nil {
		a.shutdowns = map[string]int{}
	}
	a.shutdowns[req.GetSessionId().GetValue()]++
	return &adapterv1.ShutdownResponse{ExitedCleanly: true}, nil
}

func (a *failingAttachAdapter) shutdownsOf(id string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.shutdowns[id]
}

func (a *failingAttachAdapter) Attach(stream grpc.BidiStreamingServer[adapterv1.AttachRequest, adapterv1.AttachResponse]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}
		var in heldInbound
		if json.Unmarshal(req.GetEnvelopeJson(), &in) != nil || in.Type != "message" {
			continue
		}
		text := ""
		if len(in.Input) > 0 {
			text = in.Input[0].Inline
		}
		if text == streamFailMessage {
			return status.Error(codes.Internal, "scripted runtime fault")
		}
		b, _ := json.Marshal(map[string]any{"type": "response", "text": "echo:" + text})
		if err := stream.Send(&adapterv1.AttachResponse{EnvelopeJson: b}); err != nil {
			return err
		}
	}
}

// errNotRunning stops the simulated watchdog expiry of a row another edge
// has already moved.
var errNotRunning = errors.New("session is no longer running")

// expireLikeWatchdog moves a running row to expired and runs the terminal
// pipeline the watchdog runs, which releases the session's binding. It does
// nothing when the row has already left running.
func expireLikeWatchdog(ctx context.Context, srv *sessionserver.Server, store sessionstore.Store, id string) {
	updated, err := store.Update(ctx, "acme", id, func(r *sessionstore.Session) error {
		if r.State != session.StateRunning {
			return errNotRunning
		}
		r.State = session.StateExpired
		return nil
	})
	if err != nil {
		return
	}
	srv.OnSessionTerminal(ctx, session.StateRunning, updated)
}

// spec: 28.5.1 (Gateway-to-pod), 7.3 (Retry and Resume), 6.2 (Pod State
// Machine)
// diagnosis: a stream-failure report and its failed release raced the
// session watchdog's expiry release and a concurrent delivery. A
// race-detector report means the report path, the release, or the stream
// cache is touched outside its lock; more than one Shutdown means the
// binding was released twice; a surviving binding or a delivered Send means
// a stream outlived both releases; a row in any state other than the winning
// edge's means the losing edge overwrote it; a leaked goroutine means the
// reader or an open outlived the race.
func TestStreamFailureReportRacesWatchdogReleaseAndSend_spec_28_5_1(t *testing.T) {
	fa := &failingAttachAdapter{}
	dial := heldServe(t, fa)
	scheme := k8sruntime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	reg := podsession.NewRegistry()
	pe := executor.NewPodExecutor(reg, &podsession.Binder{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Namespace: "lenny-agents",
	})
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{Executor: pe, PodRegistry: reg})
	pe.SetStreamFailureHandler(srv.ReportAttachStreamFailure)
	ctx := context.Background()

	outcomes := map[session.State]int{}
	for attempt := 0; attempt < raceAttempts; attempt++ {
		id := fmt.Sprintf("sess-report-race-%d", attempt)
		sandbox := "sbx-" + id
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning,
			CoordinationGeneration: 1, PodAssignment: sandbox,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		reg.Put(&podsession.BindResult{SessionID: id, TenantID: "acme", SandboxName: sandbox, Adapter: dial(), CoordinationGeneration: 1})
		if err := heldSend(t, ctx, pe, id, "open"); err != nil {
			t.Fatalf("open the held stream of %s: %v", id, err)
		}

		start := newRaceStart(3)
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			start.arrive()
			// The adapter ends the stream with INTERNAL on this message, and
			// the reader reports the end before the Send returns.
			_ = heldSend(t, ctx, pe, id, streamFailMessage)
		}()
		go func() {
			defer wg.Done()
			start.arrive()
			// The expiry lands at a varying offset from the stream's end, so
			// the attempts reach both orders: the expiry before the report
			// commits, and the expiry after it.
			time.Sleep(time.Duration(attempt%8) * 300 * time.Microsecond)
			expireLikeWatchdog(ctx, srv, store, id)
		}()
		go func() {
			defer wg.Done()
			start.arrive()
			sendCtx, cancel := context.WithTimeout(ctx, heldWait)
			defer cancel()
			_ = heldSend(t, sendCtx, pe, id, "racing")
		}()
		start.release(t)
		wg.Wait()

		row := awaitReportOutcome(t, store, id)
		outcomes[row.State]++
		if row.State == session.StateResumePending &&
			(row.RetryCount != 1 || row.FailureReason != string(session.FailureRuntimeCrash)) {
			t.Fatalf("attempt %d: reported row = retries %d/%q, want 1/runtime_crash", attempt, row.RetryCount, row.FailureReason)
		}
		if _, ok := reg.Get(id); ok {
			t.Fatalf("attempt %d: the binding survived both releases", attempt)
		}
		if n := awaitShutdowns(fa, id); n != 1 {
			t.Fatalf("attempt %d: Shutdowns of %s = %d, want exactly one release reaching the pod", attempt, id, n)
		}
		if _, err := pe.Send(ctx, id, []executor.Message{{Role: "user", Content: "after"}}); err == nil {
			t.Fatalf("attempt %d: a Send after both releases was delivered over a cached stream", attempt)
		}
	}
	t.Logf("winning edges across %d attempts: %v", raceAttempts, outcomes)
	if outcomes[session.StateResumePending] == 0 || outcomes[session.StateExpired] == 0 {
		t.Errorf("winning edges = %v, want the report and the expiry each to win at least once", outcomes)
	}
	heldAssertNoStreamGoroutines(t)
}

// awaitReportOutcome waits for the row to leave running, which the report or
// the watchdog expiry does, and fails when it reaches neither edge's state.
func awaitReportOutcome(t *testing.T, store sessionstore.Store, id string) sessionstore.Session {
	t.Helper()
	deadline := time.Now().Add(heldWait)
	for {
		row, err := store.Get(context.Background(), "acme", id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		switch row.State {
		case session.StateResumePending, session.StateExpired:
			return row
		case session.StateRunning:
		default:
			t.Fatalf("%s reached %s, want resume_pending (report won) or expired (watchdog won)", id, row.State)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s is still running: neither the report nor the expiry moved it", id)
		}
		time.Sleep(time.Millisecond)
	}
}

// awaitShutdowns returns the Shutdown count of id once the release that the
// winning edge issued has reached the adapter, leaving a short window for a
// duplicate to land.
func awaitShutdowns(fa *failingAttachAdapter, id string) int {
	deadline := time.Now().Add(heldWait)
	for fa.shutdownsOf(id) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	return fa.shutdownsOf(id)
}
