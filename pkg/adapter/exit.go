// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"log/slog"
	"time"
)

// GracefulStopper is the part of the adapter's gRPC server the exit path
// uses. *grpc.Server satisfies it.
type GracefulStopper interface {
	GracefulStop()
}

// ExitOnSignal runs the adapter's process-exit path, which an adapter
// binary calls once when it receives SIGTERM or an interrupt. It runs the
// §6.1 bounded DemoteSDK teardown (a no-op on a pod-warm pod or an
// SDK-warm pod that never pre-connected), closes the CH-RUNTIMEOPS
// listener and connection when one is configured, and gracefully stops
// the gRPC server.
//
// The exit path writes no session frame of its own. Per the §28.5.3
// Session frame writes table, the runtime connection (or, in the embedded
// model, the adapter process) ends with the pod, and only a teardown that
// runs before the exit writes a frame: the SIGTERM-time DemoteSDK writes
// the session_end its own row states. Adding a session_end write here
// would put a second end on a session whose teardown already wrote one,
// or an end on a session the gateway never tore down.
//
// spec: §28.5.3 (CH-MSGSOCK, Session frame writes: Pod exit), §6.1.
//
// On a supervised pod the pod's termination sets the deleting end
// disposition first. A service-mode pod's process is then ended with an end
// bounded by demoteTimeout, a preconnect pod's through ShutdownDemoteSDK, and
// a session-mode pod's process is not ended here: it runs on through the
// drain and ends as the kubelet row of the Signals table states.
// spec: §4.7.10 (Runtime supervisor, Runtime exits; Runtime supervisor,
// Signals); §6.1 (Adapter SIGTERM behavior during sdk_connecting).
func (s *Server) ExitOnSignal(demoteTimeout time.Duration, srv GracefulStopper) {
	if rt := s.supervisedRuntime(); rt != nil {
		rt.SetEndDisposition(EndDeleting)
		// A launch in flight completes before the branch below decides
		// whether a process was launched, so the end it sends reaches the
		// supervisor after that launch.
		rt.SettleLaunch()
		// That launch reset the disposition of the generation it started,
		// so the deleting value is set again for the generation the end
		// below targets.
		rt.SetEndDisposition(EndDeleting)
		if rt.Mode() == LaunchService {
			s.endServiceProcess(rt, demoteTimeout)
		}
	}
	s.ShutdownDemoteSDK(demoteTimeout)
	if s.Lifecycle != nil {
		_ = s.Lifecycle.Close()
	}
	if srv != nil {
		srv.GracefulStop()
	}
}

// endServiceProcess ends a service-mode pod's launched process within
// timeout, logging a failed end; the adapter is exiting either way, and its
// exit closes CH-SUPERVISE, on which the supervisor ends every process.
func (s *Server) endServiceProcess(rt *SupervisedRuntime, timeout time.Duration) {
	if !rt.HasLaunched() {
		return
	}
	if timeout <= 0 {
		timeout = DefaultDemoteTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := rt.EndProcess(ctx, gracefulEnd(ctx)); err != nil {
		slog.Warn("service_runtime_end_failed", "error", err)
	}
}
