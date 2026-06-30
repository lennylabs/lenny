// SPDX-License-Identifier: MIT

package events

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
