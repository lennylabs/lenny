// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"errors"
	"io"
)

// RestartableComponents lists the §24.19 components `lenny restart` can
// cycle individually. The §17.4 control plane runs as in-cluster
// Deployments, so a restart is a Kubernetes rollout-restart of the named
// Deployment through the embedded kubeconfig (proposal 0017 C5); the
// pod-backed components are the gateway, controller, and ops Deployments.
func RestartableComponents() []string {
	return []string{"gateway", "controller", "ops"}
}

// Restartable reports whether name is a §24.19 individually-restartable
// component.
func Restartable(name string) bool {
	for _, c := range RestartableComponents() {
		if c == name {
			return true
		}
	}
	return false
}

// RestartOptions configures the `lenny restart` command.
type RestartOptions struct {
	// Root is the Embedded Mode state directory.
	Root string
	// Component is the component to restart (see RestartableComponents).
	Component string
	// Out receives progress output.
	Out io.Writer
}

// errRestartNotWired marks the Kubernetes rollout-restart that replaces the
// removed host-process supervisor IPC. C2 removes the host-process supervisor
// restart path; the Deployment rollout-restart against the embedded
// kubeconfig lands in the next build step (proposal 0017 C5).
var errRestartNotWired = errors.New("embedded: the in-cluster rollout-restart is not yet wired (proposal 0017 C5)")

// RunRestart implements the foreground `lenny restart <component>` command.
// §17.4: it restarts a single in-cluster component. The control plane runs
// as Deployments, so the restart is a Kubernetes rollout-restart of the
// named Deployment through the embedded kubeconfig; that path lands in the
// next build step (proposal 0017 C5).
//
// spec: §24.19 line 264.
func RunRestart(ctx context.Context, opts RestartOptions) error {
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return err
	}
	if opts.Component == "" {
		return errors.New("a <component> argument is required")
	}
	if !Restartable(opts.Component) {
		return errors.New("component cannot be restarted individually; restartable components are gateway, controller, and ops")
	}
	paths := NewPaths(root)
	if _, ok, err := readRunningState(paths.StateFile()); err != nil {
		return err
	} else if !ok {
		return ErrNoRunningStack
	}
	_ = ctx
	return errRestartNotWired
}
