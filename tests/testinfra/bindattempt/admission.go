// SPDX-License-Identifier: MIT

package bindattempt

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// The cases in this file drive §4.7.1's admission rules, one exported case
// per numbered rule. Each builds its own fixtures over the transport it is
// given.

// PairingRule drives rule 1. A non-mid-session request carrying an empty
// bind_attempt and a mid-session request carrying a non-empty one are each
// answered INVALID_ARGUMENT, and the adapter creates, resolves, stamps and
// changes nothing, whether or not it holds an entry for the slot.
func PairingRule(t *testing.T, transport Transport) {
	malformed := []struct {
		name string
		call func(ctx context.Context, p Pod) error
	}{
		{"PrepareWorkspace without a token", func(ctx context.Context, p Pod) error {
			_, err := p.PrepareWorkspace(ctx, prepareFrame(alice, "", false, "x"))
			return err
		}},
		{"PrepareWorkspace mid-session with a token", func(ctx context.Context, p Pod) error {
			_, err := p.PrepareWorkspace(ctx, prepareFrame(alice, TokenB, true, "x"))
			return err
		}},
		{"FinalizeWorkspace without a token", func(ctx context.Context, p Pod) error {
			_, err := p.FinalizeWorkspace(ctx, finalizeReq(alice, "", false))
			return err
		}},
		{"FinalizeWorkspace mid-session with a token", func(ctx context.Context, p Pod) error {
			_, err := p.FinalizeWorkspace(ctx, finalizeReq(alice, TokenB, true))
			return err
		}},
		{"RunSetup without a token", func(ctx context.Context, p Pod) error {
			_, err := p.RunSetup(ctx, &adapterv1.RunSetupRequest{SessionId: sid(alice)})
			return err
		}},
		{"AssignCredentials without a token", func(ctx context.Context, p Pod) error {
			_, err := p.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{SessionId: sid(alice), Leases: directLease()})
			return err
		}},
		{"Resume without a token", func(ctx context.Context, p Pod) error {
			_, err := p.Resume(ctx, &adapterv1.ResumeRequest{SessionId: sid(alice), CheckpointId: "ckpt-1"})
			return err
		}},
	}
	for _, m := range malformed {
		t.Run(m.name+" with no entry", func(t *testing.T) {
			f := New(t, transport)
			if err := m.call(callCtx(t), f.Pod); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("got %v, want InvalidArgument", err)
			}
			f.wantNoEntry(t, alice)
			f.wantNoTree(t, alice)
		})
		t.Run(m.name+" against an unstarted entry", func(t *testing.T) {
			f := New(t, transport)
			f.assign(t, alice, TokenA, false)
			if err := m.call(callCtx(t), f.Pod); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("got %v, want InvalidArgument", err)
			}
			// A refused request started nothing: a bind-sequence request
			// under the entry's own token is still admitted, which rule 6
			// would refuse on a started session.
			if _, err := f.Pod.RunSetup(callCtx(t), &adapterv1.RunSetupRequest{SessionId: sid(alice), BindAttempt: TokenA}); err != nil {
				t.Errorf("RunSetup under the entry's own token after the refusal = %v, want admitted", err)
			}
			if n := f.Runtime.Closes(alice); n != 0 {
				t.Errorf("runtime closes = %d, want 0", n)
			}
			f.wantOwnedBy(t, alice, TokenA)
		})
	}
}

// ReclaimHold drives rule 2. While the §5.2 reclaim hold keeps an identifier
// held, every admission request for it is refused on ABORTED, without
// waiting on the cleanup, with one answer whichever code site tests the
// hold, and nothing is created or resolved. The rows include the §7.4
// mid-session upload the hold exists to refuse: the cleanup has already
// deregistered the entry, so an adapter that tests rule 3 before rule 2
// answers it FAILED_PRECONDITION, the permanent refusal, where the hold
// requires ABORTED. Once the cleanup completes each bind-sequence request is
// admitted, and each mid-session request reaches rule 3 and is answered
// FAILED_PRECONDITION because no entry stands.
func ReclaimHold(t *testing.T, transport Transport) {
	rows := make([]holdRow, 0, len(admissionRequests())+len(midSessionRequests()))
	for _, r := range admissionRequests() {
		rows = append(rows, holdRow{request: r, afterCleanup: codes.OK})
	}
	for _, r := range midSessionRequests() {
		rows = append(rows, holdRow{request: r, afterCleanup: codes.FailedPrecondition})
	}
	var mu sync.Mutex
	answers := map[string]string{}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			msg := row.drive(t, transport)
			mu.Lock()
			answers[row.name] = msg
			mu.Unlock()
		})
	}
	wantOneAnswer(t, answers)
}

// holdRow is one request ReclaimHold drives through a parked cleanup, with
// the code rule 3 or rule 7 answers it once the cleanup has completed.
type holdRow struct {
	request
	afterCleanup codes.Code
}

// drive refuses the row's request under the reclaim hold and returns the
// refusal message, then checks the refusal changed nothing and the answer
// the request receives once the cleanup completes.
func (row holdRow) drive(t *testing.T, transport Transport) string {
	t.Helper()
	f := New(t, transport)
	f.bindAndStart(t, alice, TokenA)
	pc := f.park(t, alice)
	// The parked cleanup has deregistered the entry and has not yet removed
	// the tree, which it removes only once the runtime close returns. The
	// snapshot is taken now, so a write the refused request makes is seen
	// before the cleanup's own removal would erase it.
	before := f.snapshotTree(t, alice)
	err := callWithin(t, func(ctx context.Context) error { return row.call(ctx, f.Pod, alice, TokenA) })
	if status.Code(err) != codes.Aborted {
		t.Fatalf("%s during the parked cleanup = %v, want Aborted", row.name, err)
	}
	what := row.name + " refused during the cleanup"
	f.wantTreeUnchanged(t, what, alice, before)
	f.finish(t, pc)
	// The Shutdown deregistered the entry before the refused request
	// arrived, so an entry or a tree standing now is one the refused
	// request created and the cleanup did not reach.
	f.wantNoEntry(t, alice)
	f.wantNoTree(t, alice)
	if got := status.Code(row.call(callCtx(t), f.Pod, alice, TokenA)); got != row.afterCleanup {
		t.Errorf("%s after the cleanup completed answered %v, want %v", row.name, got, row.afterCleanup)
	}
	return status.Convert(err).Message()
}

// wantOneAnswer fails the case unless every refusal carried one message.
func wantOneAnswer(t *testing.T, answers map[string]string) {
	t.Helper()
	var first string
	for name, msg := range answers {
		if first == "" {
			first = msg
			continue
		}
		if msg != first {
			t.Errorf("%s was refused with %q, want the one answer %q every admission request receives", name, msg, first)
		}
	}
}

// callWithin runs call and fails the case if it has not returned within
// callTimeout, which is how a handler that waits on the parked cleanup
// rather than refusing shows up.
func callWithin(t *testing.T, call func(ctx context.Context) error) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*callTimeout)
	defer cancel()
	out := make(chan error, 1)
	go func() { out <- call(ctx) }()
	select {
	case err := <-out:
		return err
	case <-time.After(callTimeout):
		t.Fatal("the request blocked on the parked cleanup instead of being answered")
		return nil
	}
}

// MidSessionCreateRule drives rule 3. A mid-session FinalizeWorkspace and a
// mid-session PrepareWorkspace for a session the adapter holds no entry for
// are answered FAILED_PRECONDITION, and neither the registry nor the
// filesystem changes.
func MidSessionCreateRule(t *testing.T, transport Transport) {
	f := New(t, transport)
	if _, err := f.Pod.FinalizeWorkspace(callCtx(t), finalizeReq(alice, "", true)); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("mid-session FinalizeWorkspace with no entry = %v, want FailedPrecondition", err)
	}
	if _, err := f.Pod.PrepareWorkspace(callCtx(t), prepareFrame(alice, "", true, "x")); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("mid-session PrepareWorkspace with no entry = %v, want FailedPrecondition", err)
	}
	f.wantNoEntry(t, alice)
	f.wantNoTree(t, alice)
}

// CreateAndStampRule drives rule 4 through a Resume, the request a stamp
// placed in the workspace handlers alone would miss. The entry the Resume
// creates carries the Resume's token: a Shutdown naming another token
// removes nothing and one naming the Resume's token reclaims it.
func CreateAndStampRule(t *testing.T, transport Transport) {
	f := New(t, transport)
	if _, err := f.Pod.Resume(callCtx(t), &adapterv1.ResumeRequest{SessionId: sid(alice), CheckpointId: "ckpt-1", BindAttempt: TokenA}); err != nil {
		t.Fatalf("Resume creating the entry = %v, want admitted", err)
	}
	f.wantOutcome(t, "a Shutdown naming another attempt", fencedReq(alice, TokenB), superseded)
	f.wantOwnedBy(t, alice, TokenA)
	if n := f.Runtime.Closes(alice); n != 1 {
		t.Errorf("runtime closes = %d, want 1: the reclaim tears down the session the Resume started", n)
	}
}

// AttemptIdentityRule drives rule 5 at every bind-sequence request that
// carries a token. A request naming attempt B against an entry stamped A is
// refused SLOT_BIND_ATTEMPT_SUPERSEDED on ABORTED with CATEGORY_TRANSIENT,
// and the entry, its current directory and its credentials.json survive.
func AttemptIdentityRule(t *testing.T, transport Transport) {
	for _, r := range tokenedRequests() {
		t.Run(r.name, func(t *testing.T) {
			f := New(t, transport)
			f.assign(t, alice, TokenA, true)
			wantSupersededRefusal(t, r.name+" naming another attempt", r.call(callCtx(t), f.Pod, alice, TokenB))
			f.wantTreeIntact(t, alice, true)
			f.wantOwnedBy(t, alice, TokenA)
		})
	}
}

// StartedSessionRule drives rule 6. A non-mid-session bind-sequence request
// against an entry whose session has started is refused
// SLOT_BIND_ALREADY_STARTED on FAILED_PRECONDITION with CATEGORY_PERMANENT
// and the session is untouched; a repeat ConfigureWorkspace for the session
// that started on the pod is admitted.
func StartedSessionRule(t *testing.T, transport Transport) {
	for _, r := range tokenedRequests() {
		t.Run(r.name, func(t *testing.T) {
			f := New(t, transport)
			f.bindAndStart(t, alice, TokenA)
			wantStartedRefusal(t, r.name+" against the started session", r.call(callCtx(t), f.Pod, alice, TokenA))
			f.wantTreeIntact(t, alice, true)
			if n := f.Runtime.Closes(alice); n != 0 {
				t.Errorf("runtime closes = %d, want 0: the refusal tore the session down", n)
			}
			f.wantOwnedBy(t, alice, TokenA)
		})
	}
	t.Run("repeat ConfigureWorkspace", func(t *testing.T) {
		f := New(t, transport)
		f.assign(t, alice, TokenA, true)
		if err := f.configure(t, alice); err != nil {
			t.Fatalf("the SDK-warm start = %v", err)
		}
		if err := f.configure(t, alice); err != nil {
			t.Errorf("the repeat ConfigureWorkspace = %v, want admitted", err)
		}
		f.wantOwnedBy(t, alice, TokenA)
	})
}

// MidSessionUploadOnStartedSession drives rule 6's mid-session exemption: a
// §7.4 upload, PrepareWorkspace and FinalizeWorkspace marked mid_session
// with no token, is admitted onto the started session over the connection
// the successful bind used, and leaves the entry's stamp as it found it.
func MidSessionUploadOnStartedSession(t *testing.T, transport Transport) {
	f := New(t, transport)
	f.bindAndStart(t, alice, TokenA)
	resp, err := f.Pod.PrepareWorkspace(callCtx(t), prepareFrame(alice, "", true, "upload"))
	if err != nil {
		t.Fatalf("mid-session PrepareWorkspace onto the started session = %v, want admitted", err)
	}
	if resp.GetStagedBytes() != int64(len("upload")) {
		t.Errorf("staged bytes = %d, want %d", resp.GetStagedBytes(), len("upload"))
	}
	if _, err := f.Pod.FinalizeWorkspace(callCtx(t), finalizeReq(alice, "", true)); err != nil {
		t.Errorf("mid-session FinalizeWorkspace onto the started session = %v, want admitted", err)
	}
	f.wantOwnedBy(t, alice, TokenA)
}

// AdmitRule drives rule 7. A bind-sequence request carrying the entry's own
// token against an unstarted entry, and a mid-session request carrying no
// token against a started one, are admitted, and each leaves the entry with
// its token exactly as found.
func AdmitRule(t *testing.T, transport Transport) {
	t.Run("own token against an unstarted entry", func(t *testing.T) {
		f := New(t, transport)
		f.assign(t, alice, TokenA, true)
		if _, err := f.Pod.RunSetup(callCtx(t), &adapterv1.RunSetupRequest{SessionId: sid(alice), BindAttempt: TokenA}); err != nil {
			t.Errorf("RunSetup under the entry's own token = %v, want admitted", err)
		}
		if _, err := f.Pod.FinalizeWorkspace(callCtx(t), finalizeReq(alice, TokenA, false)); err != nil {
			t.Errorf("FinalizeWorkspace under the entry's own token = %v, want admitted", err)
		}
		f.wantOutcome(t, "a Shutdown naming another attempt", fencedReq(alice, TokenB), superseded)
		f.wantOwnedBy(t, alice, TokenA)
	})
	t.Run("mid-session request against a started entry", func(t *testing.T) {
		f := New(t, transport)
		f.bindAndStart(t, alice, TokenA)
		if _, err := f.Pod.FinalizeWorkspace(callCtx(t), finalizeReq(alice, "", true)); err != nil {
			t.Errorf("mid-session FinalizeWorkspace = %v, want admitted", err)
		}
		f.wantOwnedBy(t, alice, TokenA)
	})
}

// StartConfirmationRule drives rule 8. An unconditional Shutdown removes the
// entry while the fake runtime is held inside Start. The start is refused on
// ABORTED, the session is taken back off the shared runtime process, the
// rollback re-creates no entry, and no cleanup outcome is reported, because
// the slot never reached running.
func StartConfirmationRule(t *testing.T, transport Transport) {
	f := New(t, transport)
	f.assign(t, alice, TokenA, true)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	f.Runtime.SetOnStart(func(id string) {
		if id == alice {
			close(entered)
			<-release
		}
	})
	started := make(chan error, 1)
	starter, startCtx := f.Dial(t), callCtx(t)
	go func() {
		_, err := starter.StartSession(startCtx, &adapterv1.StartSessionRequest{SessionId: sid(alice), Runtime: "echo"})
		started <- err
	}()
	select {
	case <-entered:
	case <-time.After(callTimeout):
		t.Fatal("StartSession never reached Runtime.Start")
	}
	f.wantOutcome(t, "the unconditional Shutdown during the start", unconditionalReq(alice), reclaimed)
	closesBefore := f.Runtime.Closes(alice)
	once.Do(func() { close(release) })
	if err := <-started; status.Code(err) != codes.Aborted {
		t.Fatalf("StartSession whose entry was removed = %v, want Aborted", err)
	}
	if f.Runtime.Closes(alice) <= closesBefore {
		t.Error("the refused start did not take the session back off the shared runtime process")
	}
	if n := f.Reports.For(alice); n != 0 {
		t.Errorf("session scrub reports = %d, want 0 for a slot that never reached running", n)
	}
	f.wantNoEntry(t, alice)
}

// FirstFrameRule drives rule 9. A PrepareWorkspace whose second frame names
// another token, or marks mid_session where the first did not, is admitted
// on the first frame's values: its bytes extend the first frame's, the stamp
// is the first frame's, and no second entry is created.
func FirstFrameRule(t *testing.T, transport Transport) {
	laterFrames := map[string]*adapterv1.PrepareWorkspaceRequest{
		"a later frame names another token": prepareFrame(alice, TokenB, false, "cd"),
		"a later frame marks mid_session":   prepareFrame(alice, "", true, "cd"),
	}
	for name, later := range laterFrames {
		t.Run(name, func(t *testing.T) {
			f := New(t, transport)
			resp, err := f.Pod.PrepareWorkspace(callCtx(t), prepareFrame(alice, TokenA, false, "ab"), later)
			if err != nil {
				t.Fatalf("PrepareWorkspace = %v, want admitted on the first frame", err)
			}
			if resp.GetStagedBytes() != 4 {
				t.Errorf("staged bytes = %d, want 4: the later frame did not extend the first", resp.GetStagedBytes())
			}
			f.wantOutcome(t, "a Shutdown naming the later frame's token", fencedReq(alice, TokenB), superseded)
			f.wantOwnedBy(t, alice, TokenA)
			f.wantNoEntry(t, alice)
		})
	}
}
