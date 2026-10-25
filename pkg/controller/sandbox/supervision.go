// SPDX-License-Identifier: MIT

package sandbox

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// supervision is the part of a pod's inputs that the runtime supervisor and
// the pod's runtime-process lifetime fix at creation.
type supervision struct {
	runtimeProcess string
	command        []string
	env            []podspec.EnvPair
	launchMode     string
}

// errAgentSidecarWithoutCommand reports a sidecar `type: agent` Runtime that
// carries no `command`, which the Runtime CRD's validation requires.
var errAgentSidecarWithoutCommand = errors.New("a type: agent sidecar runtime carries no command")

// errRestartNotRestartable reports a `restart` pool whose runtime is not
// restartable.
var errRestartNotRestartable = errors.New("recycle.runtimeProcess restart on a runtime that is not restartable")

// resolveSupervision resolves the pod's lifetime and, on a supervised pod,
// the supervisor inputs. It refuses, creating no pod:
//
//   - a sidecar `type: agent` Runtime without `command`, which the Runtime
//     CRD requires and the supervisor needs to launch anything;
//   - a `restart` pod whose Runtime is not restartable as §5.1 `command`
//     defines it, through the same helper the RuntimeReconciler mirrors;
//   - a failed pool or template read, so a transient error never creates
//     a pod whose lifetime or launch mode differs from the pool's.
//
// spec: §5.2 (Runtime-process lifetime); §5.1 (command); §4.7.10 (Runtime
// supervisor).
func (r *Reconciler) resolveSupervision(ctx context.Context, sb *lennyv1.Sandbox, rt *lennyv1.Runtime) (supervision, error) {
	process, err := r.poolRuntimeProcess(ctx, sb)
	if err != nil {
		return supervision{}, err
	}
	s := supervision{runtimeProcess: process}
	if state.RuntimeProcessLifetime(process) == state.RuntimeProcessRestart && !rt.Spec.Restartable() {
		return supervision{}, fmt.Errorf("runtime %s: %w", rt.Name, errRestartNotRestartable)
	}
	if !runsSupervisor(rt) {
		return s, nil
	}
	if len(rt.Spec.Command) == 0 {
		return supervision{}, fmt.Errorf("runtime %s: %w", rt.Name, errAgentSidecarWithoutCommand)
	}
	mode, err := r.launchMode(ctx, sb, rt)
	if err != nil {
		return supervision{}, err
	}
	s.command = append([]string(nil), rt.Spec.Command...)
	s.launchMode = mode
	for _, e := range rt.Spec.Env {
		s.env = append(s.env, podspec.EnvPair{Name: e.Name, Value: e.Value})
	}
	return s, nil
}

// runsSupervisor reports whether pods of rt run the runtime supervisor: every
// sidecar pod of a `type: agent` runtime does, and no other pod does. An
// empty deploymentModel defaults to sidecar. spec: §4.7.10 (Runtime
// supervisor).
func runsSupervisor(rt *lennyv1.Runtime) bool {
	return rt.Spec.Type == "agent" && rt.Spec.DeploymentModel != string(podspec.DeploymentEmbedded)
}

// launchMode fixes the supervised pod's launch mode at creation from the
// Sandbox's execution mode (the pool's), the Runtime's preConnect
// capability, and one read of the pool's SDK-warm circuit breaker, which is
// the only read that fixes the mode. On a preConnect runtime a failed pool
// read creates no pod, so a breaker that might be open never renders a
// warm-up launch. spec: §4.7.10 (Runtime supervisor, Launch; Runtime
// supervisor, SDK-warm); §6.1 (Circuit-breaker for SDK-warm pools).
func (r *Reconciler) launchMode(ctx context.Context, sb *lennyv1.Sandbox, rt *lennyv1.Runtime) (string, error) {
	if sb.Spec.ExecutionMode == "service" {
		return podspec.LaunchService, nil
	}
	if rt.Spec.Capabilities == nil || !rt.Spec.Capabilities.PreConnect {
		return podspec.LaunchSession, nil
	}
	disabled, err := r.sdkWarmDisabled(ctx, sb)
	if err != nil {
		return "", err
	}
	if disabled {
		return podspec.LaunchPreconnectPodWarm, nil
	}
	return podspec.LaunchPreconnectSDKWarm, nil
}

// sdkWarmDisabled reads the pool's spec.sdkWarmDisabled breaker. A Sandbox
// without a pool has no breaker, which reads as closed. Any read error,
// NotFound included, is returned, so the caller fails closed.
func (r *Reconciler) sdkWarmDisabled(ctx context.Context, sb *lennyv1.Sandbox) (bool, error) {
	if sb.Spec.PoolRef == "" {
		return false, nil
	}
	var pool lennyv1.SandboxWarmPool
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: sb.Namespace, Name: sb.Spec.PoolRef}, &pool); err != nil {
		return false, fmt.Errorf("read pool %s for its SDK-warm breaker: %w", sb.Spec.PoolRef, err)
	}
	return pool.Spec.SDKWarmDisabled, nil
}

// poolRuntimeProcess reads the pool's recycle.runtimeProcess from the
// SandboxTemplate the PoolScalingController mirrors it into. A Sandbox
// without a pool, a pool or template that no longer exists, and a template
// without the field read as empty, which creates a `keep` pod. Any other
// read error is returned, so a transient failure creates no pod rather than
// a pod whose immutable lifetime differs from the pool's. spec: §5.2
// (Runtime-process lifetime).
func (r *Reconciler) poolRuntimeProcess(ctx context.Context, sb *lennyv1.Sandbox) (string, error) {
	if sb.Spec.PoolRef == "" {
		return "", nil
	}
	var pool lennyv1.SandboxWarmPool
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: sb.Namespace, Name: sb.Spec.PoolRef}, &pool); err != nil {
		return "", ignoreNotFound(err, "pool", sb.Spec.PoolRef)
	}
	if pool.Spec.TemplateRef == "" {
		return "", nil
	}
	var tmpl lennyv1.SandboxTemplate
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: sb.Namespace, Name: pool.Spec.TemplateRef}, &tmpl); err != nil {
		return "", ignoreNotFound(err, "template", pool.Spec.TemplateRef)
	}
	if sp := tmpl.Spec.SessionPolicy; sp != nil && sp.Recycle != nil {
		return sp.Recycle.RuntimeProcess, nil
	}
	return "", nil
}

// ignoreNotFound returns nil for a NotFound error and wraps every other.
func ignoreNotFound(err error, kind, name string) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return fmt.Errorf("read %s %s for its runtime-process lifetime: %w", kind, name, err)
}
