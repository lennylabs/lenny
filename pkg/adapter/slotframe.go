// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// frameSessionID returns the `sessionId` field of a §28.5.3 JSONL frame,
// or the empty string when the frame does not parse as a JSON object or
// carries no `sessionId`. It is the demultiplexing key the Attach handler
// uses to route a runtime output frame to the session that owns it. A
// session-scoped frame that yields the empty string carries no address,
// and demuxSessionOutput decides its disposition from the pod's slot
// count. spec: §28.5.3.
func frameSessionID(line []byte) string {
	var probe struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return ""
	}
	return probe.SessionID
}

// stampSessionID injects sessionID into an outbound §28.5.3 envelope
// before the adapter forwards it to the shared runtime, so the runtime's
// dispatch loop and the per-session demultiplexing both key on it. Every
// session is bound to a slot on every pod, so the stamp is unconditional.
// A frame that is not a JSON object is returned unchanged so a
// non-envelope frame is not dropped; a malformed object surfaces an error
// rather than being written with no address, which would misroute it on a
// pod holding more than one slot (fail closed).
// spec: §6.4; §28.5.3 — inbound frames carry sessionId on every pod.
func stampSessionID(frame []byte, sessionID string) ([]byte, error) {
	trimmed := bytes.TrimSpace(frame)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return frame, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, fmt.Errorf("stamp sessionId on outbound frame: %w", err)
	}
	id, err := json.Marshal(sessionID)
	if err != nil {
		return nil, fmt.Errorf("stamp sessionId: encode %q: %w", sessionID, err)
	}
	obj["sessionId"] = id
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("stamp sessionId: re-encode outbound frame: %w", err)
	}
	return out, nil
}
