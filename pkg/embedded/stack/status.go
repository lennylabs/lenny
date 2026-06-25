// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/ctl"
	"github.com/lennylabs/lenny/pkg/embedded/k3s"
)

// ComponentStatus is the health of one Embedded Mode component.
type ComponentStatus struct {
	// Name identifies the component (gateway, controller, ops, k3s).
	Name string `json:"name"`
	// Healthy reports whether the component is up and responsive.
	Healthy bool `json:"healthy"`
	// Detail is a human-readable status note.
	Detail string `json:"detail"`
}

// Status is the §17.4 lenny status report: per-component health, the echo
// pool readiness, and the active session count.
type Status struct {
	// Running reports whether an Embedded Mode stack is recorded as
	// running.
	Running bool `json:"running"`
	// StartedAt is when the running stack came up.
	StartedAt time.Time `json:"startedAt,omitempty"`
	// Components carries the per-component health.
	Components []ComponentStatus `json:"components,omitempty"`
	// PoolReady reports whether the seeded echo warm pool has at least one
	// claimable ready idle pod. §17.4 distinguishes "gateway up" from "pool
	// ready" so the readiness is honest: lenny up returns when the gateway
	// answers and the echo pool warms in the background, so a still-warming
	// pool is reported separately from the gateway being up. spec: §17.4.
	PoolReady bool `json:"poolReady"`
	// ActiveSessions is the count of non-terminal sessions reported by
	// the gateway. It is -1 when the gateway did not report a count.
	ActiveSessions int `json:"activeSessions"`
}

// StatusOptions configures the lenny status command.
type StatusOptions struct {
	// Root is the Embedded Mode state directory.
	Root string
}

// CollectStatus probes a running Embedded Mode stack and returns its
// §17.4 / §24.19 status report. The §17.4 control plane runs as in-cluster
// pods, so the gateway, controller, and ops rows read their Deployment
// readiness through the embedded kubeconfig rather than a host process
// probe, and the echo pool readiness is reported separately so the report
// distinguishes "gateway up" from "pool ready" (lenny up returns when the
// gateway answers; the echo pool warms in the background). The k3s row keeps
// its substrate-handle probe (a Docker container probe on macOS/Windows, the
// recorded kubeconfig on Linux). The active session count is read from the
// gateway admin API through the host-side forwarder.
//
// spec: §17.4 line 178 (the control plane runs as in-cluster pods; status
// distinguishes gateway-up from pool-ready), §24.19 line 262.
func CollectStatus(ctx context.Context, opts StatusOptions) (Status, error) {
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return Status{}, err
	}
	paths := NewPaths(root)
	st, ok, err := readRunningState(paths.StateFile())
	if err != nil {
		return Status{}, err
	}
	if !ok {
		// No stack file, or the Stopped marker a non-`--purge` lenny down left
		// behind: the substrate handle and deployed tag persist on disk but the
		// stack is not running, so status reports it not running. spec: §17.4.
		return Status{Running: false, ActiveSessions: -1}, nil
	}

	out := Status{Running: true, StartedAt: st.StartedAt, ActiveSessions: -1}

	// The control-plane Deployment rows (gateway, controller, ops) read their
	// readiness from the embedded cluster. The k3s row keeps its substrate
	// handle probe. The cluster client is built once and shared across the
	// three Deployment reads. A kubeconfig the running state never recorded
	// (the substrate did not come up) leaves the Deployment rows down.
	clusterComponents := collectClusterComponents(ctx, st.KubeconfigPath)
	out.Components = append(out.Components, clusterComponents...)
	out.Components = append(out.Components, k3sComponentStatus(st))

	// The echo pool readiness is read separately from the gateway readiness so
	// the report distinguishes "gateway up" from "pool ready". spec: §17.4.
	if st.KubeconfigPath != "" {
		out.PoolReady = poolReadyFn(ctx, st.KubeconfigPath)
	}

	// Active session count from the gateway admin API through the forwarder.
	if gatewayHealthyRow(out.Components) && st.GatewayForwarderAddr != "" {
		if n, err := activeSessionCount(ctx, "https://"+st.GatewayForwarderAddr); err == nil {
			out.ActiveSessions = n
		}
	}
	return out, nil
}

// gatewayHealthyRow reports whether the gateway component row in components is
// healthy, so CollectStatus only queries the active-session count when the
// gateway Deployment is ready.
func gatewayHealthyRow(components []ComponentStatus) bool {
	for _, c := range components {
		if c.Name == "gateway" {
			return c.Healthy
		}
	}
	return false
}

// collectClusterComponents reads the gateway, controller, and ops Deployment
// readiness from the embedded cluster at kubeconfigPath and returns one
// ComponentStatus row per Deployment. A kubeconfig that is empty (the
// substrate did not come up) or cannot build a client yields all rows down
// with a connection-failure detail, so status reports an unreachable control
// plane rather than erroring. spec: §17.4 (the control-plane Deployments report
// readiness through the embedded kubeconfig).
func collectClusterComponents(ctx context.Context, kubeconfigPath string) []ComponentStatus {
	rows := make([]ComponentStatus, 0, 3)
	if kubeconfigPath == "" {
		return clusterUnreachableRows("no embedded kubeconfig recorded")
	}
	client, err := clusterClientFn(kubeconfigPath)
	if err != nil {
		return clusterUnreachableRows("embedded cluster unreachable")
	}
	for _, c := range []struct{ component, deployment string }{
		{"gateway", gatewayDeploymentName},
		{"controller", controllerDeploymentName},
		{"ops", opsDeploymentName},
	} {
		rows = append(rows, deploymentComponentStatus(ctx, client, c.component, c.deployment))
	}
	return rows
}

// clusterUnreachableRows returns the gateway/controller/ops rows all marked
// down with detail, for the path where the embedded cluster cannot be reached
// at all.
func clusterUnreachableRows(detail string) []ComponentStatus {
	return []ComponentStatus{
		{Name: "gateway", Healthy: false, Detail: detail},
		{Name: "controller", Healthy: false, Detail: detail},
		{Name: "ops", Healthy: false, Detail: detail},
	}
}

// deploymentComponentStatus reads the named Deployment in the control-plane
// namespace and builds its component row: healthy when the Deployment reports
// at least one ready replica with its status caught up to the latest spec
// generation (deploymentReady), down with a not-found detail when the
// Deployment is absent, and down with the error detail on any other read
// failure. spec: §17.4.
func deploymentComponentStatus(ctx context.Context, client kubernetes.Interface, component, deployment string) ComponentStatus {
	dep, err := client.AppsV1().Deployments(controlPlaneNamespace).Get(ctx, deployment, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ComponentStatus{Name: component, Healthy: false, Detail: fmt.Sprintf("Deployment %s not found", deployment)}
		}
		return ComponentStatus{Name: component, Healthy: false, Detail: fmt.Sprintf("read Deployment %s: %v", deployment, err)}
	}
	return ComponentStatus{
		Name:    component,
		Healthy: deploymentReady(dep),
		Detail:  deploymentDetail(dep),
	}
}

// deploymentDetail formats a Deployment's ready/desired replica counts into a
// status detail string.
func deploymentDetail(dep *appsv1.Deployment) string {
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	return fmt.Sprintf("Deployment %s (%d/%d ready)", dep.Name, dep.Status.ReadyReplicas, desired)
}

// containerRunning probes whether a Docker-backed k3s container is alive.
// It is a var so the status container-probe path is unit-testable without
// invoking a real docker.
var containerRunning = k3s.ContainerRunning

// k3sComponentStatus builds the k3s component health row from the recorded
// state. It probes by the substrate handle the launcher recorded: a docker
// container name on the Docker-backed launcher (macOS and Windows), where
// k3s runs inside the Docker VM, or the recorded kubeconfig presence on the
// Linux launcher. When neither is set the substrate did not come up (an
// unsupported host or a failed start), reported down.
//
// spec: §24.19 (the k3s health probe is a container probe on the
// Docker-backed substrate, a host probe on Linux).
func k3sComponentStatus(st State) ComponentStatus {
	switch {
	case st.K3sContainer != "":
		return ComponentStatus{
			Name:    "k3s",
			Healthy: containerRunning(st.K3sContainer),
			Detail:  containerDetail(st.K3sContainer),
		}
	case st.KubeconfigPath != "":
		return ComponentStatus{
			Name:    "k3s",
			Healthy: st.K3sEnabled,
			Detail:  fmt.Sprintf("kubeconfig %s", st.KubeconfigPath),
		}
	default:
		return ComponentStatus{
			Name: "k3s", Healthy: false, Detail: "not running (unsupported host or failed to start)",
		}
	}
}

// containerDetail formats a docker container handle into a status detail
// string, naming the container and its liveness so an operator can locate
// it with `docker logs` / `docker inspect`.
func containerDetail(name string) string {
	if containerRunning(name) {
		return fmt.Sprintf("container %s", name)
	}
	return fmt.Sprintf("container %s (not running)", name)
}

// WriteStatus renders a Status report as a human-readable table.
//
// spec: §17.4 line 178, §24.19 line 262.
func WriteStatus(w io.Writer, s Status) {
	if !s.Running {
		fmt.Fprintln(w, "lenny status: no embedded stack is running")
		fmt.Fprintln(w, "  run 'lenny up' to bring the embedded stack up")
		return
	}
	fmt.Fprintf(w, "lenny status: embedded stack running since %s\n\n", s.StartedAt.Format(time.RFC3339))
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "COMPONENT\tHEALTH\tDETAIL")
	for _, c := range s.Components {
		health := "down"
		if c.Healthy {
			health = "ok"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Name, health, c.Detail)
	}
	_ = tw.Flush()
	// The echo pool readiness is reported separately from the gateway row so
	// the readiness is honest: lenny up returns when the gateway answers and
	// the pool warms in the background. spec: §17.4.
	if s.PoolReady {
		fmt.Fprintf(w, "\necho pool: ready\n")
	} else {
		fmt.Fprintf(w, "\necho pool: warming (the first session may return a pool-warming response)\n")
	}
	if s.ActiveSessions >= 0 {
		fmt.Fprintf(w, "active sessions: %d\n", s.ActiveSessions)
	} else {
		fmt.Fprintf(w, "active sessions: unknown (gateway unreachable)\n")
	}
}

// WriteStatusJSON renders a Status report as indented JSON.
func WriteStatusJSON(w io.Writer, s Status) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(s)
}

// activeSessionCount queries the gateway admin API for the count of
// non-terminal sessions. The dev-header auth path admits the request.
func activeSessionCount(ctx context.Context, gatewayURL string) (int, error) {
	client := ctl.New(ctl.Options{
		BaseURL:   gatewayURL,
		DevTenant: defaultTenant,
		DevRoles:  "platform-admin",
		Timeout:   10 * time.Second,
	})
	var report map[string]any
	if err := client.Do(ctx, "GET", "/v1/admin/health", nil, &report); err != nil {
		return 0, err
	}
	// The §15.1 health report carries an activeSessions count when the
	// gateway publishes one. Probe the common field names; absence yields
	// zero rather than an error.
	for _, key := range []string{"activeSessions", "active_sessions"} {
		if v, ok := report[key]; ok {
			if n, ok := v.(float64); ok {
				return int(n), nil
			}
		}
	}
	if sessions, ok := report["sessions"].(map[string]any); ok {
		if v, ok := sessions["active"].(float64); ok {
			return int(v), nil
		}
	}
	return 0, nil
}
