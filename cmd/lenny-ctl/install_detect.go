// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
)

// install_detect.go implements the §17.6 install-wizard detection phase
// (spec lines 671-697). Before the wizard asks any question, it probes the
// target cluster for the capabilities the spec enumerates: cert-manager
// CRDs and at least one Ready ClusterIssuer, Prometheus Operator CRDs
// (ServiceMonitor, PrometheusRule), available RuntimeClass objects (gVisor,
// Kata), the NetworkPolicy-supporting CNI surface, and the Kubernetes
// version. The findings are presented as a summary and feed detection-driven
// defaults so the question phase can skip a question whose answer is
// unambiguous (§17.6 line 689). The datastore-reachability half of the
// detection (Postgres/Redis/MinIO) is the wizard's separate preflight phase
// (runInstallPreflight), which reuses the lenny-preflight probes. F-17.6.9.

// clusterDetection holds the §17.6 detection-phase findings. A zero value
// (every field empty/false) represents "nothing detected", which the
// summary renders honestly so the operator sees that detection produced no
// signal rather than a false all-clear.
type clusterDetection struct {
	// skipped is true when --offline suppressed detection entirely.
	skipped bool
	// available is false when the cluster could not be reached at all (no
	// kubectl, no kubeconfig, unreachable API server). The wizard then
	// falls back to the static defaults without aborting.
	available bool
	// kubernetesVersion is the API server gitVersion (e.g. "v1.29.4").
	kubernetesVersion string
	// runtimeClasses are the RuntimeClass object names present (gVisor,
	// Kata, or others). §5.3 sandboxing needs one of these.
	runtimeClasses []string
	// readyClusterIssuers are the cert-manager ClusterIssuer names whose
	// Ready condition is True. The TLS-strategy default keys off this.
	readyClusterIssuers []string
	// certManagerInstalled is true when the ClusterIssuer API is served.
	certManagerInstalled bool
	// prometheusOperator is true when the ServiceMonitor and PrometheusRule
	// CRDs are registered.
	prometheusOperator bool
	// networkPolicyAPI is true when networking.k8s.io/NetworkPolicy is
	// served (a NetworkPolicy-supporting CNI is the §13.2 prerequisite).
	networkPolicyAPI bool
	// notes carries per-probe diagnostics (absences, parse failures) so the
	// summary explains why a capability reads as missing.
	notes []string
}

// clusterDetector probes a target cluster. The real implementation shells
// out to kubectl; tests inject a fake.
type clusterDetector interface {
	detect(ctx context.Context) clusterDetection
}

// kubectlDetector runs the detection probes through kubectl against the
// resolved kubeconfig context. The command runner is injectable so the
// probe-parsing logic is unit-testable without a cluster.
type kubectlDetector struct {
	// kubeContext overrides the current kubeconfig context (§17.6 line 673
	// "--context").
	kubeContext string
	// run executes kubectl with the given args and returns stdout. A
	// non-nil error means the probe failed (API absent, unreachable, RBAC).
	run func(ctx context.Context, args ...string) ([]byte, error)
}

// newKubectlDetector builds a detector that invokes the kubectl binary on
// PATH. When kubectl is absent, detect reports the cluster as unavailable.
func newKubectlDetector(kubeContext string) *kubectlDetector {
	return &kubectlDetector{
		kubeContext: kubeContext,
		run: func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, "kubectl", args...).Output()
		},
	}
}

// kubectlArgs prepends the --context flag (when set) to a probe's args.
func (d *kubectlDetector) kubectlArgs(args ...string) []string {
	if d.kubeContext != "" {
		return append([]string{"--context", d.kubeContext}, args...)
	}
	return args
}

// detect runs every probe, accumulating findings and diagnostics. A probe
// failure is recorded as a note and never aborts the wizard: detection is
// advisory (§17.6 — the summary precedes the questions, it does not gate
// them). When kubectl is absent from PATH the cluster reads as undetected
// and the wizard uses static defaults.
func (d *kubectlDetector) detect(ctx context.Context) clusterDetection {
	if _, err := exec.LookPath("kubectl"); err != nil {
		return clusterDetection{
			notes: []string{"kubectl not found on PATH; skipping cluster detection (answers fall back to static defaults)"},
		}
	}
	return d.detectWithoutLookup(ctx)
}

// detectWithoutLookup runs the probes against the injected command runner,
// skipping the kubectl-on-PATH check. It is the unit-testable core of
// detect.
func (d *kubectlDetector) detectWithoutLookup(ctx context.Context) clusterDetection {
	res := clusterDetection{available: true}

	// Kubernetes version. A failure here means the API server is
	// unreachable, so the cluster reads as unavailable.
	if out, err := d.run(ctx, d.kubectlArgs("version", "-o", "json")...); err != nil {
		res.available = false
		res.notes = append(res.notes, "could not reach the cluster API server; skipping detection (answers fall back to static defaults)")
		return res
	} else if v := parseServerVersion(out); v != "" {
		res.kubernetesVersion = v
	}

	if out, err := d.run(ctx, d.kubectlArgs("get", "runtimeclass", "-o", "json")...); err != nil {
		res.notes = append(res.notes, "no RuntimeClass objects found; §5.3 sandboxing needs a gVisor or Kata RuntimeClass")
	} else {
		res.runtimeClasses = parseObjectNames(out)
		if len(res.runtimeClasses) == 0 {
			res.notes = append(res.notes, "no RuntimeClass objects found; §5.3 sandboxing needs a gVisor or Kata RuntimeClass")
		}
	}

	if out, err := d.run(ctx, d.kubectlArgs("get", "clusterissuers.cert-manager.io", "-o", "json")...); err != nil {
		res.notes = append(res.notes, "cert-manager ClusterIssuer API not found; TLS defaults to bring-your-own")
	} else {
		res.certManagerInstalled = true
		res.readyClusterIssuers = parseReadyClusterIssuers(out)
		if len(res.readyClusterIssuers) == 0 {
			res.notes = append(res.notes, "cert-manager is installed but no ClusterIssuer is Ready; TLS defaults to bring-your-own")
		}
	}

	// Prometheus Operator CRDs. `kubectl get crd <a> <b>` exits non-zero
	// when either is absent, which is the signal the operator is not
	// installed.
	if _, err := d.run(ctx, d.kubectlArgs("get", "crd",
		"servicemonitors.monitoring.coreos.com",
		"prometheusrules.monitoring.coreos.com")...); err == nil {
		res.prometheusOperator = true
	} else {
		res.notes = append(res.notes, "Prometheus Operator CRDs (ServiceMonitor, PrometheusRule) not found; set monitoring.format=configmap")
	}

	// NetworkPolicy API surface. `api-resources` lists the served kinds in
	// the networking.k8s.io group; a NetworkPolicy-supporting CNI registers
	// networkpolicies there.
	if out, err := d.run(ctx, d.kubectlArgs("api-resources", "--api-group=networking.k8s.io", "-o", "name")...); err == nil {
		res.networkPolicyAPI = strings.Contains(string(out), "networkpolicies")
	}
	if !res.networkPolicyAPI {
		res.notes = append(res.notes, "NetworkPolicy API not detected; §13.2 isolation requires a NetworkPolicy-supporting CNI")
	}

	return res
}

// parseServerVersion extracts serverVersion.gitVersion from `kubectl version
// -o json`. An unparseable payload yields the empty string.
func parseServerVersion(out []byte) string {
	var v struct {
		ServerVersion struct {
			GitVersion string `json:"gitVersion"`
		} `json:"serverVersion"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return ""
	}
	return v.ServerVersion.GitVersion
}

// parseObjectNames extracts metadata.name from a `kubectl get ... -o json`
// list payload, sorted for stable output.
func parseObjectNames(out []byte) []string {
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil
	}
	names := make([]string, 0, len(list.Items))
	for _, it := range list.Items {
		if it.Metadata.Name != "" {
			names = append(names, it.Metadata.Name)
		}
	}
	sort.Strings(names)
	return names
}

// parseReadyClusterIssuers returns the names of ClusterIssuers whose Ready
// condition status is "True".
func parseReadyClusterIssuers(out []byte) []string {
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil
	}
	var ready []string
	for _, it := range list.Items {
		for _, c := range it.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" && it.Metadata.Name != "" {
				ready = append(ready, it.Metadata.Name)
				break
			}
		}
	}
	sort.Strings(ready)
	return ready
}

// summaryLines renders the detection findings as the §17.6 pre-question
// summary. It always reports each probed capability so the operator sees a
// complete picture rather than only the positives.
func (d clusterDetection) summaryLines() []string {
	if d.skipped {
		return []string{"# Detection phase skipped (--offline); answers use static defaults."}
	}
	if !d.available {
		lines := []string{"# Detection phase: cluster not reachable; answers use static defaults."}
		for _, n := range d.notes {
			lines = append(lines, "#   - "+n)
		}
		return lines
	}
	lines := []string{"# Detection phase — cluster capability summary:"}
	lines = append(lines, "#   Kubernetes version: "+orNone(d.kubernetesVersion))
	lines = append(lines, "#   RuntimeClasses: "+orNone(strings.Join(d.runtimeClasses, ", ")))
	issuers := "none"
	if d.certManagerInstalled {
		issuers = "cert-manager installed; Ready ClusterIssuers: " + orNone(strings.Join(d.readyClusterIssuers, ", "))
	}
	lines = append(lines, "#   cert-manager: "+issuers)
	lines = append(lines, "#   Prometheus Operator CRDs: "+yesNo(d.prometheusOperator))
	lines = append(lines, "#   NetworkPolicy API: "+yesNo(d.networkPolicyAPI))
	for _, n := range d.notes {
		lines = append(lines, "#   - "+n)
	}
	return lines
}

// printDetectionSummary writes the detection summary to w.
func printDetectionSummary(w io.Writer, d clusterDetection) {
	for _, l := range d.summaryLines() {
		fmt.Fprintln(w, l)
	}
	fmt.Fprintln(w)
}

// tlsDefaults derives the §17.6 line 689 TLS-strategy default from
// detection: cert-manager when a Ready ClusterIssuer exists (prefilling the
// issuer name when exactly one is Ready, in which case the strategy question
// is unambiguous and the wizard skips it), bring-your-own otherwise. When
// detection produced no cluster signal it returns the wizard's historical
// blank default so the operator is still asked.
func tlsDefaults(d clusterDetection) (strategy, issuer string, skipPrompt bool) {
	if d.skipped || !d.available {
		return "", "", false
	}
	if len(d.readyClusterIssuers) > 0 {
		issuerName := ""
		skip := false
		if len(d.readyClusterIssuers) == 1 {
			issuerName = d.readyClusterIssuers[0]
			skip = true
		}
		return "cert-manager", issuerName, skip
	}
	// Cluster reachable, no Ready issuer: bring-your-own is the documented
	// fallback default.
	return "bring-your-own", "", false
}

// orNone returns s, or "none" when s is empty, for summary rendering.
func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}

// yesNo renders a detected-boolean as "detected"/"not detected".
func yesNo(b bool) string {
	if b {
		return "detected"
	}
	return "not detected"
}
