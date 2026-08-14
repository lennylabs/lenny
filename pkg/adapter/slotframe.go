// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// frameSlotID returns the `slotId` field of a §28.5.3 JSONL frame, or the
// empty string when the frame does not parse as a JSON object or carries
// no `slotId`. It is the demultiplexing key the Attach handler uses to
// route a runtime output frame to the slot that owns it. spec: §28.5.3 — outbound frames carry slotId when maxConcurrentSessions > 1.
func frameSlotID(line []byte) string {
	var probe struct {
		SlotID string `json:"slotId"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return ""
	}
	return probe.SlotID
}

// frameSlotAddress returns the `slotId` a §28.5.3 JSONL frame carries as
// an address, and whether that address is well formed. A frame that omits
// `slotId` addresses the empty slot and reports ("", true). A frame whose
// `slotId` is present but is not a JSON string carries no address to
// compare, and reports ("", false) so the caller can fail closed; the
// published JSONL schema rejects those values, and collapsing one to the
// empty address would let a malformed frame pass as untagged.
//
// It is separate from frameSlotID because demultiplexing and addressing
// need different answers for the same malformed frame: the demultiplexer
// treats an unreadable key as "no key" and delivers the frame to every
// slot stream, while an addressing decision with per-session side effects
// must reject it. spec: §28.5.3 — outbound frames carry slotId when
// maxConcurrentSessions > 1.
func frameSlotAddress(line []byte) (string, bool) {
	// The frame is decoded field by field rather than into a typed probe
	// because the decision turns on whether `slotId` is present at all: a
	// typed probe cannot separate an absent field from a JSON null, and
	// the schema rejects both a null and any other non-string value.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(line, &obj); err != nil {
		return "", false
	}
	raw, present := obj["slotId"]
	if !present {
		return "", true
	}
	// A JSON null decodes into a string without error, leaving the zero
	// value, so the value is required to open as a JSON string before it
	// is decoded.
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return "", false
	}
	var id string
	if err := json.Unmarshal(trimmed, &id); err != nil {
		return "", false
	}
	return id, true
}

// stampSlotID injects slotID into an outbound §28.5.3 envelope before the
// adapter forwards it to the shared runtime, so the runtime's dispatch
// loop and the per-slot demultiplexing both key on it. A frame that is
// not a JSON object is returned unchanged so a non-envelope frame is not
// dropped; a malformed object surfaces an error rather than being written
// with no slotId, which would misroute it on a concurrent pod (fail
// closed). spec: §6.4; §28.5.3 — inbound frames
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
