// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// fakeCore serves the seam operations the admin adapters call and records
// what each received.
type fakeCore struct {
	begun     []string
	ended     int
	terminal  []seam.Session
	fromState []state.State
	revoked   []seam.Session
	reclaimed []seam.Session
	jtis      []string
	floor     string
	denialErr error
}

func (f *fakeCore) BeginTerminal(id string) func() {
	f.begun = append(f.begun, id)
	return func() { f.ended++ }
}

func (f *fakeCore) OnSessionTerminal(_ context.Context, from state.State, s seam.Session) {
	f.fromState = append(f.fromState, from)
	f.terminal = append(f.terminal, s)
}

func (f *fakeCore) AfterTerminalWrite(_ context.Context, _ state.State, s seam.Session, _ func(context.Context, seam.Session)) seam.Session {
	return s
}

func (f *fakeCore) AfterRevokeWrite(_ context.Context, s seam.Session) { f.revoked = append(f.revoked, s) }

func (f *fakeCore) ReclaimRevokedPreRunning(_ context.Context, from state.State, s seam.Session) {
	f.fromState = append(f.fromState, from)
	f.reclaimed = append(f.reclaimed, s)
}

func (f *fakeCore) TerminateUserSessions(_ context.Context, _, _ string, ids []string) seam.UserTermination {
	return seam.UserTermination{PodsTerminated: len(ids) - 1, FailedSessions: ids[:1]}
}

func (f *fakeCore) PropagateTokenRevocation(jti string) { f.jtis = append(f.jtis, jti) }

func (f *fakeCore) RevokePoolCredentials(_ context.Context, _ string, ids []string) int { return len(ids) }

func (f *fakeCore) RevokeUserLeases(_, _ string, ids []string) int { return 2 * len(ids) }

func (f *fakeCore) ProbeTenantKMS(_ context.Context, tenantID, _ string) error {
	if tenantID != "acme" {
		return errors.New("no key")
	}
	return nil
}

func (f *fakeCore) LastTenantKMSProbe(string) (time.Time, bool) { return time.Unix(20, 0), true }

func (f *fakeCore) ResolveConnectorSecret(_ context.Context, ref string) (string, error) {
	return "v:" + ref, nil
}

func (f *fakeCore) ClearSubtreeDenial(context.Context, string, string, string) error {
	return f.denialErr
}

func (f *fakeCore) ElicitationFloor() string { return f.floor }

// TestSeamRevokeHookHandsTheCoreTheRowItWrote checks that the full_revoke
// hook opens the core's terminal claim and hands the core a session record
// that names the session and carries the row the revocation wrote, with
// the state the session left.
//
// spec: GWSEAM-BeginTerminal, GWSEAM-AfterRevokeWrite, GWSEAM-ReclaimRevokedPreRunning
func TestSeamRevokeHookHandsTheCoreTheRowItWrote(t *testing.T) {
	core := &fakeCore{}
	hook := admin.SeamRevokeHook{Terminals: core, Revocations: core}
	row := sessionstore.Session{ID: "sess-1", TenantID: "acme", UserID: "alice", State: session.StateCancelled}

	hook.BeginTerminal("sess-1")()
	hook.AfterRevokeWrite(context.Background(), row)
	hook.ReclaimRevokedPreRunning(context.Background(), session.StateStarting, row)

	if len(core.begun) != 1 || core.begun[0] != "sess-1" || core.ended != 1 {
		t.Errorf("terminal claim = %v, ended %d, want one claim for sess-1, ended once", core.begun, core.ended)
	}
	for _, rec := range append(core.revoked, core.reclaimed...) {
		stored, ok := rec.Stored.(sessionstore.Session)
		if rec.ID != "sess-1" || rec.TenantID != "acme" || !ok || stored.ID != "sess-1" {
			t.Errorf("record = %+v, want sess-1 of acme carrying its row", rec)
		}
	}
	if len(core.fromState) != 1 || core.fromState[0] != state.State(session.StateStarting) {
		t.Errorf("reclaim from = %v, want starting", core.fromState)
	}
}

// TestSessionTerminatorRunsTheCorePipelineOnTheForcedRow checks that a
// forced terminal write opens the core's terminal claim, runs the core's
// terminal pipeline on the row the write produced with the pre-force
// state, and ends the claim, and that a session already terminal runs no
// pipeline.
//
// spec: GWSEAM-BeginTerminal, GWSEAM-OnSessionTerminal
func TestSessionTerminatorRunsTheCorePipelineOnTheForcedRow(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	if err := store.Create(ctx, sessionstore.Session{ID: "sess-1", TenantID: "acme", UserID: "alice", State: session.StateRunning}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	core := &fakeCore{}
	term := admin.SessionTerminator{Store: store, Terminals: core}

	updated, prev, transitioned, err := term.ForceTerminate(ctx, "acme", "sess-1")
	if err != nil || !transitioned || prev != session.StateRunning || updated.State != session.StateFailed {
		t.Fatalf("ForceTerminate = %v, %v, %v, %v; want failed from running", updated.State, prev, transitioned, err)
	}
	if len(core.terminal) != 1 || core.fromState[0] != state.State(session.StateRunning) {
		t.Fatalf("pipeline runs = %d from %v, want one from running", len(core.terminal), core.fromState)
	}
	if stored, ok := core.terminal[0].Stored.(sessionstore.Session); !ok || stored.State != session.StateFailed || core.terminal[0].ID != "sess-1" {
		t.Errorf("pipeline record = %+v, want sess-1 carrying the failed row", core.terminal[0])
	}
	if core.ended != 1 {
		t.Errorf("terminal claim ended %d times, want 1", core.ended)
	}

	if _, _, transitioned, err := term.ForceTerminate(ctx, "acme", "sess-1"); err != nil || transitioned {
		t.Errorf("repeat ForceTerminate transitioned = %v, err %v; want an idempotent no-op", transitioned, err)
	}
	if len(core.terminal) != 1 {
		t.Errorf("pipeline ran %d times, want once", len(core.terminal))
	}
}

// TestCredentialAndDelegationAdaptersReachTheirOperations checks that each
// adapter answers the admin router's interface through its operation.
//
// spec: GWSEAM-TerminateUserSessions, GWSEAM-PropagateTokenRevocation, GWSEAM-RevokePoolCredentials, GWSEAM-RevokeUserLeases, GWSEAM-ProbeTenantKMS, GWSEAM-LastTenantKMSProbe, GWSEAM-ResolveConnectorSecret, GWSEAM-ClearSubtreeDenial, GWSEAM-ElicitationFloor
func TestCredentialAndDelegationAdaptersReachTheirOperations(t *testing.T) {
	ctx := context.Background()
	core := &fakeCore{floor: "required", denialErr: errors.New("denied")}

	res := admin.SeamUserPods{Revocations: core}.TerminateUserSessions(ctx, "acme", "alice", []string{"s1", "s2", "s3"})
	if res.PodsTerminated != 2 || len(res.FailedSessions) != 1 || res.FailedSessions[0] != "s1" {
		t.Errorf("TerminateUserSessions = %+v, want 2 released and s1 failed", res)
	}
	admin.SeamRevocationCache{Tokens: core}.Revoke("jti-9")
	if len(core.jtis) != 1 || core.jtis[0] != "jti-9" {
		t.Errorf("revoked jtis = %v, want [jti-9]", core.jtis)
	}
	if n := (admin.SeamPoolCredentialRevoker{Revocations: core}).RevokePoolCredentials(ctx, "p", []string{"a", "b"}); n != 2 {
		t.Errorf("RevokePoolCredentials = %d, want 2", n)
	}
	if n := (admin.SeamUserLeaseRevoker{Revocations: core}).RevokeUserLeases("acme", "alice", []string{"a"}); n != 2 {
		t.Errorf("RevokeUserLeases = %d, want 2", n)
	}
	probe := admin.SeamKMSProbe{Keys: core}
	if err := probe.ProbeAvailability(ctx, "acme", "T4"); err != nil {
		t.Errorf("ProbeAvailability(acme) = %v, want nil", err)
	}
	if err := probe.ProbeAvailability(ctx, "globex", "T4"); err == nil {
		t.Error("ProbeAvailability(globex) = nil, want the probe's error")
	}
	if at, ok := probe.LastProbeSuccess("acme"); !ok || !at.Equal(time.Unix(20, 0)) {
		t.Errorf("LastProbeSuccess = %v, %v", at, ok)
	}
	if v, err := (admin.SeamClientSecrets{Secrets: core}).Resolve(ctx, "ns/n"); err != nil || v != "v:ns/n" {
		t.Errorf("Resolve = %q, %v", v, err)
	}
	if err := (admin.SeamLeaseDenials{Delegation: core}).ClearSubtreeDenial(ctx, "acme", "root", "s"); err == nil {
		t.Error("ClearSubtreeDenial = nil, want the seam's error")
	}
	if got := admin.SeamElicitationFloor(core)(); got != "required" {
		t.Errorf("elicitation floor = %q, want required", got)
	}
}
