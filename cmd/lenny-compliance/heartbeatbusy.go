// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// heartbeatBusyFrame is the part of a stdout frame the heartbeat busy check
// reads.
type heartbeatBusyFrame struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Busy      *bool  `json:"busy"`
}

// checkHeartbeatBusy implements the §15.4.6 Basic-level heartbeat busy row.
// After session_start, the harness writes a message for the session and then
// a heartbeat naming it, before it reads the response. When the binary
// writes the heartbeat_ack before that response, the ack must carry busy:
// true and the session's sessionId. An ack that follows the response passes,
// so a serial runtime that answers the heartbeat after its turn conforms.
// The check reads frames until it holds both the response and the ack.
//
// spec: §15.4.6 (Conformance Test Suite), §28.5.3 (CH-MSGSOCK)
func checkHeartbeatBusy(binary string, timeout time.Duration, verbose bool) (string, error) {
	in := withSessionStart(
		`{"type":"message","id":"msg_01J9X0ZW1ZF7K8Q1V2T3M4N5P9","from":{"kind":"client","id":"client_alice"},"sessionId":"`+complianceSessionID+`","input":[{"type":"text","inline":"ping"}]}`,
		`{"type":"heartbeat","ts":1234,"sessionId":"`+complianceSessionID+`"}`,
	)
	stdout, _, code, err := driveAdapter(binary, in, 2, timeout)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("exit %d", code)
	}
	return judgeHeartbeatBusy(stdout, verbose)
}

// judgeHeartbeatBusy applies the row to the frames the binary wrote, in
// order.
func judgeHeartbeatBusy(stdout []string, verbose bool) (string, error) {
	var sawResponse, sawAck bool
	for _, line := range stdout {
		var f heartbeatBusyFrame
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			return "", fmt.Errorf("frame not JSON: %w (line: %s)", err, line)
		}
		switch f.Type {
		case "response":
			sawResponse = true
		case "heartbeat_ack":
			sawAck = true
			if sawResponse {
				continue
			}
			if f.Busy == nil || !*f.Busy || f.SessionID != complianceSessionID {
				return "", fmt.Errorf("heartbeat_ack %s was written while the message was outstanding, want busy: true and sessionId %q", line, complianceSessionID)
			}
		}
	}
	if !sawResponse || !sawAck {
		return "", errors.New("the binary did not write both the response and the heartbeat_ack")
	}
	if verbose {
		return fmt.Sprintf("frames=%q", stdout), nil
	}
	return "heartbeat_ack reports busy while the message is outstanding", nil
}
