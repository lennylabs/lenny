// SPDX-License-Identifier: MIT

package adapter

// This file wires a SupervisedRuntime into the Server: the Runtime exits
// handling under the registry critical section, the SDK-warm wrapper on the
// preconnect launch modes, sdkConnected at each launch on those modes, and
// the warm-up manifest. spec: §4.7.10 (Runtime supervisor).

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
	rt.SetWarmUpManifestWriter(s.WriteWarmUpManifest)
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

// WriteWarmUpManifest writes the manifest the warm-up launch's process reads,
// before any session: the session-independent fields and a fresh nonce.
// spec: §4.7.10 (Runtime supervisor, Launch); §4.7.6.
func (s *Server) WriteWarmUpManifest() error {
	_, err := s.writeSessionManifest(manifestInputs{})
	return err
}
