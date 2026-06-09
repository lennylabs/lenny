// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"sync"
)

// SDKWarmInProcessRuntime is the §6.1 SDK-warm embedded-model runtime: an
// InProcessRuntime that also implements SDKWarmRuntime so a first-party
// runtime declaring capabilities.preConnect: true can pre-connect its
// agent loop at warm time and be pointed at a session's workspace
// (ConfigureWorkspace) rather than started from cold (StartSession).
//
// For the echo reference runtime the "SDK" is the in-process §15.4.1 loop:
// PreConnect marks it warm, ConfigureWorkspace binds the warm loop to the
// session's cwd (equivalent to Start), and DemoteSDK tears the loop down
// so a subsequent StartSession runs the pod-warm path. A real agent SDK
// substitutes its own connect/teardown for these steps; the contract the
// gateway drives is identical.
type SDKWarmInProcessRuntime struct {
	*InProcessRuntime

	mu           sync.Mutex
	preConnected bool
}

// NewSDKWarmInProcessRuntime returns an embedded-model runtime that
// satisfies the §6.1 SDK-warm fast path over loop.
func NewSDKWarmInProcessRuntime(loop RuntimeLoop) *SDKWarmInProcessRuntime {
	return &SDKWarmInProcessRuntime{InProcessRuntime: NewInProcessRuntime(loop)}
}

// PreConnect marks the embedded runtime SDK-warm at pod warm time. For the
// echo loop there is no remote connection to establish, so PreConnect
// records readiness; a real SDK runtime starts its provider connection
// here. It is idempotent.
func (r *SDKWarmInProcessRuntime) PreConnect(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.preConnected = true
	return nil
}

// ConfigureWorkspace binds the pre-connected runtime to the session and
// the finalized workspace cwd. For the echo loop this starts the in-process
// loop for the session (the warm equivalent of StartSession); Start is
// idempotent so a repeat for the same session is a no-op success.
func (r *SDKWarmInProcessRuntime) ConfigureWorkspace(ctx context.Context, sessionID, _ string) error {
	return r.Start(ctx, sessionID)
}

// DemoteSDK tears the pre-connected runtime down so the pod falls back to
// pod-warm. It closes any bound session loop and clears the warm flag, so a
// subsequent StartSession starts the runtime fresh.
func (r *SDKWarmInProcessRuntime) DemoteSDK(ctx context.Context) error {
	r.mu.Lock()
	r.preConnected = false
	r.mu.Unlock()
	// Close tolerates an unbound runtime (no session yet) as a no-op.
	return r.InProcessRuntime.Close(ctx, r.boundSession())
}

// ForceTerminate hard-stops the pre-connected echo loop without waiting for
// it to drain, backing the §6.1 line 67 force-terminate step the adapter
// runs when a bounded DemoteSDK overruns its timeout. It clears the warm
// flag and force-closes the in-process loop. A real agent-SDK runtime
// SIGKILLs its SDK subprocess here instead. spec: §6.1 line 67.
func (r *SDKWarmInProcessRuntime) ForceTerminate() {
	r.mu.Lock()
	r.preConnected = false
	r.mu.Unlock()
	r.InProcessRuntime.ForceClose()
}

// boundSession returns the session the embedded loop is currently bound
// to, or empty when idle. It lets DemoteSDK address the live loop without
// the caller threading the session id.
func (r *SDKWarmInProcessRuntime) boundSession() string {
	r.InProcessRuntime.mu.Lock()
	defer r.InProcessRuntime.mu.Unlock()
	return r.InProcessRuntime.session
}

// compile-time assertion that SDKWarmInProcessRuntime satisfies the §6.1
// SDK-warm contract the adapter drives, including the force-terminate hook
// the §6.1 line 67 SIGTERM teardown uses on a DemoteSDK overrun.
var (
	_ SDKWarmRuntime  = (*SDKWarmInProcessRuntime)(nil)
	_ ForceTerminator = (*SDKWarmInProcessRuntime)(nil)
)
