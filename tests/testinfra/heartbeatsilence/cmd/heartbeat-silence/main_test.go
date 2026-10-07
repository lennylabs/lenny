// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"os/exec"
	"testing"
)

// spec: 15.4 (Runtime Adapter Specification)
// The fixture exits with the child's own exit code, 0 for a clean child, and
// the runtime-error code when the child could not be run at all.
func TestExitCodeIsTheChilds_spec_15_4(t *testing.T) {
	if got := exitCode(nil); got != 0 {
		t.Errorf("exitCode(nil) = %d, want 0", got)
	}
	err := exec.Command("sh", "-c", "exit 2").Run()
	if got := exitCode(err); got != 2 {
		t.Errorf("exitCode(child exit 2) = %d, want 2", got)
	}
	if got := exitCode(errors.New("start failed")); got != exitRuntimeError {
		t.Errorf("exitCode(start error) = %d, want %d", got, exitRuntimeError)
	}
}
