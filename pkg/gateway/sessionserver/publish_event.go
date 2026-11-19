// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// publishEvent is the gateway-side helper that publishes a session
// event when the event bus is wired. eventData is marshalled to
// JSON; a marshalling failure publishes an empty object. The tenant id
// is recorded on the bus so its Subscribe enforces the §7.2
// tenant-isolation predicate. A non-empty tenant id is the production
// contract; tests may pass "" for the untenanted code path. The bus is
// the stream proxy's, reached through GWSEAM-Publish.
func (s *Server) publishEvent(tenantID, sessionID, eventType string, payload any) {
	// The §6.2 agent_output / tool_use events the
	// adapter surfaces (published here as `response`, `response_degraded`,
	// and `tool_use*`) are qualifying activity that resets the §11.3
	// `maxIdleTime` clock so a streaming session is not reaped as idle.
	// Inbound, lifecycle, and warning events (status_change,
	// message_delivered, workspace_plan_warning, session.resumed,
	// session_complete) are not agent activity and do not stamp. F-11.3.7.
	if s.activityStamper != nil && isAgentActivityEvent(eventType) {
		s.activityStamper.Stamp(tenantID, sessionID)
	}
	if s.events == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte("{}")
	}
	s.events.Publish(tenantID, sessionID, eventType, string(data), s.clock())
}

// isAgentActivityEvent reports whether a published session event type is a
// §6.2 qualifying agent activity (agent_output or tool_use
// from the adapter). The gateway publishes agent output as `response` /
// `response_degraded` and tool calls under the `tool_use` prefix. F-11.3.7.
func isAgentActivityEvent(eventType string) bool {
	switch eventType {
	case "response", "response_degraded", "agent_output":
		return true
	}
	return strings.HasPrefix(eventType, "tool_use")
}

// recordTTFTOnce observes the §6.3 / §16.1 TTFT
// histogram the first time row receives an agent-originated streaming
// event (the gateway's `response` event type). T0 is the session's
// `CreatedAt` (the POST /v1/sessions admission instant per §15.1).
// Subsequent events for the same session are no-ops so the histogram
// only counts each session once. spec: §6.3, §16.1.
func (s *Server) recordTTFTOnce(row sessionstore.Session, eventType string) {
	if s.observeTimeToFirstToken == nil {
		return
	}
	if eventType != "response" {
		return
	}
	if _, loaded := s.firstTokenObserved.LoadOrStore(row.ID, struct{}{}); loaded {
		return
	}
	runtimeClass, ok := s.runtimeClassOf(row.IsolationProfile)
	if !ok {
		// A row with no resolved isolation profile would mislabel the
		// histogram; skip rather than emit an empty runtime_class.
		return
	}
	seconds := s.clock().Sub(row.CreatedAt).Seconds()
	if seconds < 0 {
		seconds = 0
	}
	s.observeTimeToFirstToken(row.PoolRef, runtimeClass, string(row.IsolationProfile), seconds)
}
