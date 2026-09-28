// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// bufferTarget selects the §7.2 destination for a buffered message:
// the session inbox (paths 3/5/6) or the dead-letter queue (path 7).
type bufferTarget int

const (
	bufferTargetInbox bufferTarget = iota
	bufferTargetDLQ
)

// bufferIncomingMessages buffers the selected non-reply messages into
// the session inbox or DLQ per the §7.2 routing decision. It returns
// whether the overflow policy evicted any message (so the caller emits
// a `dropped` receipt), the inbox depth after the enqueue (for the
// `queued` receipt's `queueDepth`), and an error when the messaging
// coordinator is not wired (the caller maps it to inbox_unavailable).
// Each payload is serialized as the §15.4 MessageEnvelope so the
// dequeue path on resume re-delivers the original wire form.
//
// spec: §7.2.
func (s *Server) bufferIncomingMessages(ctx context.Context, row sessionstore.Session, all []MessagePayload, idx []int, target bufferTarget, ttl time.Duration) (dropped bool, depth int, err error) {
	for _, i := range idx {
		m := all[i]
		payload, mErr := json.Marshal(m)
		if mErr != nil {
			return dropped, depth, mErr
		}
		id := m.ID
		if id == "" {
			id = "msg_" + session.NewID()
		}
		msg := sessioninbox.Message{
			MessageID:  id,
			Payload:    payload,
			EnqueuedAt: s.clock(),
		}
		var evicted *sessioninbox.Message
		switch target {
		case bufferTargetInbox:
			evicted, err = s.messaging.EnqueueInbox(ctx, row.TenantID, row.ID, msg)
		case bufferTargetDLQ:
			evicted, err = s.messaging.EnqueueDLQ(ctx, row.TenantID, row.ID, msg, ttl)
		}
		if err != nil {
			return dropped, depth, err
		}
		if evicted != nil {
			dropped = true
		}
	}
	if target == bufferTargetInbox {
		depth, _ = s.messaging.InboxLen(ctx, row.TenantID, row.ID)
	}
	return dropped, depth, nil
}
