// SPDX-License-Identifier: MIT

package adapter

import "fmt"

// This file wires a SupervisedRuntime into the Server: the Runtime exits
// handling under the registry critical section, the SDK-warm wrapper on the
// preconnect launch modes, sdkConnected at each launch on those modes, and
// the warm-up launch's preparation. spec: §4.7.10 (Runtime supervisor).

// supervisedCarrier is implemented by SupervisedRuntime and, through
// embedding, by its SDK-warm wrapper.
type supervisedCarrier interface {
	Supervised() *SupervisedRuntime
}

// supervisedRuntime returns the pod's supervised runtime, nil on a pod that
// runs no supervisor.
func (s *Server) supervisedRuntime() *SupervisedRuntime {
	if c, ok := s.Runtime.(supervisedCarrier); ok {
		return c.Supervised()
	}
	return nil
}

// SupervisedRuntimeOrNil returns the pod's supervised runtime, nil on a pod
// that runs no supervisor.
func (s *Server) SupervisedRuntimeOrNil() *SupervisedRuntime { return s.supervisedRuntime() }

// UseSupervisedRuntime makes rt the Server's runtime. On the preconnect launch
// modes the Server holds the SDK-warm wrapper, so ConfigureWorkspace and
// DemoteSDK are served, and every launch sets sdkConnected when it is sent;
// on the other modes those RPCs return Unimplemented. Every end of a runtime
// generation is handled by handleRuntimeEnd under s.mu.
// spec: §4.7.10 (Runtime supervisor, SDK-warm; Runtime supervisor, Runtime
// exits).
func (s *Server) UseSupervisedRuntime(rt *SupervisedRuntime) {
	rt.setEndHandler(func(gen uint64, ev RuntimeEndEvent) { s.handleRuntimeEnd(rt, gen, ev) })
	rt.SetWarmUpPreparer(s.PrepareWarmUpLaunch)
	if rt.Mode().Preconnect() {
		rt.setLaunchHook(func() {
			s.mu.Lock()
			s.sdkConnected = true
			s.mu.Unlock()
		})
		s.Runtime = SupervisedSDKWarmRuntime{rt}
		return
	}
	s.Runtime = rt
}

// PrepareWarmUpLaunch makes ready what the warm-up launch's process reads and
// dials, before any session: it writes the manifest with the
// session-independent fields and a fresh nonce, then binds the platform MCP
// listener the manifest advertises, armed with that nonce. A Standard-level
// process dials that listener at its start, so the warm-up launch may follow
// only once it is bound. That bind is the pod's one platform MCP start: the
// first session claim takes it over without restarting the server, so the
// server keeps validating the warm-up nonce across the session's manifest
// write and the warm-up process's connection and handshake record stand. A
// type: mcp runtime dials no platform MCP server, so none is bound for it.
// spec: §4.7.10 (Runtime supervisor, Launch); §4.7.6 (mcpNonce row); §15.4.3.
func (s *Server) PrepareWarmUpLaunch() error {
	nonce, err := s.writeSessionManifest(manifestInputs{})
	if err != nil {
		return fmt.Errorf("write warm-up manifest: %w", err)
	}
	if s.RuntimeKind == RuntimeKindMCP {
		return nil
	}
	if err := s.startPlatformMCP(nonce); err != nil {
		return fmt.Errorf("bind the warm-up platform MCP listener: %w", err)
	}
	s.markWarmUpPlatformMCPArmed()
	return nil
}

// markWarmUpPlatformMCPArmed records a running platform MCP server as the
// warm-up binding the first session claim takes over. It records nothing
// when no server runs (no MCP socket configured). spec: §15.4.3.
func (s *Server) markWarmUpPlatformMCPArmed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mcpWarmUpArmed = s.mcpCancel != nil
}
