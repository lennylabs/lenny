// SPDX-License-Identifier: MIT

//go:build !linux

package superviseprobe

import (
	"errors"
	"io"
)

// errUnsupported is every Linux-only operation's result off Linux. The probe
// runs only in a Linux container; these stubs keep the package building on a
// developer's host.
var errUnsupported = errors.New("supervise-probe requires Linux")

func startDaemon() ActionResult         { return ActionResult{Error: errUnsupported.Error()} }
func createIPC() ActionResult           { return ActionResult{Error: errUnsupported.Error()} }
func checkResidue(string) ResidueResult { return ResidueResult{} }
func listProcesses() ([]Process, error) { return nil, errUnsupported }
func killRuntime() AttackResult         { return AttackResult{Detail: errUnsupported.Error()} }
func runAttack([]string) AttackResult   { return AttackResult{Detail: errUnsupported.Error()} }
func daemonChild(_, stderr io.Writer) int {
	_, _ = io.WriteString(stderr, errUnsupported.Error()+"\n")
	return exitRuntimeError
}
func daemonGrandchild() {}
func createSetupResidue(key int) SetupResidue {
	return SetupResidue{Key: key, Error: errUnsupported.Error()}
}
func readSetupResidue(key int) SetupResidue {
	return SetupResidue{Key: key, Error: errUnsupported.Error()}
}
