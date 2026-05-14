// SPDX-License-Identifier: MIT

// Conformance fixture: receives `shutdown` but takes longer than
// the documented deadline (10s) to exit. Per §11 the conformance
// harness expects ADAPTER_SHUTDOWN_TIMEOUT.
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
	for scanner.Scan() {
		var m message
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			continue
		}
		switch m.Type {
		case "heartbeat":
			fmt.Fprintln(os.Stdout, `{"type":"heartbeat_ack"}`)
		case "shutdown":
			// Stall well past the 10s budget.
			time.Sleep(30 * time.Second)
			fmt.Fprintln(os.Stdout, `{"type":"shutdown_ack"}`)
			return
		}
	}
}
