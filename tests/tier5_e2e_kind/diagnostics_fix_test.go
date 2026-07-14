// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §25.6 auto-remediation `fix=true` mode
// driven through the deployed lenny-ops binary against a live cluster.
//
// pkg/ops/doctor and cmd/lenny-ops/doctorremediator*.go are unit- and
// component-tested against fake clientsets only; no test drives
// detection or remediation through the real HTTP listener on the
// deployed lenny-ops binary against genuine Kubernetes API state. This
// test creates a short-lived cert-manager Certificate in the release
// namespace — the certManagerExpiring finding's live signal is a
// Certificate whose status.notAfter falls inside the 7-day window and
// whose Ready condition is True — waits for cert-manager to actually
// issue it, then drives POST /v1/admin/diagnostics/run against the
// deployed lenny-ops over a port-forward: first read-only (the finding
// must be genuinely detected from live cluster state, not a fake
// clientset), then ?fix=true (the remediation must actually run: the
// Certificate is annotated to force temporary reissuance and its Secret
// is deleted, which the live cert-manager controller observes and reacts
// to by reissuing a new Secret), and finally a second ?fix=true run (the
// remediation must be idempotent — a repeat run against the same
// already-fixed finding succeeds rather than erroring).
//
// certManagerExpiring is the one §25.6 fixable finding whose live-cluster
// detection precondition can be constructed deterministically in a
// bounded e2e run:
//   - coreDnsStuckEndpoint requires a Ready CoreDNS pod whose IP is
//     transiently absent from the kube-dns Endpoints object. Forcing
//     that window races the endpoints controller's own reconcile loop
//     (any direct Endpoints edit is corrected on the controller's next
//     resync) and cannot be forced deterministically without disrupting
//     cluster-wide DNS for the shared e2e cluster.
//   - bootstrapConfigDrift and prometheusRuleMissing are undetectable on
//     any current chart install: the chart never sets --doctor-render-dir
//     (see cmd/lenny-ops/doctorrender.go), so HelmRenderSource is always
//     nil and both findings permanently report not_detected. That gap is
//     a chart-wiring defect out of scope for this test.
//   - warmPoolStuckReplenish requires a pool to have dwelt in
//     DEMAND_EXCEEDS_SUPPLY with zero in-flight claims for over 5
//     minutes, which does not fit a bounded e2e run.
//
// spec: §25.6 lines 2941-2982 (auto-remediation `fix=true` mode); the
// certManagerExpiring row of the fixable-finding table ("Certificate
// within 7 days of expiry and cert-manager healthy" / "Annotates
// Certificate with cert-manager.io/issue-temporary-certificate: \"true\"
// and deletes the Secret to force re-issuance").

package tier5_e2e_kind_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// diagFixCertName and diagFixSecretName name the throwaway Certificate
// this test creates in lenny-system to give certManagerExpiring a
// genuine, deterministic live detection target without touching any
// certificate the running control plane depends on.
const (
	diagFixCertName   = "doctor-fix-e2e-cert"
	diagFixSecretName = "doctor-fix-e2e-cert-tls"
)

// diagFixCertManifest renders a cert-manager Certificate with a 1-hour
// duration, well inside the §25.6 7-day certManagerExpiring window. It
// chains to lenny-webhook-selfsign, the self-signed Issuer every e2e
// install already renders for the admission-webhook serving certs
// (mtls_test.go asserts it Ready), so issuance needs no ACME account or
// external CA and completes in well under this test's poll deadline.
func diagFixCertManifest() string {
	return fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: %s
  namespace: %s
spec:
  secretName: %s
  commonName: doctor-fix-e2e.lenny-system.svc
  dnsNames:
    - doctor-fix-e2e.lenny-system.svc
  duration: 1h
  renewBefore: 30m
  issuerRef:
    name: lenny-webhook-selfsign
    kind: Issuer
`, diagFixCertName, t5SystemNS, diagFixSecretName)
}

// diagFixOpsSARBACGranted reports whether the lenny-ops-sa ServiceAccount
// currently holds the RBAC grants the §25.6 certManagerExpiring detection
// and remediation need: patch on cert-manager.io Certificates and delete
// on Secrets, both in the release namespace (charts/lenny/templates/
// ops-rbac.yaml). This is read via `kubectl auth can-i --as`, a
// SelfSubjectAccessReview-style read that changes no cluster state, so a
// cluster whose chart install predates that RBAC grant lets this test
// skip cleanly instead of failing on a precondition the test itself
// cannot satisfy: no earlier tier grants RBAC, and applying it here would
// make an e2e test respond to a failing precondition by mutating the
// cluster's authorization surface out of band.
func diagFixOpsSARBACGranted(t *testing.T, c *kind.Cluster) bool {
	t.Helper()
	sa := "system:serviceaccount:" + t5SystemNS + ":lenny-ops-sa"
	certOK, _ := c.KubectlOut(t, "auth", "can-i", "patch", "certificates.cert-manager.io", "-n", t5SystemNS, "--as="+sa)
	secretOK, _ := c.KubectlOut(t, "auth", "can-i", "delete", "secrets", "-n", t5SystemNS, "--as="+sa)
	return strings.TrimSpace(certOK) == "yes" && strings.TrimSpace(secretOK) == "yes"
}

// diagFixWaitCertIssued polls the Certificate until cert-manager reports
// it Ready, or fails the test at the deadline. A freshly-applied
// Certificate against the always-present self-signed Issuer issues
// within a few seconds; 90s leaves ample margin for a loaded CI node.
func diagFixWaitCertIssued(t *testing.T, c *kind.Cluster) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		out, _ := c.KubectlOut(
			t, "-n", t5SystemNS, "get", "certificate", diagFixCertName,
			"-o", "jsonpath={range .status.conditions[?(@.type==\"Ready\")]}{.status}{end}",
		)
		if strings.TrimSpace(out) == "True" {
			return
		}
		if time.Now().After(deadline) {
			desc, _ := c.KubectlOut(t, "-n", t5SystemNS, "describe", "certificate", diagFixCertName)
			t.Fatalf("certificate %s/%s did not reach Ready within 90s (last Ready status %q); "+
				"the certManagerExpiring live-detection precondition never became true\n--- describe ---\n%s",
				t5SystemNS, diagFixCertName, strings.TrimSpace(out), desc)
		}
		time.Sleep(2 * time.Second)
	}
}

// diagFixSecretUID reads the backing Secret's UID, or "" when the
// Secret does not currently exist (the brief window between the fix's
// delete and cert-manager's reissuance).
func diagFixSecretUID(t *testing.T, c *kind.Cluster) string {
	t.Helper()
	out, _ := c.KubectlOut(t, "-n", t5SystemNS, "get", "secret", diagFixSecretName, "-o", "jsonpath={.metadata.uid}")
	return strings.TrimSpace(out)
}

// diagFixWaitSecretRecreated polls until the Secret's UID differs from
// before (cert-manager observed the fix's Secret deletion and reissued),
// or fails the test at the deadline.
func diagFixWaitSecretRecreated(t *testing.T, c *kind.Cluster, before string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		uid := diagFixSecretUID(t, c)
		if uid != "" && uid != before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("secret %s/%s was not recreated with a new UID within 60s of the fix run (before=%q, "+
				"last-seen=%q); the certManagerExpiring remediation did not force a genuine cert-manager "+
				"reissuance on the live cluster", t5SystemNS, diagFixSecretName, before, uid)
		}
		time.Sleep(1 * time.Second)
	}
}

// diagFixFindingWire mirrors the wire shape of one §25.6
// doctor.FindingResult entry (pkg/ops/doctor.go). Decoded from the raw
// HTTP response rather than importing the product type, matching this
// package's black-box convention (see opsMCPToolCall).
type diagFixFindingWire struct {
	Finding     string `json:"finding"`
	Resource    string `json:"resource"`
	Remediation string `json:"remediation"`
	Result      string `json:"result"`
	Reason      string `json:"reason"`
	Detail      string `json:"detail"`
	Error       string `json:"error"`
}

// diagFixReportWire mirrors the wire shape of the §25.6 doctor.RunReport
// envelope POST /v1/admin/diagnostics/run returns.
type diagFixReportWire struct {
	OperationID  string               `json:"operationId"`
	Fix          bool                 `json:"fix"`
	Findings     []diagFixFindingWire `json:"findings"`
	AppliedCount int                  `json:"appliedCount"`
	SkippedCount int                  `json:"skippedCount"`
	FailedCount  int                  `json:"failedCount"`
	Progress     map[string]any       `json:"progress"`
}

// diagFixRun posts to the deployed lenny-ops POST
// /v1/admin/diagnostics/run[?fix=true], scoped to the given finding
// codes, over the dev-mode identity headers the e2e ops binary honors
// (matching opsMCPToolCall). It fails the test on a non-200 response.
func diagFixRun(t *testing.T, baseURL string, fix bool, findings []string) diagFixReportWire {
	t.Helper()
	path := "/v1/admin/diagnostics/run"
	if fix {
		path += "?fix=true"
	}
	raw, err := json.Marshal(map[string]any{"findings": findings})
	if err != nil {
		t.Fatalf("marshal diagnostics/run request body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build diagnostics/run request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "platform")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	req.Header.Set("X-Lenny-User-ID", "alice")

	hc := &http.Client{Timeout: 30 * time.Second}
	res, err := hc.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read %s response: %v", path, err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("§25.6 POST %s on the deployed lenny-ops: HTTP status %d, body=%s", path, res.StatusCode, body)
	}
	var report diagFixReportWire
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("decode %s response: %v; body=%s", path, err, body)
	}
	return report
}

// diagFixFindingFor returns the entry in report.Findings whose Finding
// code matches code, or fails the test when absent.
func diagFixFindingFor(t *testing.T, report diagFixReportWire, code string) diagFixFindingWire {
	t.Helper()
	for _, f := range report.Findings {
		if f.Finding == code {
			return f
		}
	}
	t.Fatalf("§25.6 diagnostics/run response carries no %q finding entry; findings=%+v", code, report.Findings)
	return diagFixFindingWire{}
}

// spec: §25.6 lines 2941-2982 ("POST /v1/admin/diagnostics/run?fix=true
// ... Response is a long-running operation envelope"; certManagerExpiring
// row: detection "Certificate within 7 days of expiry and cert-manager
// healthy", remediation "Annotates Certificate with
// cert-manager.io/issue-temporary-certificate: \"true\" and deletes the
// Secret to force re-issuance", "Idempotent? Yes").
//
// diagnosis: a failure here means the §25.6 auto-remediation path is
// broken somewhere between the deployed lenny-ops HTTP listener and the
// live Kubernetes API: detection against the real cert-manager
// Certificate resource, the guardrail chain the REST handler applies
// before calling Apply, the annotate-and-delete-Secret remediation
// itself, or cert-manager's observed reaction to it. The in-process
// pkg/ops/doctor and cmd/lenny-ops/doctorremediator suites pin each of
// these in isolation against fakes; this is the only test that drives
// the full chain against a live cluster through the genuine binary.
func TestDiagnosticsRunFixCertManagerExpiring(t *testing.T) {
	c := kind.InstallLenny(t)

	if !t5DeploymentReady(t, c, "lenny-ops") {
		t.Skip("precondition not met: lenny-ops is not Ready; the §25.6 doctor auto-remediation endpoint is served by lenny-ops")
	}
	issuers := issuerReadiness(t, c)
	if issuers["lenny-webhook-selfsign"] != "True" {
		t.Skip("precondition not met: the lenny-webhook-selfsign cert-manager Issuer is not Ready; " +
			"this test chains its throwaway Certificate to it rather than standing up a dedicated Issuer")
	}
	if !diagFixOpsSARBACGranted(t, c) {
		t.Skip("precondition not met: the lenny-ops-sa ServiceAccount cannot get/list/watch/patch " +
			"cert-manager.io Certificates or delete Secrets in lenny-system yet; the §25.6 " +
			"certManagerExpiring auto-remediation needs this RBAC grant (charts/lenny/templates/ops-rbac.yaml) " +
			"deployed to this cluster (a `helm upgrade`) before the live-cluster fix path is reachable")
	}

	manifest := diagFixCertManifest()
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("apply the doctor-fix-e2e test Certificate: %v\n%s", err, out)
	}
	diagFixWaitCertIssued(t, c)

	baseURL, stop := c.PortForward(t, "svc/lenny-ops", t5SystemNS, opsHTTPPort)
	defer stop()

	certResource := t5SystemNS + "/" + diagFixCertName

	// --- read-only: the finding is genuinely detected from live cluster
	// state (the real cert-manager Certificate this test just had issued),
	// not a fake clientset.
	readReport := diagFixRun(t, baseURL, false, []string{"certManagerExpiring"})
	readFinding := diagFixFindingFor(t, readReport, "certManagerExpiring")
	if readFinding.Result != "detected" {
		t.Fatalf("§25.6 read-only diagnostics/run on the deployed lenny-ops did not detect the live "+
			"near-expiry Certificate: finding=%+v", readFinding)
	}
	if readFinding.Resource != certResource {
		t.Fatalf("§25.6 read-only diagnostics/run detected resource %q, want %q; the live detection is "+
			"scanning the wrong Certificate", readFinding.Resource, certResource)
	}
	t.Logf("§25.6 read-only: certManagerExpiring detected on the deployed lenny-ops for %s", readFinding.Resource)

	// --- fix=true: the remediation actually runs against the live
	// cluster. Capture the Secret's UID first so a later poll can
	// confirm cert-manager genuinely reissued it rather than the fix
	// silently no-oping.
	secretUIDBeforeFix := diagFixSecretUID(t, c)
	if secretUIDBeforeFix == "" {
		t.Fatalf("the doctor-fix-e2e Secret %s/%s does not exist even though the Certificate reported Ready",
			t5SystemNS, diagFixSecretName)
	}

	fixReport := diagFixRun(t, baseURL, true, []string{"certManagerExpiring"})
	fixFinding := diagFixFindingFor(t, fixReport, "certManagerExpiring")
	if fixFinding.Result != "applied" {
		t.Fatalf("§25.6 fix=true diagnostics/run on the deployed lenny-ops did not apply the "+
			"certManagerExpiring remediation: finding=%+v (full report: %+v)", fixFinding, fixReport)
	}
	if fixReport.AppliedCount != 1 || fixReport.FailedCount != 0 {
		t.Fatalf("§25.6 fix=true diagnostics/run counts = %+v, want appliedCount=1 failedCount=0", fixReport)
	}
	if fixReport.OperationID == "" {
		t.Errorf("§25.6 fix=true diagnostics/run returned no operationId; the progress envelope must carry one")
	}

	// The remediation is "annotate + delete Secret"; cert-manager's own
	// controller loop (not lenny-ops) does the actual reissuance. Poll
	// the live cluster for the Secret to come back with a new UID,
	// confirming the fix reached the real cert-manager control loop
	// rather than only returning a well-formed HTTP response.
	diagFixWaitSecretRecreated(t, c, secretUIDBeforeFix)
	t.Logf("§25.6 fix=true: certManagerExpiring applied on the deployed lenny-ops; cert-manager reissued %s/%s",
		t5SystemNS, diagFixSecretName)

	// --- idempotent re-run: the spec's fixable-finding table marks
	// certManagerExpiring "Idempotent? Yes". The reissued certificate
	// still carries a 1h duration, so it is still inside the 7-day
	// window and the finding is still detected; a second fix=true run
	// must still succeed rather than erroring on an already-remediated
	// resource.
	rerunReport := diagFixRun(t, baseURL, true, []string{"certManagerExpiring"})
	rerunFinding := diagFixFindingFor(t, rerunReport, "certManagerExpiring")
	if rerunFinding.Result != "applied" || rerunFinding.Error != "" {
		t.Fatalf("§25.6 idempotent fix=true re-run did not succeed cleanly: finding=%+v", rerunFinding)
	}
	t.Logf("§25.6 idempotent re-run: certManagerExpiring applied cleanly a second time")
}
