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

// Keyer mints §25.3 stable event keys and owns the per-replica nonce
// counter (and its optional disk checkpoint). The eventbuffer Emitter and
// StreamEmitter share this so the buffer and the Redis stream stamp the
// same eventKey format. It is exported so the concrete emitters in
// pkg/gateway/eventbuffer, which produce the events this package's
// vocabulary describes, can build and use it. spec: §25.3 line 748.
type Keyer struct {
	replicaID  string
	nonce      atomic.Uint64
	checkpoint *nonceCheckpoint
	onError    func(error)
}

// NewKeyer builds a Keyer starting from `start` (0 for a fresh in-process
// counter, last_checkpointed + window when resuming from disk). cp is the
// live checkpoint ResolveCheckpoint returned alongside start. An empty
// replicaID falls back to "gateway".
func NewKeyer(replicaID string, cp *nonceCheckpoint, start uint64, onError func(error)) *Keyer {
	if replicaID == "" {
		replicaID = "gateway"
	}
	k := &Keyer{replicaID: replicaID, checkpoint: cp, onError: onError}
	k.nonce.Store(start)
	return k
}

// EventKey composes the §25.3 stable identifier {replicaID}:{at}:{nonce}
// and, when a disk checkpoint is wired, advances it. spec: §25.3 line 748.
func (k *Keyer) EventKey(at time.Time) string {
	n := k.nonce.Add(1)
	if k.checkpoint != nil {
		if err := k.checkpoint.record(n); err != nil && k.onError != nil {
			k.onError(err)
		}
	}
	return fmt.Sprintf("%s:%d:%d", k.replicaID, at.UnixNano(), n)
}

// ResolveCheckpoint turns an optional NonceCheckpoint spec into a live
// checkpoint and a starting nonce. A nil spec (the common in-process
// case) yields no checkpoint and a counter starting at 0. A load error is
// reported through onError and the keyer falls back to in-process only so
// a missing or unreadable checkpoint never blocks startup. The returned
// checkpoint flows straight into NewKeyer; the concrete emitters pass it
// through without naming its unexported type.
func ResolveCheckpoint(spec *NonceCheckpoint, onError func(error)) (*nonceCheckpoint, uint64) {
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
