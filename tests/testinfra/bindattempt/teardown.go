// SPDX-License-Identifier: MIT

package bindattempt

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// The cases in this file drive §4.7.1's Shutdown rules, one exported case per
// numbered rule, and the cases no single numbered rule states.

// TeardownPairingRule drives rule 10. A Shutdown carrying neither field and
// one carrying both are answered INVALID_ARGUMENT, and the entry, the tree
// and the credential file are intact, with neither teardown run.
func TeardownPairingRule(t *testing.T, transport Transport) {
	malformed := map[string]*adapterv1.ShutdownRequest{
		"neither field": {SessionId: sid(alice)},
		"both fields":   {SessionId: sid(alice), BindAttempt: TokenA, UnconditionalTeardown: true},
	}
	for name, req := range malformed {
		t.Run(name, func(t *testing.T) {
			f := New(t, transport)
			f.bindAndStart(t, alice, TokenA)
			if _, err := f.Pod.Shutdown(callCtx(t), req); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("Shutdown carrying %s = %v, want InvalidArgument", name, err)
			}
			f.wantTreeIntact(t, alice, true)
			if n := f.Runtime.Closes(alice); n != 0 {
				t.Errorf("runtime closes = %d, want 0", n)
			}
			if n := f.Reports.For(alice); n != 0 {
				t.Errorf("session scrub reports = %d, want 0", n)
			}
			f.wantOwnedBy(t, alice, TokenA)
		})
	}
}

// NoEntryRule drives rule 11. A Shutdown of either form for a session the
// adapter holds no entry for answers ABSENT on a successful call reporting a
// clean exit, removes nothing a co-tenant holds, and runs neither teardown.
func NoEntryRule(t *testing.T, transport Transport) {
	forms := map[string]*adapterv1.ShutdownRequest{
		"naming an attempt":        fencedReq(alice, TokenA),
		"unconditional teardown":   unconditionalReq(alice),
		"naming another's attempt": fencedReq(alice, TokenB),
	}
	for name, req := range forms {
		t.Run(name, func(t *testing.T) {
			f := New(t, transport)
			f.bindAndStart(t, bob, TokenA)
			resp := f.wantOutcome(t, "a Shutdown for a session with no entry", req, absent)
			if !resp.GetExitedCleanly() {
				t.Error("an ABSENT outcome reported an unclean exit")
			}
			if n := f.Runtime.Closes(alice) + f.Runtime.Closes(bob); n != 0 {
				t.Errorf("runtime closes = %d, want 0", n)
			}
			if n := f.Reports.For(alice) + f.Reports.For(bob); n != 0 {
				t.Errorf("session scrub reports = %d, want 0", n)
			}
			f.wantTreeIntact(t, bob, true)
			f.wantOwnedBy(t, bob, TokenA)
		})
	}
}

// UnconditionalTeardownRule drives rule 12, the removing arm a refusal
// battery never reaches. An unconditional Shutdown against a held entry
// answers RECLAIMED, the slot release removes the tree, and the runtime
// teardown runs when the session has started and only then.
func UnconditionalTeardownRule(t *testing.T, transport Transport) {
	for _, started := range []bool{true, false} {
		t.Run(fmt.Sprintf("started=%v", started), func(t *testing.T) {
			f := New(t, transport)
			f.assign(t, alice, TokenA, true)
			if started {
				f.start(t, alice)
			}
			f.wantOutcome(t, "the unconditional Shutdown", unconditionalReq(alice), reclaimed)
			wantRemoved(t, f, alice, started)
		})
	}
}

// wantRemoved fails the case unless id's entry and tree are gone and the
// runtime teardown ran exactly when the session had started. A session that
// started through StartSession reached running, so its release files one
// cleanup outcome.
func wantRemoved(t *testing.T, f *Fixture, id string, started bool) {
	t.Helper()
	f.wantTreeRemoved(t, id)
	f.wantNoEntry(t, id)
	want := 0
	if started {
		want = 1
	}
	if n := f.Runtime.Closes(id); n != want {
		t.Errorf("runtime closes = %d, want %d", n, want)
	}
	if n := f.Reports.For(id); n != want {
		t.Errorf("session scrub reports = %d, want %d", n, want)
	}
}

// AttemptMismatchRule drives rule 13 on both arms. A Shutdown naming attempt
// B against an entry stamped A, and one naming an attempt against an entry a
// StartSession or a ConfigureWorkspace created with no token, answer
// SUPERSEDED, remove nothing, and run neither teardown. The untokened arm is
// the fail-closed one: without it a reclaim naming any attempt would collect
// a successor's live session.
func AttemptMismatchRule(t *testing.T, transport Transport) {
	arms := []struct {
		name  string
		setup func(t *testing.T, f *Fixture)
		creds bool
	}{
		{"entry stamped with another attempt", func(t *testing.T, f *Fixture) { f.bindAndStart(t, alice, TokenB) }, true},
		{"entry StartSession created", func(t *testing.T, f *Fixture) { f.start(t, alice) }, false},
		{"entry ConfigureWorkspace created", func(t *testing.T, f *Fixture) {
			if err := f.configure(t, alice); err != nil {
				t.Fatalf("ConfigureWorkspace: %v", err)
			}
		}, false},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			f := New(t, transport)
			arm.setup(t, f)
			resp := f.wantOutcome(t, "a Shutdown naming an attempt the entry does not carry", fencedReq(alice, TokenA), superseded)
			if !resp.GetExitedCleanly() {
				t.Error("a SUPERSEDED outcome reported an unclean exit")
			}
			f.wantTreeIntact(t, alice, arm.creds)
			if n := f.Runtime.Closes(alice); n != 0 {
				t.Errorf("runtime closes = %d, want 0: the mismatch tore the session down", n)
			}
			f.wantEntry(t, alice)
		})
	}
}

// AttemptMatchRule drives rule 14, the positive the rule 11 and rule 13
// cases are read against. A Shutdown naming the token the entry carries
// answers RECLAIMED, the slot release runs, and the runtime teardown runs
// for a started session.
func AttemptMatchRule(t *testing.T, transport Transport) {
	for _, started := range []bool{true, false} {
		t.Run(fmt.Sprintf("started=%v", started), func(t *testing.T) {
			f := New(t, transport)
			f.assign(t, alice, TokenA, true)
			if started {
				f.start(t, alice)
			}
			f.wantOutcome(t, "the Shutdown naming the entry's token", fencedReq(alice, TokenA), reclaimed)
			wantRemoved(t, f, alice, started)
		})
	}
}

// ReclaimOutcomeRule drives rule 15. One Shutdown reaching each of rules 11,
// 12, 13 and 14 answers on a successful call carrying the matching
// slot_reclaim value, and the arms that remove nothing report a clean exit.
func ReclaimOutcomeRule(t *testing.T, transport Transport) {
	arms := []struct {
		name  string
		setup func(t *testing.T, f *Fixture)
		req   *adapterv1.ShutdownRequest
		want  adapterv1.SlotReclaimOutcome
	}{
		{"rule 11", func(*testing.T, *Fixture) {}, fencedReq(alice, TokenA), absent},
		{"rule 12", func(t *testing.T, f *Fixture) { f.bindAndStart(t, alice, TokenA) }, unconditionalReq(alice), reclaimed},
		{"rule 13", func(t *testing.T, f *Fixture) { f.bindAndStart(t, alice, TokenA) }, fencedReq(alice, TokenB), superseded},
		{"rule 14", func(t *testing.T, f *Fixture) { f.bindAndStart(t, alice, TokenA) }, fencedReq(alice, TokenA), reclaimed},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			f := New(t, transport)
			arm.setup(t, f)
			resp := f.wantOutcome(t, "the Shutdown", arm.req, arm.want)
			if arm.want != reclaimed && !resp.GetExitedCleanly() {
				t.Errorf("a %v outcome reported an unclean exit", arm.want)
			}
		})
	}
}

// StampOnce drives the stamp-once rule. A StartSession creates an entry
// carrying no token; a non-mid-session request naming attempt B against it
// is refused under rule 6 and does not write B, so a later Shutdown naming B
// answers SUPERSEDED rather than RECLAIMED.
func StampOnce(t *testing.T, transport Transport) {
	f := New(t, transport)
	f.start(t, alice)
	_, err := f.Pod.RunSetup(callCtx(t), &adapterv1.RunSetupRequest{SessionId: sid(alice), BindAttempt: TokenB})
	wantStartedRefusal(t, "RunSetup naming an attempt against the untokened started entry", err)
	f.wantOutcome(t, "a Shutdown naming the refused request's attempt", fencedReq(alice, TokenB), superseded)
	if n := f.Runtime.Closes(alice); n != 0 {
		t.Errorf("runtime closes = %d, want 0", n)
	}
}

// ResolveCreateStampIndivisible drives the first step of §4.7.1's registry
// critical section. Two bind attempts at one slot identifier race over
// separate clients; exactly one is admitted and the other is refused
// SLOT_BIND_ATTEMPT_SUPERSEDED, and the entry carries the admitted token. An
// adapter that resolves, releases its lock and then stamps admits both. Run
// the tier under -race to check the section's locking as well.
func ResolveCreateStampIndivisible(t *testing.T, transport Transport) {
	f := New(t, transport)
	first, second := f.Dial(t), f.Dial(t)
	for i := range 25 {
		id := fmt.Sprintf("alice-%d", i)
		errA, errB := raceAssign(t, first, second, id)
		switch {
		case errA == nil && errB != nil:
			wantSupersededRefusal(t, "the losing attempt", errB)
			f.wantOwnedBy(t, id, TokenA)
		case errB == nil && errA != nil:
			wantSupersededRefusal(t, "the losing attempt", errA)
			f.wantOwnedBy(t, id, TokenB)
		default:
			t.Fatalf("%s: attempt A = %v, attempt B = %v; want exactly one admitted", id, errA, errB)
		}
	}
}

// raceAssign releases an AssignCredentials under TokenA on one client and one
// under TokenB on the other at the same instant and returns both answers.
func raceAssign(t *testing.T, first, second Pod, id string) (errA, errB error) {
	t.Helper()
	ctx := callCtx(t)
	var wg sync.WaitGroup
	gate := make(chan struct{})
	send := func(p Pod, token string, out *error) {
		defer wg.Done()
		<-gate
		_, *out = p.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{SessionId: sid(id), BindAttempt: token})
	}
	wg.Add(2)
	go send(first, TokenA, &errA)
	go send(second, TokenB, &errB)
	close(gate)
	wg.Wait()
	return errA, errB
}

// AttemptIdentityBeforeStartedSession drives the order of rules 5 and 6. A
// non-mid-session request naming attempt B against a started entry stamped
// A meets both rules' conditions and is answered as rule 5 states, the
// transient refusal, and the entry, its current directory and its
// credentials.json survive.
func AttemptIdentityBeforeStartedSession(t *testing.T, transport Transport) {
	f := New(t, transport)
	f.bindAndStart(t, alice, TokenA)
	_, err := f.Pod.RunSetup(callCtx(t), &adapterv1.RunSetupRequest{SessionId: sid(alice), BindAttempt: TokenB})
	wantSupersededRefusal(t, "RunSetup naming another attempt against the started entry", err)
	f.wantTreeIntact(t, alice, true)
	f.wantOwnedBy(t, alice, TokenA)
}

// ReclaimHoldAgainstShutdown drives the interaction §5.2's reclaim-hold
// paragraph states. While a cleanup holds the identifier, a Shutdown of
// either form is admitted and answers ABSENT under rule 11, without waiting
// on the cleanup, while a bind-sequence request for the same identifier is
// refused under rule 2. An adapter that applied the hold to a reclaim would
// block its own cleanup behind itself.
func ReclaimHoldAgainstShutdown(t *testing.T, transport Transport) {
	f := New(t, transport)
	f.bindAndStart(t, alice, TokenA)
	pc := f.park(t, alice)
	for name, req := range map[string]*adapterv1.ShutdownRequest{
		"naming the attempt":     fencedReq(alice, TokenA),
		"unconditional teardown": unconditionalReq(alice),
	} {
		var resp *adapterv1.ShutdownResponse
		err := callWithin(t, func(ctx context.Context) error {
			var err error
			resp, err = f.Pod.Shutdown(ctx, req)
			return err
		})
		if err != nil || resp.GetSlotReclaim() != absent {
			t.Errorf("Shutdown %s during the cleanup = %v, %v; want ABSENT", name, resp.GetSlotReclaim(), err)
		}
	}
	err := callWithin(t, func(ctx context.Context) error {
		_, err := f.Pod.RunSetup(ctx, &adapterv1.RunSetupRequest{SessionId: sid(alice), BindAttempt: TokenA})
		return err
	})
	if status.Code(err) != codes.Aborted {
		t.Errorf("RunSetup during the cleanup = %v, want Aborted", err)
	}
	f.finish(t, pc)
	// The cleanup deregistered the entry and no refused request re-created
	// it, so a probe finds nothing to reclaim.
	f.wantNoEntry(t, alice)
}
