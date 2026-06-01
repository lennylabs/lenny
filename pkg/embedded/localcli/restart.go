// SPDX-License-Identifier: MIT

package localcli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/lennylabs/lenny/pkg/embedded/stack"
)

// cmdRestart implements `lenny restart <component>` (§24.19 line 264):
// it restarts a single embedded component without tearing down the rest
// of the stack. v1 restarts the gateway or the controller, the two
// supervised child processes that can cycle independently. The
// in-process components (Postgres, Redis, the OIDC provider, the TLS
// proxy) and the embedded k3s node share the supervisor lifecycle and
// are cycled with `lenny down` followed by `lenny up`.
func cmdRestart(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	component := ""
	for _, a := range args {
		if a != "" && a[0] != '-' && component == "" {
			component = a
		}
	}
	if component == "" {
		fmt.Fprintf(stderr, "lenny restart: a <component> argument is required (one of: %v)\n", stack.RestartableComponents())
		return 2
	}
	if !stack.Restartable(component) {
		fmt.Fprintf(stderr, "lenny restart: %q cannot be restarted individually; restartable components are %v\n",
			component, stack.RestartableComponents())
		return 2
	}
	err := stack.RunRestart(ctx, stack.RestartOptions{Component: component, Out: stdout})
	if err != nil {
		if errors.Is(err, stack.ErrNoRunningStack) {
			fmt.Fprintln(stderr, "lenny restart: no embedded stack is running; run 'lenny up' first")
		} else {
			fmt.Fprintf(stderr, "lenny restart: %v\n", err)
		}
		return 1
	}
	return 0
}
