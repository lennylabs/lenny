// SPDX-License-Identifier: MIT

// Command supervise-probe is the test-only Standard-level probe runtime the
// cluster tiers run under the runtime supervisor. Its behavior lives in
// tests/testinfra/superviseprobe, whose package comment describes the
// session commands and the subcommands a test runs with `kubectl exec`.
//
// The image (Dockerfile in this directory) sets PROBE_IMAGE_ENV with
// ENV, which no Runtime `env` sets, and STOPSIGNAL SIGINT, so a test can
// assert that the supervisor's launch environment drops the image ENV and
// that pod deletion delivers no SIGINT to the runtime process.
//
// spec: §4.7.10 (Runtime supervisor); §4.7.11 item 8.
package main

import (
	"os"

	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

func main() {
	os.Exit(superviseprobe.Main(os.Args[1:], os.Stdout, os.Stderr))
}
