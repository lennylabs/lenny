// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local ordering coverage for the CH-MSGSOCK session frames.
//
// Every start of a session writes its session_start inside an open sequence
// that runs under the slot serialization, and every teardown of a running
// session writes its session_end on the rule-8 record. Under those rules no
// attempt's session_end reaches the runtime after a later attempt's
// session_start for the same session, and every session_start is followed
// by that session's session_end. The case below races starts against
// unconditional Shutdowns of one session identifier under -race and checks
// the frame order the runtime received.
//
// spec: §28.5.3 (CH-MSGSOCK, Session frame writes), §5.2 (slot-identifier
// reclaim hold), §4.7.1 (role and gateway RPC contract).
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// frameOrderRuntime records the type of every frame the adapter writes, in
// the order the shared connection would carry them. Start yields so a
// racing teardown can interleave with a start that is making the runtime
// live.
type frameOrderRuntime struct {
	mu    sync.Mutex
	types []string
}

func (r *frameOrderRuntime) Start(context.Context, string) error {
	time.Sleep(50 * time.Microsecond)
	return nil
}

func (r *frameOrderRuntime) WriteEnvelope(_ string, envelope []byte) error {
	var probe struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(envelope, &probe)
	r.mu.Lock()
	r.types = append(r.types, probe.Type)
	r.mu.Unlock()
	return nil
}

func (r *frameOrderRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *frameOrderRuntime) Interrupt(context.Context, string, bool) error { return nil }

func (r *frameOrderRuntime) Close(context.Context, string) error { return nil }

func (r *frameOrderRuntime) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.types...)
}

// spec: 28.5.3 (CH-MSGSOCK, Session frame writes), 5.2 (slot-identifier
//
//	reclaim hold), 4.7.1 (role and gateway RPC contract, rule 8)
//
// diagnosis: the runtime received two session_start frames for one session
//
//	with no session_end between them, a session_end with no session_start
//	open, or a session_start never followed by a session_end. Either the
//	open sequence no longer runs under the slot serialization, or a
//	teardown decided its session_end outside the rule-8 record, so a stale
//	attempt's session_end can release a later attempt's context on the
//	runtime, or a session's context leaks on a runtime that lives as long
//	as the pod.
func TestSessionFramesAlternateUnderStartShutdownRace_spec_28_5_3(t *testing.T) {
	const (
		sessionID = "sess-race"
		rounds    = 100
		workers   = 4
	)
	rt := &frameOrderRuntime{}
	base := t.TempDir()
	s := adapter.New("session-frame-order-race")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.Runtime = rt

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	shutdown := &adapterv1.ShutdownRequest{
		SessionId:             &adapterv1.SessionId{Value: sessionID},
		UnconditionalTeardown: true,
	}
	// Each worker starts the session and tears it down in turn, so starts
	// from one worker race teardowns from the others at every step of the
	// open sequence.
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				// A start refused by a standing entry or an open reclaim
				// hold is an expected outcome of the race.
				_, _ = s.StartSession(ctx, &adapterv1.StartSessionRequest{
					SessionId: &adapterv1.SessionId{Value: sessionID},
				})
				if _, err := s.Shutdown(ctx, shutdown); err != nil {
					t.Errorf("Shutdown round %d: %v", i, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if _, err := s.Shutdown(ctx, shutdown); err != nil {
		t.Fatalf("final Shutdown: %v", err)
	}

	types := rt.snapshot()
	open := false
	starts := 0
	for i, typ := range types {
		switch typ {
		case "session_start":
			if open {
				t.Fatalf("frame %d: a second session_start with no session_end between: %v", i, types)
			}
			open = true
			starts++
		case "session_end":
			if !open {
				t.Fatalf("frame %d: a session_end with no session_start open: %v", i, types)
			}
			open = false
		default:
			t.Fatalf("frame %d: unexpected frame type %q", i, typ)
		}
	}
	if open {
		t.Fatalf("the last session_start was never followed by a session_end: %v", types)
	}
	if starts == 0 {
		t.Fatal("no start won the race; the case exercised nothing")
	}
	t.Logf("%d starts opened and ended across %d rounds on %d workers", starts, rounds, workers)
}
