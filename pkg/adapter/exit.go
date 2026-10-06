// SPDX-License-Identifier: MIT

package adapter

import "time"

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
func (s *Server) ExitOnSignal(demoteTimeout time.Duration, srv GracefulStopper) {
	s.ShutdownDemoteSDK(demoteTimeout)
	if s.Lifecycle != nil {
		_ = s.Lifecycle.Close()
	}
	if srv != nil {
		srv.GracefulStop()
	}
}
