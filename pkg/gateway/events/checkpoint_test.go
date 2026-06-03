// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// nonceFromID extracts the nonce suffix from a §25.3 eventKey of the form
// {replicaID}:{emittedAt}:{nonce}.
func nonceFromID(t *testing.T, id string) uint64 {
	t.Helper()
	parts := strings.Split(id, ":")
	if len(parts) < 3 {
		t.Fatalf("eventKey %q is not {replicaID}:{emittedAt}:{nonce}", id)
	}
	n, err := strconv.ParseUint(parts[len(parts)-1], 10, 64)
	if err != nil {
		t.Fatalf("parse nonce from %q: %v", id, err)
	}
	return n
}

// emitAndNonce emits one event and returns the nonce baked into its
// generated eventKey (the newest event in the buffer).
func emitAndNonce(t *testing.T, em *Emitter) uint64 {
	t.Helper()
	if err := em.Emit(context.Background(), OperationalEvent{Type: "dev.lenny.alert_fired"}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	page := em.buffer.Query(0, EventFilter{}, DefaultBufferCapacity)
	if len(page.Events) == 0 {
		t.Fatal("no event recorded")
	}
	return nonceFromID(t, page.Events[len(page.Events)-1].Event.ID)
}

// TestNonceCheckpointSurvivesRestart_spec_25_3_748 asserts the §25.3 line
// 748 contract: with the disk checkpoint wired, a restarted emitter
// resumes from last_checkpointed + safe_skip_window and never replays a
// nonce that was used before the restart.
func TestNonceCheckpointSurvivesRestart_spec_25_3_748(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonce")
	spec := NonceCheckpoint{Path: path, Every: 4, Window: 16}

	em := NewEmitter(NewEventBuffer(0), "replica-1", WithNonceCheckpoint(spec))
	var maxBefore uint64
	for i := 0; i < 10; i++ {
		if n := emitAndNonce(t, em); n > maxBefore {
			maxBefore = n
		}
	}
	if maxBefore != 10 {
		t.Fatalf("pre-restart max nonce = %d, want 10", maxBefore)
	}

	// The checkpoint must have persisted a high-water mark at a multiple
	// of Every (the last persist before nonce 10 is at 8).
	persisted := readCheckpoint(t, path)
	if persisted != 8 {
		t.Fatalf("persisted checkpoint = %d, want 8 (last multiple of Every<=10)", persisted)
	}

	// Simulate a restart: a fresh emitter with the same replicaID reading
	// the same checkpoint file.
	em2 := NewEmitter(NewEventBuffer(0), "replica-1", WithNonceCheckpoint(spec))
	resumed := emitAndNonce(t, em2)
	if want := persisted + spec.Window + 1; resumed != want {
		t.Fatalf("first post-restart nonce = %d, want last_checkpointed(%d)+window(%d)+1 = %d",
			resumed, persisted, spec.Window, want)
	}
	if resumed <= maxBefore {
		t.Fatalf("post-restart nonce %d must exceed every pre-restart nonce (max %d) to avoid replay",
			resumed, maxBefore)
	}
}

// TestNonceCheckpointPersistsEveryN_spec_25_3_748 asserts the checkpoint
// advances only once the counter has moved Every ticks past the last
// persisted value (the §25.3 periodic checkpoint, not a per-event write).
func TestNonceCheckpointPersistsEveryN_spec_25_3_748(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonce")
	em := NewEmitter(NewEventBuffer(0), "r", WithNonceCheckpoint(NonceCheckpoint{Path: path, Every: 5, Window: 20}))

	for i := 1; i <= 4; i++ {
		emitAndNonce(t, em)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("checkpoint written before Every ticks elapsed (err=%v)", err)
	}
	emitAndNonce(t, em) // 5th emit reaches Every
	if got := readCheckpoint(t, path); got != 5 {
		t.Fatalf("checkpoint after 5 emits = %d, want 5", got)
	}
}

// TestNonceCheckpointWindowBelowEveryUsesDefault_spec_25_3_748 asserts a
// Window smaller than Every is widened to the package default so the
// safe-skip window always exceeds the unpersisted-tick gap.
func TestNonceCheckpointWindowBelowEveryUsesDefault_spec_25_3_748(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonce")
	if err := os.WriteFile(path, []byte("100"), 0o600); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	// Window (2) < Every (10): loadNonceCheckpoint must widen Window to
	// defaultCheckpointWindow.
	_, start, err := loadNonceCheckpoint(NonceCheckpoint{Path: path, Every: 10, Window: 2})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if want := uint64(100) + defaultCheckpointWindow; start != want {
		t.Fatalf("resume start = %d, want %d (default window applied)", start, want)
	}
}

// TestNonceCheckpointMissingFileStartsFresh_spec_25_3_748 asserts an
// absent checkpoint resumes from nonce 1 (no error, in-process start).
func TestNonceCheckpointMissingFileStartsFresh_spec_25_3_748(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")
	cp, start, err := loadNonceCheckpoint(NonceCheckpoint{Path: path})
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if start != 0 {
		t.Fatalf("missing-file start = %d, want 0", start)
	}
	if cp == nil {
		t.Fatal("checkpoint should be non-nil so subsequent records persist")
	}
}

// TestNonceCheckpointCorruptFileFallsBack_spec_25_3_748 asserts a corrupt
// checkpoint surfaces an error through the logger and the emitter falls
// back to an in-process counter rather than failing startup.
func TestNonceCheckpointCorruptFileFallsBack_spec_25_3_748(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonce")
	if err := os.WriteFile(path, []byte("not-a-number"), 0o600); err != nil {
		t.Fatalf("seed corrupt checkpoint: %v", err)
	}
	var loggedErr error
	em := NewEmitter(NewEventBuffer(0), "r",
		WithNonceCheckpoint(NonceCheckpoint{Path: path}),
		WithEmitErrorLogger(func(e error) { loggedErr = e }),
	)
	if loggedErr == nil {
		t.Fatal("corrupt checkpoint should be reported through the error logger")
	}
	if got := emitAndNonce(t, em); got != 1 {
		t.Fatalf("post-corruption first nonce = %d, want 1 (in-process fallback)", got)
	}
}

// TestNoCheckpointKeepsInProcessNonce asserts the default (no checkpoint)
// path is unchanged: the counter starts at 1 and increments per emit.
func TestNoCheckpointKeepsInProcessNonce(t *testing.T) {
	em := NewEmitter(NewEventBuffer(0), "r")
	if got := emitAndNonce(t, em); got != 1 {
		t.Fatalf("first nonce without checkpoint = %d, want 1", got)
	}
}

func readCheckpoint(t *testing.T, path string) uint64 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read checkpoint %q: %v", path, err)
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		t.Fatalf("parse checkpoint %q: %v", path, err)
	}
	return n
}
