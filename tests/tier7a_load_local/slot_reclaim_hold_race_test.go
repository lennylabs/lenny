// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local ordering coverage for the §5.2 slot-identifier reclaim
// hold and the per-slot guard a removing Shutdown takes.
//
// A Shutdown that removes a slot's registry entry opens the reclaim hold in
// the same critical section, and the hold refuses every admission request
// naming the identifier until the cleanup that reclaims it has completed. A
// section admitted before the hold opened is excluded by the per-slot guard
// instead, which the removing arm of Shutdown takes before it re-decides and
// deregisters. The cases below drive both mechanisms concurrently under
// -race, parking the reclaim inside Runtime.Close or a guarded section
// inside its own work, and assert the refusals, the exclusion and the
// absence of a deadlock between s.mu and the guard.
//
// spec: §5.2 (slot-identifier reclaim hold), §4.7.1 (role and gateway RPC
// contract).
package tier7a_load_local_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// hookRuntime is a RuntimeProcess whose Start and Close run per-call hooks
// and record the interval each call spent inside the runtime, so a case can
// park a section and assert that two guarded sections never overlap.
type hookRuntime struct {
	mu        sync.Mutex
	onStart   func(sessionID string)
	onClose   func(sessionID string)
	intervals []interval
}

// interval is one call's time inside the runtime.
type interval struct {
	what       string
	start, end time.Time
}

func (r *hookRuntime) record(what string, hook func(string), sessionID string) {
	begin := time.Now()
	if hook != nil {
		hook(sessionID)
	}
	r.mu.Lock()
	r.intervals = append(r.intervals, interval{what: what, start: begin, end: time.Now()})
	r.mu.Unlock()
}

func (r *hookRuntime) hooks() (start, closeHook func(string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.onStart, r.onClose
}

func (r *hookRuntime) Start(_ context.Context, sessionID string) error {
	h, _ := r.hooks()
	r.record("start "+sessionID, h, sessionID)
	return nil
}

func (r *hookRuntime) Close(_ context.Context, sessionID string) error {
	_, h := r.hooks()
	r.record("close "+sessionID, h, sessionID)
	return nil
}

func (r *hookRuntime) WriteEnvelope(string, []byte) error { return nil }

func (r *hookRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *hookRuntime) Interrupt(context.Context, string, bool) error { return nil }

func (r *hookRuntime) snapshot() []interval {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]interval(nil), r.intervals...)
}

// reclaimPod builds an adapter with every per-slot root under one temp base.
func reclaimPod(t *testing.T, rt adapter.RuntimeProcess) *adapter.Server {
	t.Helper()
	base := t.TempDir()
	s := adapter.New("reclaim-hold-race")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.Runtime = rt
	return s
}

// slotTreeExists reports whether the slot's workspace directory is on disk.
func slotTreeExists(s *adapter.Server, sessionID string) bool {
	_, err := os.Stat(filepath.Join(s.WorkspaceBase, "slots", sessionID))
	return err == nil
}

// chanPrepareStream is a PrepareWorkspace server stream fed from a channel,
// so a case can hold the call open between two frames. A closed channel is
// the client's end of stream.
type chanPrepareStream struct {
	grpc.ServerStream
	ctx    context.Context
	frames chan *adapterv1.PrepareWorkspaceRequest
}

func (s *chanPrepareStream) Context() context.Context { return s.ctx }

func (s *chanPrepareStream) Recv() (*adapterv1.PrepareWorkspaceRequest, error) {
	f, ok := <-s.frames
	if !ok {
		return nil, io.EOF
	}
	return f, nil
}

func (s *chanPrepareStream) SendAndClose(*adapterv1.PrepareWorkspaceResponse) error { return nil }

// prepareFrameFor is one PrepareWorkspace frame.
func prepareFrameFor(sessionID, token string, midSession bool) *adapterv1.PrepareWorkspaceRequest {
	return &adapterv1.PrepareWorkspaceRequest{
		SessionId:   &adapterv1.SessionId{Value: sessionID},
		BindAttempt: token,
		MidSession:  midSession,
		UploadRef:   "lenny-blob://t/a",
		Chunk:       []byte("x"),
	}
}

// singleFramePrepare runs a one-frame PrepareWorkspace call.
func singleFramePrepare(s *adapter.Server, frame *adapterv1.PrepareWorkspaceRequest) error {
	frames := make(chan *adapterv1.PrepareWorkspaceRequest, 1)
	frames <- frame
	close(frames)
	return s.PrepareWorkspace(&chanPrepareStream{ctx: context.Background(), frames: frames})
}

// assignCreds sends AssignCredentials naming token.
func assignCreds(s *adapter.Server, sessionID, token string) error {
	_, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID}, BindAttempt: token,
	})
	return err
}

// startSession sends StartSession, which carries no token.
func startSession(s *adapter.Server, sessionID string) error {
	_, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID}, Runtime: "echo",
	})
	return err
}

// fencedReclaim sends the fenced Shutdown naming token.
func fencedReclaim(s *adapter.Server, sessionID, token string) (*adapterv1.ShutdownResponse, error) {
	return s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID}, BindAttempt: token,
	})
}

// refusedCallBudget bounds a refused call's wall time. The parked cleanup
// runs for as long as the case holds it, which is far longer, so a call that
// waited for the cleanup before refusing overruns it.
const refusedCallBudget = 2 * time.Second

// spec: §5.2 (slot-identifier reclaim hold); §4.7.1 (role and gateway RPC contract)
//
// diagnosis: a bind naming a slot whose reclaim is still inside its runtime
// close was admitted, or was refused only once the cleanup returned. An
// admission during the cleanup lets a successor materialize onto a tree
// the reclaim is about to remove; a refusal that waits spends the caller's
// budget on the cleanup it cannot join.
func TestSlotIdentifierReclaimHoldRefusesABindUntilTheCleanupReturns_spec_5_2(t *testing.T) {
	arms := []struct {
		name string
		// during is the request driven repeatedly while the cleanup is
		// parked; after is the request driven once it has returned.
		during, after func(s *adapter.Server, id string) error
		afterCode     codes.Code
	}{
		{
			name:      "retry of the bind",
			during:    func(s *adapter.Server, id string) error { return assignCreds(s, id, "attempt-2") },
			after:     func(s *adapter.Server, id string) error { return assignCreds(s, id, "attempt-2") },
			afterCode: codes.OK,
		},
		{
			name: "mid-session upload",
			during: func(s *adapter.Server, id string) error {
				return singleFramePrepare(s, prepareFrameFor(id, "", true))
			},
			after: func(s *adapter.Server, id string) error {
				return singleFramePrepare(s, prepareFrameFor(id, "", true))
			},
			// The reclaim removed the entry, and a mid-session request never
			// creates one.
			afterCode: codes.FailedPrecondition,
		},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			for i := range 5 {
				reclaimHoldIteration(t, arm.during, arm.after, arm.afterCode, i)
			}
		})
	}
}

func reclaimHoldIteration(t *testing.T, during, after func(*adapter.Server, string) error, afterCode codes.Code, i int) {
	t.Helper()
	rt := &hookRuntime{}
	s := reclaimPod(t, rt)
	id := "alice"
	if err := assignCreds(s, id, "attempt-1"); err != nil {
		t.Fatalf("iteration %d: assign: %v", i, err)
	}
	if err := startSession(s, id); err != nil {
		t.Fatalf("iteration %d: start: %v", i, err)
	}
	parked, unpark := make(chan struct{}), make(chan struct{})
	rt.mu.Lock()
	rt.onClose = func(string) {
		close(parked)
		<-unpark
	}
	rt.mu.Unlock()

	var reclaimReturned atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := fencedReclaim(s, id, "attempt-1"); err != nil {
			t.Errorf("iteration %d: reclaim: %v", i, err)
		}
		reclaimReturned.Store(true)
	}()
	<-parked

	for j := range 10 {
		began := time.Now()
		err := during(s, id)
		if took := time.Since(began); took > refusedCallBudget {
			t.Errorf("iteration %d call %d: refused after %v, want well inside the parked cleanup", i, j, took)
		}
		if status.Code(err) != codes.Aborted {
			t.Errorf("iteration %d call %d during the cleanup = %v, want Aborted", i, j, err)
		}
		if reclaimReturned.Load() {
			t.Fatalf("iteration %d: the reclaim returned while its close was parked", i)
		}
	}
	close(unpark)
	<-done
	if slotTreeExists(s, id) {
		t.Errorf("iteration %d: the reclaimed slot's tree survived the cleanup", i)
	}
	if err := after(s, id); status.Code(err) != afterCode {
		t.Errorf("iteration %d: request after the cleanup = %v, want %v", i, err, afterCode)
	}
}

// overlapping reports the first pair of intervals that intersect.
func overlapping(ivs []interval) (interval, interval, bool) {
	for i := range ivs {
		for j := i + 1; j < len(ivs); j++ {
			a, b := ivs[i], ivs[j]
			if a.start.Before(b.end) && b.start.Before(a.end) {
				return a, b, true
			}
		}
	}
	return interval{}, interval{}, false
}

// spec: §5.2 (slot-identifier reclaim hold); §4.7.1 (role and gateway RPC contract)
//
// diagnosis: the guarded sections for one slot identifier deadlocked or
// overlapped. s.mu is never held while a slot guard is acquired and no path
// holds two guards; a deadlock means one of those orders was broken, and an
// overlap means a removing Shutdown ran its close beside a Resume that was
// still inside its start under the same identifier's guard.
func TestGuardedSectionsForOneSlotNeitherDeadlockNorOverlap_spec_5_2(t *testing.T) {
	for i := range 20 {
		rt := &hookRuntime{}
		rt.onStart = func(string) { time.Sleep(time.Millisecond) }
		rt.onClose = func(string) { time.Sleep(time.Millisecond) }
		s := reclaimPod(t, rt)
		id := "alice"
		if err := assignCreds(s, id, "attempt-1"); err != nil {
			t.Fatalf("iteration %d: assign: %v", i, err)
		}
		calls := []func(){
			func() {
				_, _ = s.FinalizeWorkspace(context.Background(), &adapterv1.FinalizeWorkspaceRequest{
					SessionId: &adapterv1.SessionId{Value: id}, BindAttempt: "attempt-1",
					WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1},
				})
			},
			func() {
				_, _ = s.RunSetup(context.Background(), &adapterv1.RunSetupRequest{
					SessionId: &adapterv1.SessionId{Value: id}, BindAttempt: "attempt-1",
				})
			},
			func() { _ = singleFramePrepare(s, prepareFrameFor(id, "attempt-1", false)) },
			func() {
				_, _ = s.Resume(context.Background(), &adapterv1.ResumeRequest{
					SessionId: &adapterv1.SessionId{Value: id}, CheckpointId: "ckpt-1", BindAttempt: "attempt-1",
				})
			},
			func() { _, _ = fencedReclaim(s, id, "attempt-1") },
		}
		start := newRaceStart(len(calls))
		var wg sync.WaitGroup
		for _, call := range calls {
			wg.Add(1)
			go func() {
				defer wg.Done()
				start.arrive()
				call()
			}()
		}
		start.release(t)
		finished := make(chan struct{})
		go func() { wg.Wait(); close(finished) }()
		select {
		case <-finished:
		case <-time.After(30 * time.Second):
			t.Fatalf("iteration %d: the guarded sections deadlocked", i)
		}
		if a, b, ok := overlapping(rt.snapshot()); ok {
			t.Errorf("iteration %d: %s [%v, %v] overlapped %s [%v, %v]", i,
				a.what, a.start, a.end, b.what, b.start, b.end)
		}
	}
}

// spec: §5.2 (slot-identifier reclaim hold); §4.7.1 (role and gateway RPC contract)
//
// diagnosis: after a reclaim of one identifier completed beside a section
// holding that identifier's guard, a later acquirer entered its work while
// the first holder was still inside its own. A guard table that dropped its
// entry on the reclaim hands the later acquirer a fresh channel and loses
// the mutual exclusion.
func TestTheSlotGuardOutlivesACompletedReclaim_spec_5_2(t *testing.T) {
	rt := &hookRuntime{}
	s := reclaimPod(t, rt)
	id := "alice"
	parked, unpark := make(chan struct{}), make(chan struct{})
	var once sync.Once
	rt.onStart = func(string) {
		once.Do(func() {
			close(parked)
			<-unpark
		})
	}
	resumeDone := make(chan error, 1)
	go func() {
		_, err := s.Resume(context.Background(), &adapterv1.ResumeRequest{
			SessionId: &adapterv1.SessionId{Value: id}, CheckpointId: "ckpt-1", BindAttempt: "attempt-1",
		})
		resumeDone <- err
	}()
	<-parked
	reclaimDone := make(chan error, 1)
	go func() {
		_, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: id}, UnconditionalTeardown: true,
		})
		reclaimDone <- err
	}()
	select {
	case <-reclaimDone:
		t.Fatal("the removing Shutdown returned while the Resume held the slot's guard")
	case <-time.After(200 * time.Millisecond):
	}
	close(unpark)
	if err := <-resumeDone; err != nil {
		t.Fatalf("resume: %v", err)
	}
	if err := <-reclaimDone; err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if _, err := s.Resume(context.Background(), &adapterv1.ResumeRequest{
		SessionId: &adapterv1.SessionId{Value: id}, CheckpointId: "ckpt-1", BindAttempt: "attempt-2",
	}); err != nil {
		t.Fatalf("a later resume after the completed reclaim: %v", err)
	}
	if a, b, ok := overlapping(rt.snapshot()); ok {
		t.Errorf("%s overlapped %s", a.what, b.what)
	}
}

// spec: §5.2 (slot-identifier reclaim hold); §4.7.1 (role and gateway RPC contract)
//
// diagnosis: a matching Shutdown removed a slot's tree while a section
// admitted before the reclaim's hold opened was still writing under the
// identifier, or a Shutdown that removes nothing waited on that section.
// The first leaves a half-written tree with no registry entry; the second
// spends the compensation's budget and books a leak against the pod.
func TestAReclaimAgainstASectionAdmittedBeforeTheHoldOpened_spec_5_2(t *testing.T) {
	t.Run("multi-frame PrepareWorkspace", func(t *testing.T) {
		s := reclaimPod(t, &hookRuntime{})
		frames := make(chan *adapterv1.PrepareWorkspaceRequest, 2)
		frames <- prepareFrameFor("alice", "attempt-1", false)
		prepareDone := make(chan error, 1)
		go func() {
			prepareDone <- s.PrepareWorkspace(&chanPrepareStream{ctx: context.Background(), frames: frames})
		}()
		waitForTree(t, s, "alice")

		// A Shutdown naming another attempt removes nothing and takes no
		// guard, so it answers while the upload is still parked.
		began := time.Now()
		resp, err := fencedReclaim(s, "alice", "attempt-other")
		if err != nil || resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED {
			t.Errorf("non-matching reclaim = (%v, %v), want SUPERSEDED", resp, err)
		}
		if took := time.Since(began); took > refusedCallBudget {
			t.Errorf("non-matching reclaim took %v behind the parked upload", took)
		}

		reclaimDone := make(chan *adapterv1.ShutdownResponse, 1)
		go func() {
			r, rerr := fencedReclaim(s, "alice", "attempt-1")
			if rerr != nil {
				t.Errorf("matching reclaim: %v", rerr)
			}
			reclaimDone <- r
		}()
		select {
		case <-reclaimDone:
			t.Fatal("the matching reclaim returned while the upload held the slot's guard")
		case <-time.After(200 * time.Millisecond):
		}
		frames <- prepareFrameFor("alice", "attempt-1", false)
		close(frames)
		if err := <-prepareDone; err != nil {
			t.Fatalf("prepare: %v", err)
		}
		r := <-reclaimDone
		if r.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED {
			t.Errorf("matching reclaim = %v, want RECLAIMED", r.GetSlotReclaim())
		}
		if slotTreeExists(s, "alice") {
			t.Error("a slot tree survived the reclaim once the upload had returned")
		}
	})
	t.Run("Resume parked inside its start", func(t *testing.T) {
		rt := &hookRuntime{}
		s := reclaimPod(t, rt)
		parked, unpark := make(chan struct{}), make(chan struct{})
		var once sync.Once
		rt.onStart = func(string) { once.Do(func() { close(parked); <-unpark }) }
		resumeDone := make(chan error, 1)
		go func() {
			_, err := s.Resume(context.Background(), &adapterv1.ResumeRequest{
				SessionId: &adapterv1.SessionId{Value: "alice"}, CheckpointId: "ckpt-1", BindAttempt: "attempt-1",
			})
			resumeDone <- err
		}()
		<-parked
		reclaimDone := make(chan *adapterv1.ShutdownResponse, 1)
		go func() {
			r, err := fencedReclaim(s, "alice", "attempt-1")
			if err != nil {
				t.Errorf("reclaim: %v", err)
			}
			reclaimDone <- r
		}()
		select {
		case <-reclaimDone:
			t.Fatal("the reclaim returned while the Resume held the slot's guard")
		case <-time.After(200 * time.Millisecond):
		}
		close(unpark)
		if err := <-resumeDone; err != nil {
			t.Fatalf("resume: %v", err)
		}
		if r := <-reclaimDone; r.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED {
			t.Errorf("reclaim = %v, want RECLAIMED", r.GetSlotReclaim())
		}
		if slotTreeExists(s, "alice") {
			t.Error("a slot tree survived the reclaim")
		}
		if a, b, ok := overlapping(rt.snapshot()); ok {
			t.Errorf("%s overlapped %s", a.what, b.what)
		}
	})
}

// waitForTree waits until the slot's workspace directory exists, which is
// when a PrepareWorkspace call has resolved the identifier.
func waitForTree(t *testing.T, s *adapter.Server, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !slotTreeExists(s, sessionID) {
		if time.Now().After(deadline) {
			t.Fatalf("the slot tree for %s never appeared", sessionID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
