// SPDX-License-Identifier: MIT

// Conformance fixture: receives heartbeat messages from the gateway
// but never replies with heartbeat_ack. The §11 conformance harness
// expects ADAPTER_HEARTBEAT_TIMEOUT after the 10-second budget.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type message struct {
	Type string `json:"type"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1<<16), 4<<20)
	for scanner.Scan() {
		var m message
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			continue
		}
		// Echo every message kind EXCEPT heartbeat — that is the
		// deliberate violation. The conformance harness will time
		// out waiting for a heartbeat_ack.
		switch m.Type {
		case "heartbeat":
			// Sleep past the §11 10s deadline.
			time.Sleep(20 * time.Second)
		case "shutdown":
			fmt.Fprintln(os.Stdout, `{"type":"shutdown_ack"}`)
			return
		}
	}
}
