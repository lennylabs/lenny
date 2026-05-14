// SPDX-License-Identifier: MIT

// Conformance fixture: emits a JSON Lines record with a trailing
// comma — not valid JSON. The §11 conformance harness expects
// ADAPTER_PROTOCOL_VIOLATION.
package main

import (
	"fmt"
	"os"
)

func main() {
	// Send a malformed message immediately on startup. The trailing
	// comma after "1" is the deliberate violation.
	fmt.Fprintln(os.Stdout, `{"type":"ready","schema_version":1,}`)
	// Keep the process alive long enough for the harness to read
	// the violation, then exit.
	var hold chan struct{}
	<-hold
}
