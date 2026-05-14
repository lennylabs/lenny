// SPDX-License-Identifier: MIT

// Conformance fixture: emits a top-level message type that is not in
// the documented enum. Per §11, unknown types must be ignored by the
// gateway, not rejected. The conformance harness asserts no error.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type message struct {
	Type string `json:"type"`
}

func main() {
	// Emit a deliberately-unknown message type at startup.
	fmt.Fprintln(os.Stdout, `{"type":"lenny-unknown-fixture-v0"}`)
	// Then handle protocol messages normally.
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var m message
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			continue
		}
		switch m.Type {
		case "heartbeat":
			fmt.Fprintln(os.Stdout, `{"type":"heartbeat_ack"}`)
		case "shutdown":
			fmt.Fprintln(os.Stdout, `{"type":"shutdown_ack"}`)
			return
		}
	}
}
