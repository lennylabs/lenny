// SPDX-License-Identifier: MIT

package podregistry

import "time"

// SetWatchTuningForTest configures the WatchPods polling cadence and
// per-watch channel buffer so the §12.6 line 482 resync backpressure
// path can be exercised deterministically. Test-only: a buffer of 1
// plus a sub-millisecond poll makes a slow consumer fall behind within
// a single reconcile.
func (r *CRDPodRegistry) SetWatchTuningForTest(interval time.Duration, bufferSize int) {
	r.pollInterval = interval
	r.watchBufferSize = bufferSize
}
