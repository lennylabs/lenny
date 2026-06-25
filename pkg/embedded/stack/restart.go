// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// restartedAtAnnotation is the pod-template annotation a rollout-restart
// stamps with the current time to trigger a new rollout of the Deployment,
// the same key `kubectl rollout restart` writes. Changing a pod-template
// annotation rolls the Deployment's pods without changing the desired spec.
const restartedAtAnnotation = "kubectl.kubernetes.io/restartedAt"

// RestartableComponents lists the §24.19 components `lenny restart` can
// cycle individually. The §17.4 control plane runs as in-cluster
// Deployments, so a restart is a Kubernetes rollout-restart of the named
// Deployment through the embedded kubeconfig; the pod-backed components are
// the gateway, controller, and ops Deployments.
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

// RunRestart implements the foreground `lenny restart <component>` command.
// §17.4: it restarts a single in-cluster component. The control plane runs as
// Deployments, so the restart is a Kubernetes rollout-restart of the named
// Deployment through the embedded kubeconfig the running stack recorded: it
// patches the Deployment's pod template with a restartedAt annotation, the
// same mechanism `kubectl rollout restart` uses, so the Deployment rolls a
// fresh ReplicaSet without changing its desired spec. The other components
// (k3s, the runtime pods) are cycled through `lenny down`/`lenny up`.
//
// spec: §24.19 line 264.
func RunRestart(ctx context.Context, opts RestartOptions) error {
	out := orDiscard(opts.Out)
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return err
	}
	if opts.Component == "" {
		return errors.New("a <component> argument is required")
	}
	deployment, ok := componentDeployment(opts.Component)
	if !ok {
		return errors.New("component cannot be restarted individually; restartable components are gateway, controller, and ops")
	}
	paths := NewPaths(root)
	st, ok, err := readRunningState(paths.StateFile())
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoRunningStack
	}
	if st.KubeconfigPath == "" {
		return fmt.Errorf("embedded restart: stack state at %s has no kubeconfigPath", paths.StateFile())
	}
	client, err := clusterClientFn(st.KubeconfigPath)
	if err != nil {
		return err
	}
	if err := rolloutRestartDeployment(ctx, client, controlPlaneNamespace, deployment); err != nil {
		return err
	}
	fmt.Fprintf(out, "lenny restart: rolled the %s Deployment (%s/%s)\n", opts.Component, controlPlaneNamespace, deployment)
	return nil
}

// rolloutRestartDeployment patches the named Deployment's pod template with a
// restartedAt annotation set to now, triggering a rollout of its pods. A
// strategic-merge patch on the pod-template annotation is the same change
// `kubectl rollout restart` makes, so the Deployment rolls a new ReplicaSet
// without altering its desired spec. The patch is idempotent in effect: each
// call sets a fresh timestamp, so a re-run rolls again rather than failing.
// spec: §24.19 line 264 (the restart is a Deployment rollout-restart).
func rolloutRestartDeployment(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	patch := fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{%q:%q}}}}}`,
		restartedAtAnnotation, time.Now().UTC().Format(time.RFC3339),
	)
	_, err := client.AppsV1().Deployments(namespace).Patch(
		ctx, name, types.StrategicMergePatchType, []byte(patch),
		metav1.PatchOptions{FieldManager: applyFieldManager},
	)
	if err != nil {
		return fmt.Errorf("embedded restart: roll Deployment %s/%s: %w", namespace, name, err)
	}
	return nil
}
