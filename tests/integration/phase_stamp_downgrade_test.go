// SPDX-License-Identifier: MIT

//go:build integration

package integration_test

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// downgradeErrCode is the §17.2 line 76 render-time fail-closed error the
// phase-stamp ConfigMap template raises when a recorded-enabled
// admission-plane feature flag is rendered as disabled without the
// acceptFeatureFlagDowngrade override.
const downgradeErrCode = "PHASE_STAMP_FEATURE_FLAG_DOWNGRADE"

// chartDir is the chart path relative to tests/integration/.
const chartDir = "../../charts/lenny"

// baseSet are the chart values every render needs that are unrelated to
// the phase-stamp guard under test.
var baseSet = []string{"global.spiffeTrustDomain=lenny-test", "coredns.clusterIP=10.96.0.10"}

// TestPhaseStampDowngradeRenderTimeGuard is the §17.2
// phase_stamp_downgrade_test.go suite (line 76). It parameterises every
// (from, to) transition for each admission-plane feature flag and asserts
// the layer-2 render-time fail-closed gate, exercised against a live
// cluster via `helm install --dry-run=server` so Helm's `lookup` reads a
// seeded phase-stamp ConfigMap:
//
//	(a) true → false without the override fails the render with
//	    PHASE_STAMP_FEATURE_FLAG_DOWNGRADE;
//	(b) true → false with acceptFeatureFlagDowngrade.<flag>=true renders
//	    (the guard does not fire);
//	(c) false → true renders (no prior enabled entry to downgrade);
//	(d) true → true renders (the flag is unchanged).
//
// The render-time `fail` aborts template execution before Helm builds
// Kubernetes objects, so the assertion keys on the presence/absence of
// the downgrade error code; a success-path render may still fail later on
// unrelated server-side CRD validation (cert-manager Certificates absent
// on the bare cluster), which is out of scope for the guard under test.
//
// spec: §17.2 line 76 (layer-2 fail-closed chart render-time validation).
// F-17.2.15 / F-17.2.7.
func TestPhaseStampDowngradeRenderTimeGuard(t *testing.T) {
	kind.SkipUnlessAvailable(t)
	helm.SkipUnlessAvailable(t)
	c := kind.EnsureCluster(t)

	flags := []string{"llmProxy", "drainReadiness", "compliance"}
	for _, flag := range flags {
		flag := flag
		t.Run(flag, func(t *testing.T) {
			t.Run("true_to_false_without_override_fails", func(t *testing.T) {
				ns := phaseNS(flag, "downgrade")
				seedPhaseStamp(t, c, ns, []string{flag})
				out, err := helmDryRunServer(t, c, ns, []string{"features." + flag + "=false"})
				if err == nil {
					t.Fatalf("render succeeded; a %s true→false downgrade without the override must fail:\n%s", flag, out)
				}
				if !strings.Contains(out, downgradeErrCode) {
					t.Fatalf("downgrade did not raise %s for %s:\n%s", downgradeErrCode, flag, out)
				}
				if !strings.Contains(out, fmt.Sprintf("%q", flag)) {
					t.Errorf("downgrade error does not name flag %q:\n%s", flag, out)
				}
			})

			t.Run("true_to_false_with_override_renders", func(t *testing.T) {
				ns := phaseNS(flag, "ack")
				seedPhaseStamp(t, c, ns, []string{flag})
				out, _ := helmDryRunServer(t, c, ns, []string{
					"features." + flag + "=false",
					"acceptFeatureFlagDowngrade." + flag + "=true",
				})
				if strings.Contains(out, downgradeErrCode) {
					t.Fatalf("an acknowledged %s downgrade must not raise %s:\n%s", flag, downgradeErrCode, out)
				}
			})

			t.Run("false_to_true_renders", func(t *testing.T) {
				ns := phaseNS(flag, "enable")
				// No prior ConfigMap: a fresh install has nothing to downgrade.
				seedPhaseStamp(t, c, ns, nil)
				out, _ := helmDryRunServer(t, c, ns, []string{"features." + flag + "=true"})
				if strings.Contains(out, downgradeErrCode) {
					t.Fatalf("a %s false→true enable must not raise %s:\n%s", flag, downgradeErrCode, out)
				}
			})

			t.Run("true_to_true_renders", func(t *testing.T) {
				ns := phaseNS(flag, "stable")
				seedPhaseStamp(t, c, ns, []string{flag})
				out, _ := helmDryRunServer(t, c, ns, []string{"features." + flag + "=true"})
				if strings.Contains(out, downgradeErrCode) {
					t.Fatalf("a %s true→true no-op must not raise %s:\n%s", flag, downgradeErrCode, out)
				}
			})
		})
	}
}

// TestPhaseStampDowngradeHelmTemplateIsNoOp is the companion the spec
// names: under `helm template` Helm's `lookup` returns empty (it does not
// connect to a cluster), so the layer-2 render-time guard does NOT fire
// even for a flag the cluster records as enabled. This verifies the
// preflight Job's PREFLIGHT_PHASE_STAMP_MISMATCH check is the sole
// enforcement on the GitOps code path.
//
// spec: §17.2 line 76 (scope limitation: lookup is empty under helm
// template). F-17.2.15 / F-17.2.7.
func TestPhaseStampDowngradeHelmTemplateIsNoOp(t *testing.T) {
	helm.SkipUnlessAvailable(t)
	// helm template never connects to a cluster, so no seeding is needed:
	// lookup is empty and a downgrade renders without the guard firing.
	args := append([]string{"template", "lenny", chartDir, "--namespace", "ps-template"}, setArgs(append(baseSet, "features.compliance=false"))...)
	out, err := exec.Command(helmBinary(), args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template must render a downgrade (the guard is a no-op without a live cluster): %v\n%s", err, out)
	}
	if strings.Contains(string(out), downgradeErrCode) {
		t.Fatalf("helm template raised %s; the render-time guard must be a no-op under helm template:\n%s", downgradeErrCode, out)
	}
}

func phaseNS(flag, kind string) string {
	return strings.ToLower("ps-" + flag + "-" + kind)
}

// seedPhaseStamp creates ns and, for each enabled flag, a
// lenny-deployment-phase-stamp ConfigMap entry recording it as enabled,
// mirroring the append-only schema the chart writes. A nil enabled set
// creates only the namespace (a fresh install with no prior phase stamp).
func seedPhaseStamp(t *testing.T, c *kind.Cluster, ns string, enabled []string) {
	t.Helper()
	// A prior run may have left the namespace; delete then recreate so the
	// seeded ConfigMap reflects exactly this case.
	_ = c.Kubectl("delete", "ns", ns, "--ignore-not-found", "--wait=true").Run()
	if out, err := c.Kubectl("create", "ns", ns).CombinedOutput(); err != nil {
		t.Fatalf("create ns %s: %v\n%s", ns, err, out)
	}
	t.Cleanup(func() { _ = c.Kubectl("delete", "ns", ns, "--ignore-not-found", "--wait=false").Run() })
	if len(enabled) == 0 {
		return
	}
	args := []string{"create", "configmap", "lenny-deployment-phase-stamp", "-n", ns}
	for _, f := range enabled {
		args = append(args, fmt.Sprintf(`--from-literal=%s={"enabled":true,"enabledAt":"2026-01-01T00:00:00Z"}`, f))
	}
	if out, err := c.Kubectl(args...).CombinedOutput(); err != nil {
		t.Fatalf("seed phase-stamp ConfigMap in %s: %v\n%s", ns, err, out)
	}
}

// helmDryRunServer runs `helm install --dry-run=server` against the kind
// cluster so the chart's lookup-backed render-time guard reads the seeded
// ConfigMap. Returns the combined output and the helm exit error.
func helmDryRunServer(t *testing.T, c *kind.Cluster, ns string, set []string) (string, error) {
	t.Helper()
	args := []string{
		"install", "lenny", chartDir,
		"--namespace", ns,
		"--dry-run=server",
		"--kubeconfig", c.KubeconfigPath,
	}
	args = append(args, setArgs(append(append([]string{}, baseSet...), set...))...)
	out, err := exec.Command(helmBinary(), args...).CombinedOutput()
	return string(out), err
}

func setArgs(values []string) []string {
	args := make([]string, 0, len(values)*2)
	for _, v := range values {
		args = append(args, "--set", v)
	}
	return args
}

func helmBinary() string {
	if b := strings.TrimSpace(execLookHelm()); b != "" {
		return b
	}
	return "helm"
}

func execLookHelm() string {
	p, err := exec.LookPath("helm")
	if err != nil {
		return ""
	}
	return p
}
