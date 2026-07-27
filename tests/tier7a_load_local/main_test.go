// SPDX-License-Identifier: MIT

//go:build load_local

package tier7a_load_local_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/inproc"
)

// TestMain stops the embedded PostgreSQL the multi-component scenarios
// share. TESTING.md §12.7.a has the in-process harness boot an embedded
// Postgres adapter; the harness starts one PostgreSQL child process per
// test binary, lazily, and that process outlives the binary unless it
// is stopped here.
func TestMain(m *testing.M) {
	code := m.Run()
	if err := inproc.ShutdownSharedPostgres(); err != nil {
		fmt.Fprintf(os.Stderr, "tier7a: shutdown embedded postgres: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
