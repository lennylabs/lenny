// SPDX-License-Identifier: MIT

package localcli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/lennylabs/lenny/pkg/embedded/stack"
)

// cmdRestart implements `lenny restart <component>` (§24.19 line 264): it
// rolls a single in-cluster control-plane Deployment without tearing down the
// rest of the stack. The §17.4 control plane runs as in-cluster pods, so the
// restartable components are the gateway, controller, and ops Deployments, and
// the restart is a Kubernetes rollout-restart through the embedded kubeconfig.
// The embedded k3s substrate and the agent (runtime) pods are not individually
// restartable here; they cycle with `lenny down` followed by `lenny up`.
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
