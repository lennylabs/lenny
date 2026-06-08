// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"testing"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §5.1 / §15.4.3 — a runtime that opened neither the lifecycle
// channel nor the platform MCP server is observationally Basic (stdin/
// stdout binary protocol only).
func TestObservedIntegrationLevelBasic_spec_5_1(t *testing.T) {
	s := New("test")
	if lvl := s.observedIntegrationLevel(context.Background(), 0); lvl != observedLevelBasic {
		t.Errorf("level = %q, want basic", lvl)
	}
}

// spec: §5.1 — a runtime that connected to the platform MCP server but not
// the lifecycle channel is observationally Standard.
func TestObservedIntegrationLevelStandard_spec_5_1(t *testing.T) {
	s := New("test")
	s.markMCPHandshakeSeen()
	if lvl := s.observedIntegrationLevel(context.Background(), 0); lvl != observedLevelStandard {
		t.Errorf("level = %q, want standard", lvl)
	}
}

// spec: §5.1 — a runtime that completed the §4.7 lifecycle handshake is
// observationally Full, the §5.1 "runtime source of truth", regardless of
// the MCP signal.
func TestObservedIntegrationLevelFull_spec_5_1(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	s := New("test")
	s.Lifecycle = lc
	if lvl := s.observedIntegrationLevel(context.Background(), time.Second); lvl != observedLevelFull {
		t.Errorf("level = %q, want full", lvl)
	}
}

// spec: §5.1 — when the adapter offers a lifecycle channel but the runtime
// never completes the handshake within the wait window, the runtime is
// classified by its MCP signal: Standard if it reached MCP, else Basic.
// This is the underperformance case the §5.1 admission check catches.
func TestObservedIntegrationLevelLifecycleNotCompleted_spec_5_1(t *testing.T) {
	lc, _ := startLifecycleChannel(t) // dialled but no handshake written
	s := New("test")
	s.Lifecycle = lc

	if lvl := s.observedIntegrationLevel(context.Background(), 50*time.Millisecond); lvl != observedLevelBasic {
		t.Errorf("no-handshake, no-mcp level = %q, want basic", lvl)
	}
	s.markMCPHandshakeSeen()
	if lvl := s.observedIntegrationLevel(context.Background(), 50*time.Millisecond); lvl != observedLevelStandard {
		t.Errorf("no-handshake, mcp-seen level = %q, want standard", lvl)
	}
}

// spec: §5.1 — the GetObservedIntegrationLevel RPC reports the classified
// level; wait_ms is honored as the lifecycle-handshake budget.
func TestGetObservedIntegrationLevelRPC_spec_5_1(t *testing.T) {
	s := New("test")
	s.markMCPHandshakeSeen()
	resp, err := s.GetObservedIntegrationLevel(context.Background(), &adapterv1.GetObservedIntegrationLevelRequest{WaitMs: 0})
	if err != nil {
		t.Fatalf("GetObservedIntegrationLevel: %v", err)
	}
	if resp.GetObservedLevel() != observedLevelStandard {
		t.Errorf("observed level = %q, want standard", resp.GetObservedLevel())
	}
}

// spec: §5.1 — releaseSession clears the MCP-handshake signal so the next
// session's runtime must reconnect to be observed at Standard.
func TestObservedLevelResetOnReleaseSession_spec_5_1(t *testing.T) {
	s := New("test")
	s.markMCPHandshakeSeen()
	s.releaseSession()
	if lvl := s.observedIntegrationLevel(context.Background(), 0); lvl != observedLevelBasic {
		t.Errorf("after releaseSession level = %q, want basic", lvl)
	}
}
