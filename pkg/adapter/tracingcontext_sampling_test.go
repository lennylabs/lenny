// SPDX-License-Identifier: MIT

package adapter

import (
	"sync"
	"testing"
	"time"
)

// tracingSamplingToggleWindow is how long the toggling goroutine flips the
// registry underneath the addressing decision. The two states it alternates
// between are both rejected, so the assertion is a constant: no sampling of
// the registry taken at one instant admits the frame.
const tracingSamplingToggleWindow = 300 * time.Millisecond

// spec: 28.5.3 (set_tracing_context addressing, live-binding
// confirmation), 6.4 (slot claim and release lifecycle)
//
// diagnosis: the pod-global branch of the addressing decision reads the
// registry more than once. The registry alternates between a pod holding a
// slot and the stream's session (the empty-registry term fails) and a pod
// holding neither (the session term fails), and every instantaneous
// sampling of those two states rejects the frame. A decision that admits
// one took its two terms from two different samplings, so it can register
// tracing identifiers against a session that has already been released.
func TestSetTracingContextAddressingSamplesTheRegistryOnce_spec_28_5_3(t *testing.T) {
	const sessionID = "sess-pod"
	s := New("test")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// The pod holds the stream's session and a claimed slot: the
			// address is ambiguous and the frame is rejected.
			s.mu.Lock()
			s.sessionID = sessionID
			s.slots = map[string]*slotState{"slot-a": {sessionID: "sess-slot-a"}}
			s.mu.Unlock()
			// The pod holds neither: the session term is what rejects the
			// frame, and the registry is empty.
			s.mu.Lock()
			s.sessionID = ""
			s.slots = map[string]*slotState{}
			s.mu.Unlock()
		}
	}()

	deadline := time.Now().Add(tracingSamplingToggleWindow)
	decisions := 0
	for time.Now().Before(deadline) {
		for i := 0; i < 1024; i++ {
			decisions++
			// An untagged frame on a slotless stream: both addresses are
			// the empty string, so address equality holds and only the
			// live-binding confirmation can reject.
			if s.tracingFrameAddressesStream(sessionID, "", "") {
				close(stop)
				wg.Wait()
				t.Fatalf("addressing decision %d admitted an untagged frame on a pod that never held "+
					"the stream's session and an empty slot registry at the same time: the decision "+
					"read the session and the slot registry at two different times",
					decisions)
			}
		}
	}
	close(stop)
	wg.Wait()
	if decisions == 0 {
		t.Fatal("no addressing decision ran while the registry was being toggled")
	}
}
