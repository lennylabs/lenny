// SPDX-License-Identifier: MIT

package superviseprobe

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
)

// Exit codes of Main. spec: §15.4 (runtime exit codes).
const (
	exitOK            = 0
	exitRuntimeError  = 1
	exitProtocolError = 2
)

// Main runs the probe with args (os.Args without the program name) and
// returns the process exit code. No argument serves sessions; otherwise the
// first argument names a subcommand.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return serve(stderr)
	}
	switch args[0] {
	case SubAttack:
		return printJSON(stdout, stderr, runAttack(args[1:]))
	case SubCleanupRecord:
		return reportCleanup(stderr)
	case SubProcesses:
		procs, err := listProcesses()
		if err != nil {
			fmt.Fprintf(stderr, "supervise-probe: %v\n", err)
			return exitRuntimeError
		}
		return printJSON(stdout, stderr, procs)
	case SubKillRuntime:
		return printJSON(stdout, stderr, killRuntime())
	case SubList:
		return printJSON(stdout, stderr, listDir(args[1:]))
	case SubSupervisePeer:
		return supervisePeer(args[1:], stdout, stderr)
	case SubSetupResidue:
		return printSetupResidue(stdout, stderr, args[1:], createSetupResidue)
	case SubSetupResidueCheck:
		return printSetupResidue(stdout, stderr, args[1:], readSetupResidue)
	case subDaemonChild:
		return daemonChild(stdout, stderr)
	case subDaemonGrandchild:
		daemonGrandchild()
		return exitOK
	default:
		fmt.Fprintf(stderr, "supervise-probe: unknown subcommand %q\n", args[0])
		return exitProtocolError
	}
}

// printJSON writes v as one JSON line to stdout.
func printJSON(stdout, stderr io.Writer, v any) int {
	if err := json.NewEncoder(stdout).Encode(v); err != nil {
		fmt.Fprintf(stderr, "supervise-probe: encode result: %v\n", err)
		return exitRuntimeError
	}
	return exitOK
}

// listDir lists the directory args[0] names. A read failure is reported
// as an AttackResult carrying the errno, so a caller can tell an empty
// directory from an unreadable one.
func listDir(args []string) any {
	if len(args) == 0 {
		return AttackResult{Attack: SubList, Detail: "no directory named"}
	}
	entries, err := os.ReadDir(args[0])
	if err != nil {
		return AttackResult{Attack: SubList, Errno: errnoName(err), Detail: args[0]}
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// selfExe is the probe binary's path, which the daemon double fork re-execs.
func selfExe() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return os.Args[0]
}

// printSetupResidue parses the System V key args[0] names, runs op with it,
// and prints the result. A result carrying an error exits non-zero, so a
// setup command that could not leave its residue fails the session's setup.
func printSetupResidue(stdout, stderr io.Writer, args []string, op func(int) SetupResidue) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "supervise-probe: no System V key named")
		return exitProtocolError
	}
	key, err := strconv.Atoi(args[0])
	if err != nil || key <= 0 {
		fmt.Fprintf(stderr, "supervise-probe: System V key %q is not a positive integer\n", args[0])
		return exitProtocolError
	}
	r := op(key)
	if code := printJSON(stdout, stderr, r); code != exitOK {
		return code
	}
	if r.Error != "" {
		fmt.Fprintf(stderr, "supervise-probe: %s\n", r.Error)
		return exitRuntimeError
	}
	return exitOK
}
