// SPDX-License-Identifier: MIT

// Conformance fixture: spawns a goroutine that holds stdin open
// indefinitely without responding to handshake messages. Per §11
// the conformance harness expects ADAPTER_HANDSHAKE_TIMEOUT.
package main

import (
	"io"
	"os"
	"time"
)

func main() {
	// Drain stdin into the void but never reply. This produces the
	// pathological "the adapter is alive but unresponsive" shape.
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}()
	// Hold the process alive past the handshake budget.
	time.Sleep(2 * time.Minute)
}
