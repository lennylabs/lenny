// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// TestToolCallsRacingSealNeverSealInFlight_spec_4_7_1 drives tool calls
// concurrently with SealSession, at an idle clock past the window, over
// randomized interleavings. A seal requested while a call is in flight is
// never placed, and the call count returns to zero once every call returns.
// Run under -race (tier 7a).
//
// spec: 4.7.1 (Role and Gateway RPC Contract), 28.5.3 (CH-MCP-PLATFORM,
// CH-MCP-CONNECTOR)
//
// diagnosis: a failure means the call is resolved or counted outside the
// seal's lock, so a seal can land while a platform or connector call is in
// flight and the idle suspension releases a pod whose runtime is waiting on
// the call, or a count that outlives its entry corrupts a recreated entry.
func TestToolCallsRacingSealNeverSealInFlight_spec_4_7_1(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s, _, clk := sealServer(t, "alice")
		clk.advance(time.Hour)
		var violations atomic.Int64
		var wg sync.WaitGroup
		stop := make(chan struct{})
		for w := 0; w < 4; w++ {
			delay := time.Duration(rng.Intn(50)) * time.Microsecond
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					_, end, err := s.beginToolCall()
					if err != nil {
						continue
					}
					// With this call in flight, no seal may be placed.
					resp, serr := s.SealSession(context.Background(), &adapterv1.SealSessionRequest{
						SessionId: &adapterv1.SessionId{Value: "alice"}, IdleSeconds: 1,
					})
					if serr == nil && resp.GetSealed() {
						violations.Add(1)
					}
					time.Sleep(delay)
					end()
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				resp, err := s.SealSession(context.Background(), &adapterv1.SealSessionRequest{
					SessionId: &adapterv1.SessionId{Value: "alice"}, IdleSeconds: 1,
				})
				if err == nil && resp.GetSealed() {
					// Lift the seal so the next interleaving starts unsealed.
					s.mu.Lock()
					if st := s.slots["alice"]; st != nil {
						st.sealed = false
					}
					s.mu.Unlock()
				}
				clk.advance(time.Hour)
			}
		}()
		time.Sleep(time.Duration(5+rng.Intn(10)) * time.Millisecond)
		close(stop)
		wg.Wait()
		if v := violations.Load(); v != 0 {
			t.Fatalf("seed %d: %d seals observed with a call in flight", seed, v)
		}
		s.mu.Lock()
		if st := s.slots["alice"]; st != nil && st.toolCalls != 0 {
			t.Fatalf("seed %d: call count = %d after every call returned, want 0", seed, st.toolCalls)
		}
		s.mu.Unlock()
	}
}
