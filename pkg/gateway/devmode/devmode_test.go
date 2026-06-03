// SPDX-License-Identifier: MIT

package devmode

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// spec: §17.4 line 268 — the hard startup assertion. The gateway starts
// only when dev mode is on or an upstream TLS terminator is acknowledged.
func TestResolveStartupGate_spec_17_4_268(t *testing.T) {
	cases := []struct {
		name                  string
		devMode               bool
		tlsTerminatedUpstream bool
		wantErr               bool
	}{
		{"neither set refuses to start", false, false, true},
		{"dev mode permits relaxed TLS", true, false, false},
		{"upstream termination acknowledged", false, true, false},
		{"both set is permitted", true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ResolveStartupGate(tc.devMode, tc.tlsTerminatedUpstream)
			if tc.wantErr && err == nil {
				t.Fatalf("ResolveStartupGate(%v,%v) = nil, want error", tc.devMode, tc.tlsTerminatedUpstream)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ResolveStartupGate(%v,%v) = %v, want nil", tc.devMode, tc.tlsTerminatedUpstream, err)
			}
			if tc.wantErr && err != ErrTLSRequired {
				t.Fatalf("want ErrTLSRequired, got %v", err)
			}
		})
	}
}

// spec: §17.4 line 269 — the warning text is the exact verbatim string.
func TestTLSDisabledWarning_verbatim_spec_17_4_269(t *testing.T) {
	const want = "WARNING: TLS disabled — dev mode active. Do not use in production."
	if TLSDisabledWarning != want {
		t.Fatalf("TLSDisabledWarning = %q, want %q", TLSDisabledWarning, want)
	}
}

// spec: §17.4 line 269 — the warning is logged once at startup and then
// repeated on the configured cadence while the process runs.
func TestStartWarnTicker_emitsAtStartupAndRepeats_spec_17_4_269(t *testing.T) {
	var mu sync.Mutex
	var got []string
	logf := func(msg string) {
		mu.Lock()
		got = append(got, msg)
		mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartWarnTicker(ctx, 10*time.Millisecond, logf)

	// The immediate startup emission is synchronous.
	mu.Lock()
	startupCount := len(got)
	mu.Unlock()
	if startupCount < 1 {
		t.Fatalf("expected at least the startup warning, got %d", startupCount)
	}

	// Wait for at least one recurring tick beyond the startup emission.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ticker did not repeat: only %d emissions", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	for i, m := range got {
		if !strings.Contains(m, "TLS disabled") {
			t.Fatalf("emission %d = %q, want the dev-mode TLS warning", i, m)
		}
	}
	mu.Unlock()
}

// spec: §17.4 line 269 — cancelling the context stops the broadcast.
func TestStartWarnTicker_stopsOnContextCancel_spec_17_4_269(t *testing.T) {
	var mu sync.Mutex
	count := 0
	logf := func(string) {
		mu.Lock()
		count++
		mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	StartWarnTicker(ctx, 5*time.Millisecond, logf)
	time.Sleep(40 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	stable := count
	mu.Unlock()
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	final := count
	mu.Unlock()
	if final != stable {
		t.Fatalf("ticker kept emitting after cancel: %d then %d", stable, final)
	}
}

// A non-positive interval falls back to the spec's 60s cadence rather
// than spinning a zero-duration ticker (which panics).
func TestStartWarnTicker_nonPositiveIntervalFallsBack(t *testing.T) {
	logged := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	StartWarnTicker(ctx, 0, func(m string) {
		select {
		case logged <- m:
		default:
		}
	})
	select {
	case <-logged:
	case <-time.After(time.Second):
		t.Fatal("expected the startup warning even with a zero interval")
	}
}
