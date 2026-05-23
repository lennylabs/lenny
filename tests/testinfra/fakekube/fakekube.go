// SPDX-License-Identifier: MIT

package fakekube

import (
	"sync"
	"time"
)

// Surface is the fake Kubernetes API used by tier-7a scenarios.
//
// The Wave 2 cut exposes the minimum surface scenarios need: a way
// to register objects, a way to read them back, and a configurable
// watch-event delay. Scenarios that need deep SSA conflict
// reproduction extend the surface as Wave 3 lands them; the public
// API is kept narrow on purpose so the implementation can be replaced
// with a more accurate fake (e.g. envtest's local apiserver) when
// the scenario set demands it.
type Surface struct {
	mu        sync.RWMutex
	objects   map[string][]byte
	watchLag  time.Duration
}

// New returns an empty Surface. The default watch-event delay is
// zero (synchronous delivery).
func New() *Surface {
	return &Surface{
		objects: make(map[string][]byte),
	}
}

// SetWatchLag configures the delay between an object mutation and
// the watch event firing for that object. Used by ordering tests
// (the §5.2 claim_admission_ordering scenario for example).
func (s *Surface) SetWatchLag(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watchLag = d
}

// WatchLag returns the currently configured watch-event delay.
func (s *Surface) WatchLag() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.watchLag
}

// Put stores raw object bytes under key. Wave 2 minimum: scenarios
// that need typed CRUD will extend this once the first scenario
// needing it lands in Wave 3 (see TESTING.md §12.7.a multi-component
// catalogue).
func (s *Surface) Put(key string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = body
}

// Get returns the raw object bytes for key. ok is false when the
// key is absent.
func (s *Surface) Get(key string) (body []byte, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	body, ok = s.objects[key]
	return
}

// Delete removes key from the surface. Idempotent.
func (s *Surface) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
}

// Len returns the number of objects in the surface.
func (s *Surface) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.objects)
}
