// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/executor"
)

// resolveExecutor selects the gateway's in-process session executor
// from the §17.4 local-dev runtime selectors. Precedence:
//
//  1. agentRuntime == "echo" (LENNY_AGENT_RUNTIME=echo) forces the
//     built-in echo executor — the §17.4 line 262 zero-credential mode,
//     selectable explicitly in Compose Mode and the default in Source
//     Mode.
//  2. a non-empty runtimeBin (--runtime-bin / LENNY_AGENT_BINARY)
//     dispatches each message to a child process speaking the §15.4.1
//     adapter protocol — the §17.4 line 323 custom-runtime override.
//  3. otherwise the built-in echo executor, the Source Mode default.
//
// An explicit LENNY_AGENT_RUNTIME=echo wins over a runtime binary: the
// operator asked for zero-credential mode. The only built-in runtime
// name this selector defines is "echo"; any other non-empty value is
// rejected so a typo fails closed at startup rather than silently
// falling back to echo.
//
// spec: §17.4 line 262 (zero-credential echo default / explicit
// LENNY_AGENT_RUNTIME=echo selection), line 323 (LENNY_AGENT_BINARY
// custom-runtime override).
func resolveExecutor(runtimeBin, agentRuntime string) (executor.Executor, string, error) {
	switch {
	case strings.EqualFold(agentRuntime, "echo"):
		return executor.NewEchoExecutor(), "in-process echo runtime (LENNY_AGENT_RUNTIME=echo)", nil
	case agentRuntime != "":
		return nil, "", fmt.Errorf(
			"unknown LENNY_AGENT_RUNTIME %q: the only built-in dev runtime is %q; "+
				"register other runtimes via the admin API or point at a binary with LENNY_AGENT_BINARY",
			agentRuntime, "echo",
		)
	case runtimeBin != "":
		return executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: runtimeBin}),
			fmt.Sprintf("runtime binary %s", runtimeBin), nil
	default:
		return executor.NewEchoExecutor(), "in-process echo runtime (default)", nil
	}
}
