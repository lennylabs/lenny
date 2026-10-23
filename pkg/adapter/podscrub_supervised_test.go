// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/scrub"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// nopScrubOps is a scrub.Ops whose steps succeed, with an optional step-0
// failure.
type nopScrubOps struct{ removeErr error }

func (o nopScrubOps) KillUserProcesses(context.Context) error { return nil }
func (o nopScrubOps) PurgeIPCShm(context.Context) error       { return nil }
func (o nopScrubOps) RemoveAll(string) error                  { return o.removeErr }
func (o nopScrubOps) ClearContents(string) error              { return nil }
func (o nopScrubOps) PathState(string) (bool, bool, error)    { return false, true, nil }

// scrubCell is one cell of the Supervised scrub sequence table.
type scrubCell struct {
	// reactions are queued on the fake before the scrub, by request type, in
	// the order the requests of that type arrive.
	reactions map[string][]fakeReaction
	step0Err  bool
	// credEnumFails makes the per-slot credentials container unreadable,
	// so step 0 cannot enumerate the credential files.
	credEnumFails bool
	shortCtx      bool
	// rebindFails makes the listener rebind fail.
	rebindFails bool
	wantSent    []string
	wantResult  scrub.Result
	wantCause   LivenessCause
}

var fullSequence = []string{superviseEnd, superviseSweep, superviseCleanup, superviseEnd, superviseSweep}

// spec: 5.2 (Supervised scrub sequence), 4.7.10 (Runtime supervisor, End;
// Runtime supervisor, Sweep; Runtime supervisor, Cleanup; Runtime
// supervisor, Liveness)
func TestSupervisedScrubSequenceCells_spec_5_2(t *testing.T) {
	ok := fakeReaction{}
	errEnd := fakeReaction{errCode: SuperviseErrEndDeadline}
	silent := fakeReaction{silent: true}
	closed := fakeReaction{closeConn: true}
	cells := map[string]scrubCell{
		"all rows succeed": {wantSent: fullSequence, wantResult: scrub.Succeeded},
		"step 0 error skips cleanupCommands": {
			step0Err: true, wantSent: []string{superviseEnd, superviseSweep, superviseEnd, superviseSweep}, wantResult: scrub.Failed,
		},
		"step 0 enumeration failure skips cleanupCommands": {
			credEnumFails: true, wantSent: []string{superviseEnd, superviseSweep, superviseEnd, superviseSweep}, wantResult: scrub.Failed,
		},
		"first end error":          {reactions: map[string][]fakeReaction{superviseEnd: {errEnd}}, wantSent: fullSequence, wantResult: scrub.Succeeded, wantCause: CauseReplyError},
		"first end no reply":       {reactions: map[string][]fakeReaction{superviseEnd: {silent}}, shortCtx: true, wantSent: []string{superviseEnd}, wantResult: scrub.Succeeded, wantCause: CauseLinkFailure},
		"first end closed":         {reactions: map[string][]fakeReaction{superviseEnd: {closed}}, wantSent: []string{superviseEnd}, wantResult: scrub.Succeeded, wantCause: CauseLinkFailure},
		"first sweep error":        {reactions: map[string][]fakeReaction{superviseSweep: {{errCode: SuperviseErrSweepFailed}}}, wantSent: fullSequence, wantResult: scrub.Succeeded, wantCause: CauseReplyError},
		"first sweep bound":        {reactions: map[string][]fakeReaction{superviseSweep: {{errCode: SuperviseErrSweepBound}}}, wantSent: fullSequence, wantResult: scrub.Succeeded, wantCause: CauseReplyError},
		"first sweep closed":       {reactions: map[string][]fakeReaction{superviseSweep: {closed}}, wantSent: []string{superviseEnd, superviseSweep}, wantResult: scrub.Succeeded, wantCause: CauseLinkFailure},
		"cleanup failed":           {reactions: map[string][]fakeReaction{superviseCleanup: {{errCode: SuperviseErrCleanupFailed}}}, wantSent: fullSequence, wantResult: scrub.Failed},
		"cleanup timeout":          {reactions: map[string][]fakeReaction{superviseCleanup: {{errCode: SuperviseErrCleanupTimeout}}}, wantSent: fullSequence, wantResult: scrub.Failed},
		"cleanup no reply":         {reactions: map[string][]fakeReaction{superviseCleanup: {silent}}, shortCtx: true, wantSent: []string{superviseEnd, superviseSweep, superviseCleanup}, wantResult: scrub.Succeeded, wantCause: CauseLinkFailure},
		"cleanup closed":           {reactions: map[string][]fakeReaction{superviseCleanup: {closed}}, wantSent: []string{superviseEnd, superviseSweep, superviseCleanup}, wantResult: scrub.Succeeded, wantCause: CauseLinkFailure},
		"second end error":         {reactions: map[string][]fakeReaction{superviseEnd: {ok, errEnd}}, wantSent: fullSequence, wantResult: scrub.Succeeded, wantCause: CauseReplyError},
		"second end closed":        {reactions: map[string][]fakeReaction{superviseEnd: {ok, closed}}, wantSent: []string{superviseEnd, superviseSweep, superviseCleanup, superviseEnd}, wantResult: scrub.Succeeded, wantCause: CauseLinkFailure},
		"second sweep error":       {reactions: map[string][]fakeReaction{superviseSweep: {ok, {errCode: SuperviseErrSweepFailed}}}, wantSent: fullSequence, wantResult: scrub.Succeeded, wantCause: CauseReplyError},
		"second sweep bound":       {reactions: map[string][]fakeReaction{superviseSweep: {ok, {errCode: SuperviseErrSweepBound}}}, wantSent: fullSequence, wantResult: scrub.Succeeded, wantCause: CauseReplyError},
		"second sweep no reply":    {reactions: map[string][]fakeReaction{superviseSweep: {ok, silent}}, shortCtx: true, wantSent: fullSequence, wantResult: scrub.Succeeded, wantCause: CauseLinkFailure},
		"listener rebind error":    {rebindFails: true, wantSent: fullSequence, wantResult: scrub.Succeeded, wantCause: CauseReplyError},
		"cleanup failed and later": {reactions: map[string][]fakeReaction{superviseCleanup: {{errCode: SuperviseErrCleanupFailed}}, superviseEnd: {ok, errEnd}}, wantSent: fullSequence, wantResult: scrub.Failed, wantCause: CauseReplyError},
	}
	for name, c := range cells {
		for _, exitedDuring := range []bool{false, true} {
			label := name
			if exitedDuring {
				label += "/exited during the first row"
			}
			t.Run(label, func(t *testing.T) {
				runScrubCell(t, c, exitedDuring)
			})
		}
	}
}

func runScrubCell(t *testing.T, c scrubCell, exitedDuring bool) {
	t.Helper()
	e := newExitsFixture(t, LifetimeRestart, LaunchSession)
	for typ, rs := range c.reactions {
		for _, r := range rs {
			e.sup.react(typ, r)
		}
	}
	if exitedDuring {
		// The runtime exits on its own before the scrub's first end: the
		// exit takes its Runtime exits row and changes no row of the scrub.
		e.sup.writeLine(`{"type":"exited","gen":1,"status":0}`)
		waitFor(t, "the exit to be handled", func() bool { nl, _ := e.rt.NotLive(); return nl })
	}
	if c.rebindFails {
		e.rt.newTransport = func() (*SocketRuntimeProcess, error) { return nil, errors.New("bind refused") }
	}
	e.s.ScrubOps = nopScrubOps{}
	if c.step0Err {
		e.s.ScrubOps = nopScrubOps{removeErr: errors.New("unremovable")}
	}
	if c.credEnumFails {
		e.s.CredentialsDir = unreadableSlotsRoot(t)
	}
	cfg, _ := e.s.scrubConfig(&adapterv1.RecycleScrub{CleanupCommands: []string{"/bin/true"}, CleanupTimeoutSeconds: 1})
	if c.step0Err {
		cfg.CredentialFiles = []string{"/nonexistent/credentials.json"}
	}
	ctx := context.Background()
	if c.shortCtx {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
	}
	rep, err := scrub.Run(ctx, e.s.ScrubOps, cfg)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	// The fixture's own launch precedes the scrub's requests.
	want := append([]string{superviseLaunch}, c.wantSent...)
	if got := e.sup.sent(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	if rep.Result != c.wantResult {
		t.Fatalf("scrub result = %v, want %v (steps %+v)", rep.Result, c.wantResult, rep.Steps)
	}
	wantCause := c.wantCause
	if exitedDuring {
		wantCause = CauseRuntimeExit
	}
	nl, cause := e.rt.NotLive()
	if (wantCause != "") != nl || (nl && cause != wantCause) {
		t.Fatalf("not-live = %v (%s), want %q", nl, cause, wantCause)
	}
	if live := e.s.runtimeServesNextSession(); live != (wantCause == "") {
		t.Fatalf("runtime_live = %v, want %v", live, wantCause == "")
	}
}

// spec: 5.2 (Supervised scrub sequence; Lenny scrub procedure)
func TestSupervisedScrubOrderOnAKeepPod_spec_5_2(t *testing.T) {
	e := newExitsFixture(t, LifetimeKeep, LaunchSession)
	e.s.ScrubOps = nopScrubOps{}
	cfg, _ := e.s.scrubConfig(&adapterv1.RecycleScrub{CleanupCommands: []string{"/bin/true"}, CleanupTimeoutSeconds: 1})
	if cfg.EndRuntime != nil || cfg.EndRuntimeAgain != nil || cfg.RebindListeners != nil {
		t.Fatal("a keep pod's scrub ends the runtime process")
	}
	rep, err := scrub.Run(context.Background(), e.s.ScrubOps, cfg)
	if err != nil || rep.Result != scrub.Succeeded {
		t.Fatalf("scrub = %+v, %v", rep, err)
	}
	if got := e.sup.sent(); !reflect.DeepEqual(got, []string{superviseLaunch, superviseCleanup}) {
		t.Fatalf("requests = %v, want only cleanup", got)
	}
	if !e.s.runtimeServesNextSession() {
		t.Fatal("the kept process must serve the next session")
	}
}

// spec: 28.5.3 (CH-SUPERVISE, Timing)
func TestScrubDeadlineExceedsTheCleanupRequestBound_spec_28_5_3(t *testing.T) {
	for _, d := range []time.Duration{0, time.Second, 10 * time.Minute} {
		bound := d
		if bound == 0 {
			bound = scrub.DefaultCleanupTimeout
		}
		if got := scrubDeadline(d); got <= bound+superviseCleanupSlack {
			t.Errorf("scrubDeadline(%s) = %s, want more than the cleanup request bound %s", d, got, bound+superviseCleanupSlack)
		}
	}
}

// unreadableSlotsRoot returns a credentials root whose per-slot container is
// a regular file, so enumerating it fails with ENOTDIR on every uid the
// tests run as: a read failure rather than absence.
func unreadableSlotsRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "slots"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write the slots path: %v", err)
	}
	return root
}
