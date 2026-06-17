// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// frameSlotID returns the `slotId` field of a §15.4.1 JSONL frame, or the
// empty string when the frame does not parse as a JSON object or carries
// no `slotId`. It is the demultiplexing key the Attach handler uses to
// route a runtime output frame to the slot that owns it. spec: §15.4.1
// line 1459 — outbound frames carry slotId when maxConcurrentSessions > 1.
func frameSlotID(line []byte) string {
	var probe struct {
		SlotID string `json:"slotId"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return ""
	}
	return probe.SlotID
}

// stampSlotID injects slotID into an outbound §15.4.1 envelope before the
// adapter forwards it to the shared runtime, so the runtime's dispatch
// loop and the per-slot demultiplexing both key on it. A frame that is
// not a JSON object is returned unchanged so a non-envelope frame is not
// dropped; a malformed object surfaces an error rather than being written
// with no slotId, which would misroute it on a concurrent pod (fail
// closed). spec: §6.4 lines 401-405; §15.4.1 line 1459 — inbound frames
// carry slotId when maxConcurrentSessions > 1.
func stampSlotID(frame []byte, slotID string) ([]byte, error) {
	trimmed := bytes.TrimSpace(frame)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return frame, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, fmt.Errorf("stamp slotId on outbound frame: %w", err)
	}
	id, err := json.Marshal(slotID)
	if err != nil {
		return nil, fmt.Errorf("stamp slotId: encode %q: %w", slotID, err)
	}
	obj["slotId"] = id
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("stamp slotId: re-encode outbound frame: %w", err)
	}
	return out, nil
}
