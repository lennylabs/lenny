// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// fakeKubectl maps a kubectl sub-verb (args[0], or args[1] when args[0] is
// "get") to a canned (stdout, error) pair so the detector's probe-parsing is
// exercised without a cluster.
type fakeKubectl struct {
	version       string // serverVersion.gitVersion JSON payload helper
	runtimeClass  []byte
	clusterIssuer []byte
	issuerErr     error
	rcErr         error
	prometheusErr error
	netpol        string
	// §17.9.2 cluster-type detection inputs (F-17.9.8). nodes is the
	// `kubectl get nodes -o json` payload; openshiftCRD is the
	// `get crd clusterversions.config.openshift.io -o name` output (a
	// non-empty value marks the cluster as OpenShift).
	nodes        []byte
	nodesErr     error
	openshiftCRD []byte
	openshiftErr error
}

func (f fakeKubectl) run(_ context.Context, args ...string) ([]byte, error) {
	// Drop a leading "--context <name>" pair so the matcher sees the verb.
	if len(args) >= 2 && args[0] == "--context" {
		args = args[2:]
	}
	switch {
	case args[0] == "version":
		return []byte(fmt.Sprintf(`{"serverVersion":{"gitVersion":%q}}`, f.version)), nil
	case args[0] == "get" && args[1] == "runtimeclass":
		return f.runtimeClass, f.rcErr
	case args[0] == "get" && strings.HasPrefix(args[1], "clusterissuers"):
		return f.clusterIssuer, f.issuerErr
	case args[0] == "get" && args[1] == "crd" && len(args) > 2 && strings.HasPrefix(args[2], "clusterversions"):
		return f.openshiftCRD, f.openshiftErr
	case args[0] == "get" && args[1] == "crd":
		return nil, f.prometheusErr
	case args[0] == "get" && args[1] == "nodes":
		return f.nodes, f.nodesErr
	case args[0] == "api-resources":
		return []byte(f.netpol), nil
	}
	return nil, fmt.Errorf("unexpected kubectl args: %v", args)
}

// spec: §17.6 lines 671-697 — the detection phase parses RuntimeClasses,
// Ready ClusterIssuers, Prometheus Operator CRDs, and the NetworkPolicy API
// surface from kubectl output. F-17.6.9.
func TestDetectParsesClusterCapabilities_spec_17_6_671(t *testing.T) {
	fk := fakeKubectl{
		version: "v1.29.4",
		runtimeClass: []byte(`{"items":[
			{"metadata":{"name":"gvisor"}},
			{"metadata":{"name":"kata"}}]}`),
		clusterIssuer: []byte(`{"items":[
			{"metadata":{"name":"letsencrypt-prod"},"status":{"conditions":[{"type":"Ready","status":"True"}]}},
			{"metadata":{"name":"selfsigned"},"status":{"conditions":[{"type":"Ready","status":"False"}]}}]}`),
		prometheusErr: nil,
		netpol:        "networkpolicies.networking.k8s.io\n",
	}
	d := (&kubectlDetector{run: fk.run}).detectWithoutLookup(context.Background())

	if !d.available {
		t.Fatalf("cluster should read as available: %+v", d)
	}
	if d.kubernetesVersion != "v1.29.4" {
		t.Errorf("kubernetesVersion = %q, want v1.29.4", d.kubernetesVersion)
	}
	if len(d.runtimeClasses) != 2 || d.runtimeClasses[0] != "gvisor" || d.runtimeClasses[1] != "kata" {
		t.Errorf("runtimeClasses = %v, want [gvisor kata]", d.runtimeClasses)
	}
	if !d.certManagerInstalled {
		t.Errorf("certManagerInstalled = false, want true")
	}
	if len(d.readyClusterIssuers) != 1 || d.readyClusterIssuers[0] != "letsencrypt-prod" {
		t.Errorf("readyClusterIssuers = %v, want [letsencrypt-prod]", d.readyClusterIssuers)
	}
	if !d.prometheusOperator {
		t.Errorf("prometheusOperator = false, want true")
	}
	if !d.networkPolicyAPI {
		t.Errorf("networkPolicyAPI = false, want true")
	}
}

// When cert-manager and the Prometheus Operator are absent, the detector
// records diagnostics and the booleans stay false.
func TestDetectRecordsMissingCapabilities_spec_17_6_671(t *testing.T) {
	fk := fakeKubectl{
		version:       "v1.27.0",
		runtimeClass:  []byte(`{"items":[]}`),
		issuerErr:     fmt.Errorf("the server doesn't have a resource type \"clusterissuers\""),
		prometheusErr: fmt.Errorf("crd not found"),
		netpol:        "",
	}
	d := (&kubectlDetector{run: fk.run}).detectWithoutLookup(context.Background())

	if d.certManagerInstalled {
		t.Errorf("certManagerInstalled = true, want false")
	}
	if d.prometheusOperator {
		t.Errorf("prometheusOperator = true, want false")
	}
	if d.networkPolicyAPI {
		t.Errorf("networkPolicyAPI = true, want false")
	}
	joined := strings.Join(d.notes, " | ")
	for _, want := range []string{"RuntimeClass", "cert-manager", "Prometheus Operator", "NetworkPolicy"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes missing %q diagnostic: %v", want, d.notes)
		}
	}
}

// An unreachable API server (the version probe errors) reads as unavailable
// and the wizard falls back to static defaults.
func TestDetectUnreachableCluster_spec_17_6_671(t *testing.T) {
	run := func(_ context.Context, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("connection refused")
	}
	d := (&kubectlDetector{run: run}).detectWithoutLookup(context.Background())
	if d.available {
		t.Fatalf("unreachable cluster should not read as available: %+v", d)
	}
	if len(d.notes) == 0 {
		t.Errorf("expected a diagnostic note for the unreachable cluster")
	}
}

// spec: §17.6 line 689 — the TLS-strategy default keys off detection:
// cert-manager with a single Ready issuer skips the prompt; multiple Ready
// issuers default to cert-manager but still prompt; no Ready issuer falls
// back to bring-your-own.
func TestTLSDefaultsFromDetection_spec_17_6_689(t *testing.T) {
	cases := []struct {
		name       string
		det        clusterDetection
		wantStrat  string
		wantIssuer string
		wantSkip   bool
	}{
		{
			name:       "single ready issuer skips prompt",
			det:        clusterDetection{available: true, readyClusterIssuers: []string{"letsencrypt-prod"}},
			wantStrat:  "cert-manager",
			wantIssuer: "letsencrypt-prod",
			wantSkip:   true,
		},
		{
			name:      "multiple ready issuers default cert-manager but prompt",
			det:       clusterDetection{available: true, readyClusterIssuers: []string{"a", "b"}},
			wantStrat: "cert-manager",
			wantSkip:  false,
		},
		{
			name:      "no ready issuer falls back to bring-your-own",
			det:       clusterDetection{available: true},
			wantStrat: "bring-your-own",
			wantSkip:  false,
		},
		{
			name:      "unavailable cluster leaves the blank default",
			det:       clusterDetection{available: false},
			wantStrat: "",
			wantSkip:  false,
		},
		{
			name:      "offline leaves the blank default",
			det:       clusterDetection{skipped: true},
			wantStrat: "",
			wantSkip:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			strat, issuer, skip := tlsDefaults(tc.det)
			if strat != tc.wantStrat || skip != tc.wantSkip {
				t.Fatalf("tlsDefaults = (%q, %q, %v), want (%q, _, %v)",
					strat, issuer, skip, tc.wantStrat, tc.wantSkip)
			}
			if tc.wantIssuer != "" && issuer != tc.wantIssuer {
				t.Errorf("issuer = %q, want %q", issuer, tc.wantIssuer)
			}
		})
	}
}

func TestDetectionSummaryRendersFindings(t *testing.T) {
	d := clusterDetection{
		available:            true,
		kubernetesVersion:    "v1.29.4",
		runtimeClasses:       []string{"gvisor"},
		certManagerInstalled: true,
		readyClusterIssuers:  []string{"letsencrypt-prod"},
		prometheusOperator:   true,
		networkPolicyAPI:     true,
	}
	out := strings.Join(d.summaryLines(), "\n")
	for _, want := range []string{"v1.29.4", "gvisor", "letsencrypt-prod", "Prometheus Operator CRDs: detected", "NetworkPolicy API: detected"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}

	skipped := clusterDetection{skipped: true}
	if !strings.Contains(strings.Join(skipped.summaryLines(), "\n"), "skipped") {
		t.Errorf("offline summary should report the skip")
	}
}

// spec: §17.9.2 line 1376 — the detection phase infers the §17.9.1
// cluster-type dimension from node providerIDs and the OpenShift API
// surface, and suggests the matching §17.9.2 catalog answer-file base.
// F-17.9.8.
func TestDetectClusterTypeSuggestsAnswerFile_spec_17_9_2_1376(t *testing.T) {
	nodesWith := func(providerID string) []byte {
		return []byte(fmt.Sprintf(`{"items":[{"spec":{"providerID":%q}}]}`, providerID))
	}
	cases := []struct {
		name        string
		fk          fakeKubectl
		wantType    string
		wantAnswers string
	}{
		{
			name:        "EKS from an aws:// providerID suggests eks-small-team",
			fk:          fakeKubectl{version: "v1.29.4", nodes: nodesWith("aws:///us-east-1a/i-0abc")},
			wantType:    "eks",
			wantAnswers: "eks-small-team.yaml",
		},
		{
			name:        "GKE from a gce:// providerID suggests gke-production",
			fk:          fakeKubectl{version: "v1.29.4", nodes: nodesWith("gce://acme-project/us-central1-a/gke-node")},
			wantType:    "gke",
			wantAnswers: "gke-production.yaml",
		},
		{
			name:        "AKS from an azure:// providerID suggests aks-production",
			fk:          fakeKubectl{version: "v1.29.4", nodes: nodesWith("azure:///subscriptions/abc/vm")},
			wantType:    "aks",
			wantAnswers: "aks-production.yaml",
		},
		{
			name:        "kind providerID suggests the laptop answer file",
			fk:          fakeKubectl{version: "v1.29.4", nodes: nodesWith("kind://docker/kind/kind-control-plane")},
			wantType:    "laptop",
			wantAnswers: "laptop.yaml",
		},
		{
			// OpenShift on AWS still reports aws:// node providerIDs, but the
			// OpenShift API surface takes precedence so it maps to the
			// openshift self-managed answer file rather than eks.
			name: "OpenShift on AWS suggests openshift-self-managed",
			fk: fakeKubectl{
				version:      "v1.29.4",
				openshiftCRD: []byte("customresourcedefinition.apiextensions.k8s.io/clusterversions.config.openshift.io\n"),
				nodes:        nodesWith("aws:///us-east-1a/i-0abc"),
			},
			wantType:    "openshift",
			wantAnswers: "openshift-self-managed.yaml",
		},
		{
			name:        "k3s version suffix suggests the laptop answer file",
			fk:          fakeKubectl{version: "v1.29.4+k3s1", nodes: nodesWith("")},
			wantType:    "laptop",
			wantAnswers: "laptop.yaml",
		},
		{
			name:        "an empty providerID with no managed signal falls back to vanilla",
			fk:          fakeKubectl{version: "v1.29.4", nodes: nodesWith("")},
			wantType:    "vanilla",
			wantAnswers: "bare-metal-self-managed.yaml",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := (&kubectlDetector{run: tc.fk.run}).detectWithoutLookup(context.Background())
			if d.clusterType != tc.wantType {
				t.Errorf("clusterType = %q, want %q", d.clusterType, tc.wantType)
			}
			if d.suggestedAnswerFile != tc.wantAnswers {
				t.Errorf("suggestedAnswerFile = %q, want %q", d.suggestedAnswerFile, tc.wantAnswers)
			}
		})
	}
}

// The detection summary surfaces the inferred cluster type and the
// suggested catalog answer file so the operator sees the §17.9.2 line 1376
// auto-suggestion before any question. F-17.9.8.
func TestDetectionSummaryShowsClusterTypeSuggestion(t *testing.T) {
	d := clusterDetection{
		available:           true,
		kubernetesVersion:   "v1.29.4",
		clusterType:         "eks",
		suggestedAnswerFile: "eks-small-team.yaml",
		networkPolicyAPI:    true,
	}
	out := strings.Join(d.summaryLines(), "\n")
	for _, want := range []string{"Cluster type: eks", "charts/lenny/answers/catalog/eks-small-team.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}

// kubectlArgs prepends --context when set, leaving it off otherwise.
func TestKubectlArgsContext(t *testing.T) {
	d := &kubectlDetector{kubeContext: "prod"}
	got := d.kubectlArgs("get", "runtimeclass")
	if len(got) != 4 || got[0] != "--context" || got[1] != "prod" {
		t.Fatalf("kubectlArgs = %v, want [--context prod get runtimeclass]", got)
	}
	plain := (&kubectlDetector{}).kubectlArgs("version")
	if len(plain) != 1 || plain[0] != "version" {
		t.Fatalf("kubectlArgs without context = %v, want [version]", plain)
	}
}
