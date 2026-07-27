// SPDX-License-Identifier: MIT

package inproc

import (
	"fmt"
	"os"
	"testing"
)

// TestMain stops the process-wide embedded PostgreSQL the harness
// starts on the first Env.Start. The instance is a child process that
// outlives the test binary unless it is stopped explicitly, so every
// binary that boots an inproc.Env owns this teardown.
func TestMain(m *testing.M) {
	code := m.Run()
	if err := ShutdownSharedPostgres(); err != nil {
		fmt.Fprintf(os.Stderr, "inproc: shutdown embedded postgres: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
