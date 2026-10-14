// SPDX-License-Identifier: MIT

package adapter

import (
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// beginToolCall resolves the session a CH-MCP-PLATFORM or CH-MCP-CONNECTOR
// tools/call runs for and counts the call as that session's activity: in one
// hold of s.mu it resolves the calling session as callingSession does,
// increments the entry's in-flight call count, and stamps the entry. end
// decrements the count and stamps the same entry again; it acts on the entry
// pointer resolved here, so a call that outlives its entry changes no entry
// created after it, and a second call to end does nothing. While the count
// is above zero SealSession answers not sealed (§4.7.1 **Session seal.**).
//
// It returns callingSession's FailedPrecondition when the resolution names
// no session, with no entry changed. A sole session the registry holds no
// entry for is forwarded and counted on no entry.
//
// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.3 (CH-MCP-PLATFORM,
// CH-MCP-CONNECTOR)
func (s *Server) beginToolCall() (sessionID string, end func(), err error) {
	s.mu.Lock()
	sessionID = s.soleSessionLocked()
	if sessionID == "" {
		s.mu.Unlock()
		return "", nil, status.Error(codes.FailedPrecondition,
			"the pod's shared runtime process is serving more than one session; the intra-pod MCP surface cannot name the calling session")
	}
	entry := s.slots[sessionID]
	if entry != nil {
		entry.toolCalls++
		s.stampLocked(entry)
	}
	s.mu.Unlock()
	var once sync.Once
	end = func() {
		once.Do(func() {
			if entry == nil {
				return
			}
			s.mu.Lock()
			entry.toolCalls--
			s.stampLocked(entry)
			s.mu.Unlock()
		})
	}
	return sessionID, end, nil
}
