// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"testing"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: 28 (CH-MCP-PLATFORM exclusivity), 15.4.3 (runtime integration
// levels), 4.7.10 (Runtime process lifetime)
//
// The runtime process lives as long as the pod, so the generation counts
// every session the process has been given and never resets. alice is sole
// while she is the only session the process has been given and is live in
// it. Once she closes, no session is sole, and bob, who starts on the same
// process after occupancy zero, is not sole either.
//
// diagnosis: the generation reset at occupancy zero, so a pod-global
// surface acts under a later session on a runtime process that still holds
// an earlier session's code, or it named a session that has closed.
func TestSoleSessionIsEmptyOnceTheCohortSessionCloses_spec_28(t *testing.T) {
	s, _ := slotPod(t)
	s.Runtime = &probeRuntime{}

	if err := s.claimSessionForTest("alice"); err != nil {
		t.Fatalf("claim alice: %v", err)
	}
	if got := s.SoleSessionID(); got != "alice" {
		t.Fatalf("SoleSessionID with alice alone = %q, want alice", got)
	}

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		UnconditionalTeardown: true,
		SessionId:             &adapterv1.SessionId{Value: "alice"},
	}); err != nil {
		t.Fatalf("Shutdown alice: %v", err)
	}
	if got := s.SoleSessionID(); got != "" {
		t.Errorf("SoleSessionID after alice's Shutdown = %q, want empty", got)
	}

	if err := s.claimSessionForTest("bob"); err != nil {
		t.Fatalf("claim bob: %v", err)
	}
	if got := s.SoleSessionID(); got != "" {
		t.Errorf("SoleSessionID after bob starts on the process alice used = %q, want empty", got)
	}
}

// spec: 28 (CH-MCP-PLATFORM exclusivity), 4.7.10 (Runtime process lifetime)
//
// A repeated start of the generation's first session does not raise the
// cohort, and a close of a session the generation never held moves
// nothing, so neither drives the sole session empty while that session is
// the only one the process has been given.
//
// diagnosis: an idempotent repeat of a start, or a stray close, changed the
// answer the pod-global surfaces read.
func TestSoleSessionSurvivesARepeatedStartAndAStrayClose_spec_28(t *testing.T) {
	s := New("generation")
	s.mu.Lock()
	s.noteRuntimeStartedLocked("alice")
	s.noteRuntimeStartedLocked("alice")
	s.mu.Unlock()
	s.noteRuntimeClosed("never-started")
	if got := s.soleSession(); got != "alice" {
		t.Errorf("soleSession = %q, want alice", got)
	}
}
