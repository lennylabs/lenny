// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

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

// Status is the §17.4 lenny status report: per-component health and the
// active session count.
type Status struct {
	// Running reports whether an Embedded Mode stack is recorded as
	// running.
	Running bool `json:"running"`
	// StartedAt is when the running stack came up.
	StartedAt time.Time `json:"startedAt,omitempty"`
	// Components carries the per-component health.
	Components []ComponentStatus `json:"components,omitempty"`
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
// pods, so the cluster-backed status (substrate, gateway/controller
// Deployment readiness, and echo pool readiness, distinguishing "gateway
// up" from "pool ready") lands in the next build step (proposal 0017 C5).
// Here CollectStatus reports the substrate handle and the gateway forwarder
// reachability so the command keeps working through the host-process
// removal.
//
// spec: §17.4 line 178, §24.19 line 262.
func CollectStatus(ctx context.Context, opts StatusOptions) (Status, error) {
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return Status{}, err
	}
	paths := NewPaths(root)
	st, ok, err := readState(paths.StateFile())
	if err != nil {
		return Status{}, err
	}
	if !ok {
		return Status{Running: false, ActiveSessions: -1}, nil
	}

	out := Status{Running: true, StartedAt: st.StartedAt, ActiveSessions: -1}

	// Gateway: probe the host-side forwarder it answers behind.
	gwHealthy := st.GatewayForwarderAddr != "" && gatewayHealthy(ctx, "https://"+st.GatewayForwarderAddr)
	out.Components = append(out.Components, ComponentStatus{
		Name:    "gateway",
		Healthy: gwHealthy,
		Detail:  gatewayDetail(st),
	})

	// Embedded Kubernetes substrate. The Docker-backed launcher records a
	// container handle and the status probes it by name; the Linux launcher
	// runs k3s as a host process under the recorded kubeconfig.
	out.Components = append(out.Components, k3sComponentStatus(st))

	// Active session count from the gateway admin API.
	if gwHealthy {
		if n, err := activeSessionCount(ctx, "https://"+st.GatewayForwarderAddr); err == nil {
			out.ActiveSessions = n
		}
	}
	return out, nil
}

// gatewayDetail formats the gateway status detail from the recorded
// host-side forwarder address.
func gatewayDetail(st State) string {
	if st.GatewayForwarderAddr == "" {
		return "no gateway forwarder recorded"
	}
	return fmt.Sprintf("https://%s", st.GatewayForwarderAddr)
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
	if s.ActiveSessions >= 0 {
		fmt.Fprintf(w, "\nactive sessions: %d\n", s.ActiveSessions)
	} else {
		fmt.Fprintf(w, "\nactive sessions: unknown (gateway unreachable)\n")
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
