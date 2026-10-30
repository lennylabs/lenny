// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"

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

// bufferIncomingMessages buffers the selected non-reply messages into the
// session inbox per the §7.2 routing decision (paths 3, 5, and 6). It returns
// whether the overflow policy evicted any message (so the caller emits a
// `dropped` receipt), the inbox depth after the enqueue (for the `queued`
// receipt's `queueDepth`), and an error when the messaging coordinator is not
// wired (the caller maps it to inbox_unavailable). A DLQ hold goes through
// holdMessage instead. Each payload is serialized as the §15.4
// MessageEnvelope so the delivery of held messages re-delivers the original
// wire form.
//
// spec: §7.2.
func (s *Server) bufferIncomingMessages(ctx context.Context, row sessionstore.Session, all []MessagePayload, idx []int) (dropped bool, depth int, err error) {
	msgs, err := s.inboxMessages(all, idx)
	if err != nil {
		return false, 0, err
	}
	for _, msg := range msgs {
		evicted, err := s.messaging.EnqueueInbox(ctx, row.TenantID, row.ID, msg)
		if err != nil {
			return dropped, depth, err
		}
		if evicted != nil {
			dropped = true
		}
	}
	depth, _ = s.messaging.InboxLen(ctx, row.TenantID, row.ID)
	return dropped, depth, nil
}

// inboxMessages serializes the selected payloads as buffered messages, each
// stamped with its message id (gateway-assigned when absent) and the
// acceptance instant.
func (s *Server) inboxMessages(all []MessagePayload, idx []int) ([]sessioninbox.Message, error) {
	out := make([]sessioninbox.Message, 0, len(idx))
	for _, i := range idx {
		m := all[i]
		payload, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		id := m.ID
		if id == "" {
			id = "msg_" + session.NewID()
		}
		out = append(out, sessioninbox.Message{
			MessageID:  id,
			Payload:    payload,
			EnqueuedAt: s.clock(),
		})
	}
	return out, nil
}
