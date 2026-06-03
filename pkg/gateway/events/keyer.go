// SPDX-License-Identifier: MIT

package events

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The §25.3 eventKey is {replicaID}:{emittedAt}:{nonce}, where nonce is a
// per-replica monotonically-increasing counter that increments for every
// emitted event regardless of the emitting subsystem. Combined with a
// unique replicaID it makes the key globally unique across replicas and
// emission paths. spec: §25.3 line 748.

// Default nonce-checkpoint cadence and safe-skip window. The window is
// the §25.3 safe_skip_window: on restart the counter resumes from
// last_checkpointed + window so it cannot replay a nonce that was used
// after the last checkpoint but before the crash. window must exceed
// every (the max number of unpersisted increments) for that guarantee to
// hold. spec: §25.3 line 748.
const (
	defaultCheckpointEvery  uint64 = 128
	defaultCheckpointWindow uint64 = 1024
)

// NonceCheckpoint configures the §25.3 on-disk nonce checkpoint. When a
// non-empty Path is wired, the per-replica nonce counter survives a
// restart so the eventKey stays unique even when the replicaID is stable
// across restarts (e.g. LENNY_REPLICA_ID pinned to the pod name). Every
// and Window fall back to the package defaults when zero.
// spec: §25.3 line 748.
type NonceCheckpoint struct {
	// Path is the local-disk file the high-water mark is persisted to.
	Path string
	// Every persists the counter once it has advanced this many ticks
	// past the last persisted value. Zero uses defaultCheckpointEvery.
	Every uint64
	// Window is the safe_skip_window added to the persisted value on
	// restart. Zero (or any value below Every) uses
	// defaultCheckpointWindow. spec: §25.3 line 748.
	Window uint64
}

// nonceCheckpoint persists the per-replica nonce high-water mark to local
// disk. spec: §25.3 line 748.
type nonceCheckpoint struct {
	path   string
	every  uint64
	window uint64

	mu        sync.Mutex
	persisted uint64
}

// loadNonceCheckpoint opens (or initializes) the checkpoint at spec.Path
// and returns it together with the nonce the counter should resume from:
// last_checkpointed + safe_skip_window, or 0 when the file is absent. A
// read or parse error is returned so the caller can decide whether to
// proceed with an in-process-only counter. spec: §25.3 line 748.
func loadNonceCheckpoint(spec NonceCheckpoint) (*nonceCheckpoint, uint64, error) {
	every := spec.Every
	if every == 0 {
		every = defaultCheckpointEvery
	}
	window := spec.Window
	if window < every {
		window = defaultCheckpointWindow
	}
	cp := &nonceCheckpoint{path: spec.Path, every: every, window: window}
	b, err := os.ReadFile(spec.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return cp, 0, nil
	}
	if err != nil {
		return cp, 0, fmt.Errorf("events: read nonce checkpoint %q: %w", spec.Path, err)
	}
	last, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return cp, 0, fmt.Errorf("events: parse nonce checkpoint %q: %w", spec.Path, err)
	}
	cp.persisted = last
	return cp, last + window, nil
}

// record advances the persisted high-water mark to n once n has moved at
// least `every` ticks past the last persisted value. The write is atomic
// (temp file + rename) so a crash mid-write never leaves a corrupt
// checkpoint. A write error is returned so the keyer can log it; a failed
// persist degrades restart durability but never blocks an emit.
// spec: §25.3 line 748.
func (c *nonceCheckpoint) record(n uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n < c.persisted+c.every {
		return nil
	}
	if err := c.write(n); err != nil {
		return err
	}
	c.persisted = n
	return nil
}

func (c *nonceCheckpoint) write(n uint64) error {
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatUint(n, 10)), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// keyer mints §25.3 stable event keys and owns the per-replica nonce
// counter (and its optional disk checkpoint). The Emitter and
// StreamEmitter share this so the buffer and the Redis stream stamp the
// same eventKey format. spec: §25.3 line 748.
type keyer struct {
	replicaID  string
	nonce      atomic.Uint64
	checkpoint *nonceCheckpoint
	onError    func(error)
}

// newKeyer builds a keyer starting from `start` (0 for a fresh in-process
// counter, last_checkpointed + window when resuming from disk). An empty
// replicaID falls back to "gateway".
func newKeyer(replicaID string, cp *nonceCheckpoint, start uint64, onError func(error)) *keyer {
	if replicaID == "" {
		replicaID = "gateway"
	}
	k := &keyer{replicaID: replicaID, checkpoint: cp, onError: onError}
	k.nonce.Store(start)
	return k
}

// eventKey composes the §25.3 stable identifier {replicaID}:{at}:{nonce}
// and, when a disk checkpoint is wired, advances it. spec: §25.3 line 748.
func (k *keyer) eventKey(at time.Time) string {
	n := k.nonce.Add(1)
	if k.checkpoint != nil {
		if err := k.checkpoint.record(n); err != nil && k.onError != nil {
			k.onError(err)
		}
	}
	return fmt.Sprintf("%s:%d:%d", k.replicaID, at.UnixNano(), n)
}

// resolveCheckpoint turns an optional NonceCheckpoint spec into a live
// checkpoint and a starting nonce. A nil spec (the common in-process
// case) yields no checkpoint and a counter starting at 0. A load error is
// reported through onError and the keyer falls back to in-process only so
// a missing or unreadable checkpoint never blocks startup.
func resolveCheckpoint(spec *NonceCheckpoint, onError func(error)) (*nonceCheckpoint, uint64) {
	if spec == nil || spec.Path == "" {
		return nil, 0
	}
	cp, start, err := loadNonceCheckpoint(*spec)
	if err != nil {
		if onError != nil {
			onError(err)
		}
		return cp, start
	}
	return cp, start
}
