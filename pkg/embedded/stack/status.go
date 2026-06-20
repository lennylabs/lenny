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
	// Name identifies the component (gateway, controller, postgres,
	// redis, k3s).
	Name string `json:"name"`
	// Healthy reports whether the component is up and responsive.
	Healthy bool `json:"healthy"`
	// Detail is a human-readable status note.
	Detail string `json:"detail"`
	// Resource carries the component's CPU% and resident memory.
	//
	// spec: §24.19 line 262 "resource usage".
	Resource ResourceUsage `json:"resource"`
}

// ResourceUsage is a per-component CPU% / RSS sample. CPUPercent is
// the share of one CPU (so >100% on multi-threaded processes). RSSBytes
// is the OS-reported resident set size of the process.
//
// Sampled is false when the OS probe could not produce a sample (the
// process is gone, ps is unavailable, or the platform is not
// supported); JSON callers can distinguish that from a real zero.
//
// spec: §24.19 line 262 "resource usage".
type ResourceUsage struct {
	Sampled    bool    `json:"sampled"`
	CPUPercent float64 `json:"cpuPercent"`
	RSSBytes   int64   `json:"rssBytes"`
}

// Status is the §17.4 lenny status report: per-component health and
// the active session count.
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
// §17.4 / §24.19 status report (per-component health, active session
// count, and resource usage).
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

	// Sample resource usage for every PID we know about; one ps
	// invocation per stack rather than per component.
	pids := []int{st.SupervisorPID, st.GatewayPID, st.ControllerPID, st.K3sPID}
	usage := sampleResourceUsage(pids)

	// Supervisor.
	out.Components = append(out.Components, ComponentStatus{
		Name:     "supervisor",
		Healthy:  processAlive(st.SupervisorPID),
		Detail:   pidDetail(st.SupervisorPID),
		Resource: usage[st.SupervisorPID],
	})

	// Gateway: probe its liveness endpoint.
	gwHealthy := processAlive(st.GatewayPID) && gatewayHealthy(ctx, "http://"+st.HTTPAddr)
	out.Components = append(out.Components, ComponentStatus{
		Name:     "gateway",
		Healthy:  gwHealthy,
		Detail:   fmt.Sprintf("https://localhost%s (%s)", portSuffix(st.HTTPSAddr), pidDetail(st.GatewayPID)),
		Resource: usage[st.GatewayPID],
	})

	// Controller.
	if st.ControllerPID != 0 {
		out.Components = append(out.Components, ComponentStatus{
			Name:     "controller",
			Healthy:  processAlive(st.ControllerPID),
			Detail:   pidDetail(st.ControllerPID),
			Resource: usage[st.ControllerPID],
		})
	} else if st.K3sEnabled {
		out.Components = append(out.Components, ComponentStatus{
			Name: "controller", Healthy: false, Detail: "not started",
		})
	}

	// Embedded Kubernetes. The Linux launcher runs k3s as a host process,
	// so its liveness is a PID probe and the host ps samples its
	// resources. The Docker-backed launcher runs k3s inside the Docker VM
	// with no host PID, so its liveness is a container probe by the
	// recorded container handle and the host ps cannot sample it.
	// spec: §24.19 (the k3s health/resource probe is a container probe on
	// the Docker-backed substrate).
	out.Components = append(out.Components, k3sComponentStatus(st, usage))

	// Stores: a probe against the gateway covers them indirectly; a
	// healthy gateway is configured against the embedded Postgres and
	// Redis. Report them from the gateway-health signal.
	out.Components = append(
		out.Components,
		ComponentStatus{Name: "postgres", Healthy: gwHealthy, Detail: "embedded PostgreSQL 16 bundle"},
		ComponentStatus{Name: "redis", Healthy: gwHealthy, Detail: "embedded in-process Redis"},
	)

	// Active session count from the gateway admin API.
	if gwHealthy {
		if n, err := activeSessionCount(ctx, "http://"+st.HTTPAddr); err == nil {
			out.ActiveSessions = n
		}
	}
	return out, nil
}

// containerRunning probes whether a Docker-backed k3s container is alive.
// It is a var so the status container-probe path is unit-testable without
// invoking a real docker, matching the resourceSampler injection point.
var containerRunning = k3s.ContainerRunning

// k3sComponentStatus builds the k3s component health row from the
// recorded state. It probes by the substrate handle the launcher
// recorded: a host PID on the Linux managed-child-process launcher, or a
// docker container name on the Docker-backed launcher (macOS and
// Windows), where k3s runs inside the Docker VM with no host PID. When
// neither handle is set the substrate did not come up (an unsupported
// host or a failed start), reported down.
//
// spec: §24.19 (the k3s health/resource probe is a container probe on the
// Docker-backed substrate, a host-PID probe on Linux).
func k3sComponentStatus(st State, usage map[int]ResourceUsage) ComponentStatus {
	switch {
	case st.K3sContainer != "":
		// Docker-backed substrate: probe the container by name. The host
		// ps cannot sample a process inside the Docker VM, so the resource
		// sample is left unsampled.
		return ComponentStatus{
			Name:    "k3s",
			Healthy: containerRunning(st.K3sContainer),
			Detail:  containerDetail(st.K3sContainer),
		}
	case st.K3sPID != 0:
		// Linux managed-child-process launcher: probe by host PID and
		// sample its resources from the host ps.
		return ComponentStatus{
			Name:     "k3s",
			Healthy:  processAlive(st.K3sPID),
			Detail:   pidDetail(st.K3sPID),
			Resource: usage[st.K3sPID],
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
	fmt.Fprintln(tw, "COMPONENT\tHEALTH\tCPU%\tRSS\tDETAIL")
	for _, c := range s.Components {
		health := "down"
		if c.Healthy {
			health = "ok"
		}
		cpu, rss := formatResource(c.Resource)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", c.Name, health, cpu, rss, c.Detail)
	}
	_ = tw.Flush()
	if s.ActiveSessions >= 0 {
		fmt.Fprintf(w, "\nactive sessions: %d\n", s.ActiveSessions)
	} else {
		fmt.Fprintf(w, "\nactive sessions: unknown (gateway unreachable)\n")
	}
}

// formatResource renders a ResourceUsage sample as two display cells
// ("CPU%", "RSS"). An unsampled component renders as "—".
func formatResource(r ResourceUsage) (string, string) {
	if !r.Sampled {
		return "—", "—"
	}
	return fmt.Sprintf("%.1f", r.CPUPercent), humanizeBytes(r.RSSBytes)
}

// humanizeBytes formats a byte count as a short human-readable string
// (e.g., "12.4 MiB"). It rounds to one decimal place.
func humanizeBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	suffixes := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	if exp >= len(suffixes) {
		exp = len(suffixes) - 1
	}
	return fmt.Sprintf("%.1f %s", float64(b)/float64(div), suffixes[exp])
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
	// gateway publishes one. Probe the common field names; absence
	// yields zero rather than an error.
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

// pidDetail formats a PID into a status detail string.
func pidDetail(pid int) string {
	if pid <= 0 {
		return "no pid"
	}
	if processAlive(pid) {
		return fmt.Sprintf("pid %d", pid)
	}
	return fmt.Sprintf("pid %d (not running)", pid)
}
