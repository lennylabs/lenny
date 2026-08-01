// SPDX-License-Identifier: MIT

package runtime

// MessageEnvelope is the canonical envelope a runtime author reads.
//
// spec: spec/15_external-api-surface.md#messageenvelope--unified-message-format (the envelope and its fields)
type MessageEnvelope struct {
	Kind string
}

// Frame writes the envelope onto the adapter connection.
//
// spec: §28.5.1 (the framing of the connection)
func (e MessageEnvelope) Frame() []byte { return nil }
