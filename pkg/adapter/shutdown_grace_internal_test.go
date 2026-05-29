// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"testing"
	"time"
)

// TestResolveShutdownGracePrefersCtxDeadline asserts the §11.4 step-3
// graceful window plumbed onto ctx by Server.Shutdown wins over the
// runtime-configured grace and the package default, so the adapter
// honors the gateway's deadline_ms instead of an internal default.
// spec: §11.4 line 258.
func TestResolveShutdownGracePrefersCtxDeadline_spec_11_4_258(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	got := resolveShutdownGrace(ctx, 1*time.Second, 5*time.Second)
	if got <= 0 || got > 4*time.Second {
		t.Errorf("resolveShutdownGrace with 4s ctx deadline = %v, want (0, 4s]", got)
	}
}

// TestResolveShutdownGraceFallsBackToConfigured asserts that without a
// ctx deadline, the runtime's configured grace (MCPRuntime.ShutdownGrace)
// is preferred over the package default. spec: §11.4 line 258.
func TestResolveShutdownGraceFallsBackToConfigured_spec_11_4_258(t *testing.T) {
	got := resolveShutdownGrace(context.Background(), 7*time.Second, 5*time.Second)
	if got != 7*time.Second {
		t.Errorf("resolveShutdownGrace = %v, want 7s (configured wins when ctx has no deadline)", got)
	}
}

// TestResolveShutdownGraceFallsBackToDefault asserts the package default
// fires only when no ctx deadline is set and configured is non-positive.
// spec: §11.4 line 258.
func TestResolveShutdownGraceFallsBackToDefault_spec_11_4_258(t *testing.T) {
	got := resolveShutdownGrace(context.Background(), 0, 9*time.Second)
	if got != 9*time.Second {
		t.Errorf("resolveShutdownGrace = %v, want 9s (default wins when ctx + configured absent)", got)
	}
}

// TestResolveShutdownGraceIgnoresExpiredCtxDeadline asserts an already
// elapsed ctx deadline does not collapse the grace window to zero —
// the adapter still falls back to configured/default rather than
// SIGKILL'ing immediately. spec: §11.4 line 258.
func TestResolveShutdownGraceIgnoresExpiredCtxDeadline_spec_11_4_258(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	got := resolveShutdownGrace(ctx, 0, 5*time.Second)
	if got != 5*time.Second {
		t.Errorf("resolveShutdownGrace with expired ctx = %v, want 5s (default fallback)", got)
	}
}
