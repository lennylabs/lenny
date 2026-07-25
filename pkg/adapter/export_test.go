// SPDX-License-Identifier: MIT

package adapter

// MarkEvicting sets the pod's termination (evicting) flag so an external
// checkpoint-stream test can exercise the best-effort eviction snapshot path
// that fires only while the pod is itself terminating. Production code sets
// the flag from the SIGTERM/eviction handler via setEvicting.
func (s *Server) MarkEvicting() { s.setEvicting() }
