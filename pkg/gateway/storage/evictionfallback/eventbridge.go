// SPDX-License-Identifier: MIT

package evictionfallback

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// SessionEventsBridge adapts the session event bus to the EventEmitter
// interface so the gateway can publish §4.4
// events onto the regular session event stream. It is a thin
// translation layer; production callers wire the stream proxy's bus,
// reached through GWSEAM-Publish, into the Writer's Events field via
// this bridge.
//
// spec: §4.4 (Event / Checkpoint Store)
type SessionEventsBridge struct {
	// Bus is the §15.1 session event bus the gateway publishes onto.
	// Required. A nil Bus makes EmitSessionLost a logged no-op.
	Bus seam.Events
	// Now overrides time.Now for tests. Nil selects time.Now.
	Now func() time.Time
}

// EmitSessionLost publishes a `session.lost` event with the supplied
// reason and fields onto the bridge's Bus. Best-effort: when the bus
// is nil, the bridge logs the loss and returns (so the §4.4
// "publishes ... before the preStop hook exits; if the event stream
// itself is unavailable the emission is skipped (logged only)"
// contract is honored).
func (b *SessionEventsBridge) EmitSessionLost(_ context.Context, sessionID, reason string, fields map[string]any) {
	payload := map[string]any{"reason": reason}
	for k, v := range fields {
		payload[k] = v
	}
	body, err := json.Marshal(payload)
	if err != nil {
		body = []byte(`{}`)
	}
	if b.Bus == nil {
		log.Printf("evictionfallback: session.lost (event bus unavailable) session=%s reason=%s payload=%s",
			sessionID, reason, body)
		return
	}
	b.Bus.Publish("", sessionID, "session.lost", string(body), b.now())
}

func (b *SessionEventsBridge) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now().UTC()
}
