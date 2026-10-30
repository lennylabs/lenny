// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos scenarios for Open Responses conversation sessions on the
// lifecycle harness. Each replica's POST /v1/responses is the production
// translator handler, which Harness.WireOpenResponses installs through
// Replica.SetResponsesHandler with a binder that holds every session it
// binds. The scenarios crash the coordinating replica during a warm turn and
// after it read a forwarded turn, and restore a held session through the
// §7.3 resume driver between turns and during one.
//
// spec: §15 (Open Responses conversation sessions), §29.3 (Interactive
// message send) Forwarding, §7.3 (Retry and Resume), §6.2 (Pod State
// Machine)
package tier8_chaos_test

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// responsesChaosUser is the user every Open Responses turn names.
const responsesChaosUser = "alice@acme.com"

// postResponses posts one Open Responses turn to r, continuing prev when it
// is set, and returns the status and decoded body.
func postResponses(h *lifecycleharness.Harness, r *lifecycleharness.Replica, prev, input string) (int, map[string]any) {
	body := map[string]any{"model": lifecycleharness.RuntimeRef, "input": input, "user": responsesChaosUser}
	if prev != "" {
		body["previous_response_id"] = prev
	}
	return h.Do(r, http.MethodPost, "/v1/responses", body)
}

// firstResponsesTurn runs the first turn of a conversation on r and returns
// the response id and its session id, failing unless it answered 200 with
// ordinal 1.
func firstResponsesTurn(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, input string) (string, string) {
	t.Helper()
	code, body := postResponses(h, r, "", input)
	id, _ := body["id"].(string)
	dot := strings.LastIndex(id, ".")
	if code != http.StatusOK || dot <= 0 || id[dot+1:] != "1" {
		t.Fatalf("first turn = %d %v, want 200 with id <session>.1", code, body)
	}
	return id, id[:dot]
}

// requireColdContinuation continues prev on r and fails unless the turn ran
// on a new session whose continuation parent is prev. It returns the new
// session id.
func requireColdContinuation(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, prev, old, input string) string {
	t.Helper()
	code, body := postResponses(h, r, prev, input)
	id, _ := body["id"].(string)
	dot := strings.LastIndex(id, ".")
	if code != http.StatusOK || dot <= 0 {
		t.Fatalf("continuation of %s = %d %v, want 200", prev, code, body)
	}
	cold := id[:dot]
	if cold == old || id[dot+1:] != "1" {
		t.Fatalf("continuation id %q, want ordinal 1 on a session other than %s (cold)", id, old)
	}
	if got := h.Row(cold).ContinuationParentID; got != prev {
		t.Errorf("new session's continuation parent = %q, want %q", got, prev)
	}
	return cold
}

// spec: 15 (Open Responses conversation sessions), 10.1.2 (Coordinator Handoff Protocol), 6.2 (Pod State Machine)
// diagnosis: the replica coordinating a held Open Responses session crashed
// after a warm turn reserved its response and before the runtime answered,
// and afterwards the reserved response was completed, withdrawn, or reused,
// a continuation of the last completed response did not go cold onto a new
// session whose continuation parent is that response, or the adopted session
// kept its pod after its idle suspension. A reserved response is never
// withdrawn, so it stops warm turns on that session for good. Check reserve
// and serveWarm in pkg/gateway/environment/translator/open_responses.go.
func TestCoordinatorCrashMidWarmTurnLeavesTheReservationAndGoesCold_spec_15(t *testing.T) {
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 6})
	h.RelaxOrphanClaims = true
	h.WireOpenResponses(true)
	coordinator, peer := h.Replicas[0], h.Replicas[1]
	first, id := firstResponsesTurn(t, h, coordinator, "turn-1")

	var crashed atomic.Bool
	h.Trace.ArmMatching("Update", "reserve", lifecycleharness.FaultCrash,
		func(lifecycleharness.TraceEntry, lifecycleharness.FaultKind) {
			crashed.Store(true)
			h.Crash(coordinator)
		})
	// The crashed replica's answer is not observable to a client, whose
	// connection a real crash resets.
	postResponses(h, coordinator, first, "lost-in-the-crash")
	if !crashed.Load() || !coordinator.Crashed() {
		t.Fatal("the armed crash after the reservation never fired")
	}

	h.RunToQuiescence()
	row := h.Row(id)
	if len(row.Responses) != 2 || row.Responses[1].Completed {
		t.Fatalf("held session responses after the crash = %+v, want the second reserved and not completed", row.Responses)
	}
	if n := timesReceived(h, id, "lost-in-the-crash"); n != 0 {
		t.Errorf("the runtime received the crashed turn %d times, want none", n)
	}
	if code, body := h.Do(peer, http.MethodGet, "/v1/responses/"+id+".2", nil); code != http.StatusNotFound {
		t.Errorf("GET of the reserved response = %d %v, want the native 404", code, body)
	}

	cold := requireColdContinuation(t, h, peer, first, id, "after-the-crash")
	if n := timesReceived(h, cold, "after-the-crash"); n != 1 {
		t.Errorf("the new session's runtime received the input %d times, want once", n)
	}
	if st := h.Row(id).State; st == session.StateRunning {
		h.SuspendIdle(peer, id)
	}
	if old := h.Row(id); old.PodAssignment != "" {
		t.Errorf("superseded session: state %q pod %q, want no pod after its idle suspension", old.State, old.PodAssignment)
	}
	h.Check(t)
}

// spec: 15 (Open Responses conversation sessions), 7.3 (Retry and Resume)
// diagnosis: after a §7.3 restore of a held Open Responses session between
// two turns, a continuation of its newest response ran warm on the restored
// session, whose runtime memory rolled back to a checkpoint, rather than
// going cold onto a new session whose continuation parent is that response.
// The warm path requires the session's recovery_generation to equal the
// value recorded for the response. Check warmEligible in
// pkg/gateway/environment/translator/open_responses.go.
func TestRestoreBetweenTurnsSendsTheNextContinuationCold_spec_15(t *testing.T) {
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 1, Pods: 6})
	h.WireOpenResponses(true)
	r := h.Replicas[0]
	first, id := firstResponsesTurn(t, h, r, "turn-1")
	gen := h.Row(id).RecoveryGeneration

	lifecycleharness.Report(t, r, id, string(session.FailureRuntimeCrash))
	row := h.AwaitState(id, session.StateRunning, chaosRecoveryPasses)
	for i := 0; i < chaosRecoveryPasses && row.RecoveryGeneration == gen; i++ {
		h.Pass()
		row = h.Row(id)
	}
	if row.State != session.StateRunning || row.RecoveryGeneration <= gen {
		t.Fatalf("after the restore the session is %q at recovery generation %d, want running above %d",
			row.State, row.RecoveryGeneration, gen)
	}

	cold := requireColdContinuation(t, h, r, first, id, "after-restore")
	if n := timesReceived(h, id, "after-restore"); n != 0 {
		t.Errorf("the restored session's runtime received the continuation %d times, want none", n)
	}
	if n := timesReceived(h, cold, "after-restore"); n != 1 {
		t.Errorf("the new session's runtime received the continuation %d times, want once", n)
	}
	if n := len(h.Row(id).Responses); n != 1 {
		t.Errorf("restored session carries %d response records, want only its first turn's", n)
	}
}

// spec: 15 (Open Responses conversation sessions), 7.3 (Retry and Resume), 7.2 (Interactive Session Model)
// diagnosis: a §7.3 restore of a held Open Responses session between a warm
// turn's reservation and its completion write let the turn complete, or the
// handler answered anything but the native 500, or left the session other
// than failed. The runtime answers the turn and the transcript records it,
// then the completion write is held while a runtime_crash report sends the
// session through resume_pending and the resume driver restores it at a
// higher recovery_generation. The completion write is conditioned on the
// recovery_generation captured at the reservation, and every failure after
// input may have reached the runtime ends the session. Check commitWarm and
// endHeldSession in pkg/gateway/environment/translator/open_responses.go.
func TestRestoreMidTurnFailsTheTurnAndTheSession_spec_15(t *testing.T) {
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 1, Pods: 6})
	h.WireOpenResponses(true)
	r := h.Replicas[0]
	first, id := firstResponsesTurn(t, h, r, "turn-1")
	gen := h.Row(id).RecoveryGeneration

	// The hold runs on the handler's goroutine before the completion write,
	// so it records what it observed and the test asserts afterwards.
	var held atomic.Bool
	var restored sessionstore.Session
	var reserved int
	var reportErr error
	h.Trace.ArmMatching("Update", "commitWarm", lifecycleharness.FaultHold,
		func(lifecycleharness.TraceEntry, lifecycleharness.FaultKind) {
			held.Store(true)
			reserved = len(h.Row(id).Responses)
			if _, reportErr = r.Server.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
				TenantID: lifecycleharness.Tenant, SessionID: id, Reason: string(session.FailureRuntimeCrash),
			}); reportErr != nil {
				return
			}
			restored = h.AwaitState(id, session.StateRunning, chaosRecoveryPasses)
			for i := 0; i < chaosRecoveryPasses && restored.RecoveryGeneration == gen; i++ {
				h.Pass()
				restored = h.Row(id)
			}
		})
	code, body := postResponses(h, r, first, "in-flight")

	if !held.Load() {
		t.Fatalf("the warm turn never reached its completion write; answered %d %v", code, body)
	}
	if reportErr != nil {
		t.Fatalf("ReportSessionFailure during the held completion write: %v", reportErr)
	}
	if reserved != 2 {
		t.Errorf("the warm turn in hand left %d response records, want the reservation", reserved)
	}
	if restored.State != session.StateRunning || restored.RecoveryGeneration <= gen {
		t.Fatalf("the runtime_crash report left the session %q at recovery generation %d, want running after a restore above %d",
			restored.State, restored.RecoveryGeneration, gen)
	}
	if n := timesReceived(h, id, "in-flight"); n < 1 {
		t.Errorf("the runtime received the warm turn %d times, want it delivered before the restore", n)
	}
	h.Settle()
	e, _ := body["error"].(map[string]any)
	if code != http.StatusInternalServerError || e["type"] != "server_error" {
		t.Fatalf("warm turn across the restore = %d %v, want the native 500", code, body)
	}
	row := h.Row(id)
	if row.State != session.StateFailed {
		t.Errorf("session after the failed turn = %q, want failed", row.State)
	}
	if row.RecoveryGeneration != restored.RecoveryGeneration {
		t.Errorf("recovery generation after the failed turn = %d, want the restored %d", row.RecoveryGeneration, restored.RecoveryGeneration)
	}
	if len(row.Responses) != 2 || row.Responses[1].Completed {
		t.Errorf("responses after the failed turn = %+v, want the second not completed", row.Responses)
	}
	if code, _ := h.Do(r, http.MethodGet, "/v1/responses/"+id+".2", nil); code != http.StatusNotFound {
		t.Errorf("GET of the failed turn's response = %d, want the native 404", code)
	}
}

// spec: 29.3 (Interactive message send) Forwarding rules 6 and 7, 15 (Open Responses conversation sessions)
// diagnosis: the coordinating replica crashed after it read an Open Responses
// warm turn forwarded to it over CH-HOLDERFORWARD, and the serving replica
// served the turn itself, went cold, held it in the DLQ, reserved a response,
// delivered it to the runtime, or answered other than 409 TARGET_NOT_READY
// in the adapter's native envelope. An outcome-unknown forward may already
// have been applied, so it fails closed. Check ForwardResponsesTurn in
// pkg/gateway/sessionserver/holder_forward.go and handleCreate in
// pkg/gateway/environment/translator/open_responses.go.
func TestCoordinatorCrashAfterReadingAForwardedResponsesTurnFailsClosed_spec_29_3(t *testing.T) {
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 6})
	h.WireOpenResponses(true)
	coordinator, peer := h.Replicas[0], h.Replicas[1]
	first, id := firstResponsesTurn(t, h, coordinator, "turn-1")
	binds := h.Claims.Len()

	var read atomic.Int64
	coordinator.SetForwardFault(func(req *http.Request) bool {
		if req.URL.Path != "/v1/responses" {
			return false
		}
		read.Add(1)
		h.Crash(coordinator)
		return true
	})
	const text = "lost-after-the-read"
	code, body := postResponses(h, peer, first, text)

	if n := read.Load(); n != 1 {
		t.Fatalf("the coordinator read %d forwarded turns, want 1", n)
	}
	e, _ := body["error"].(map[string]any)
	if code != http.StatusConflict || e["code"] != "TARGET_NOT_READY" || e["current_state"] != string(session.StateRunning) {
		t.Fatalf("turn on the serving replica = %d %v, want the native 409 TARGET_NOT_READY with current_state running", code, body)
	}
	if _, lenny := e["details"]; lenny {
		t.Errorf("refusal carries the Lenny envelope's details: %v", body)
	}
	if n := h.Claims.Len() - binds; n != 0 {
		t.Errorf("the refused turn claimed %d pods, want none (no cold fallback)", n)
	}
	if n := len(h.Row(id).Responses); n != 1 {
		t.Errorf("held session carries %d response records, want only the first turn's (no reservation)", n)
	}
	requireNothingHeld(t, h, id, text)
}
