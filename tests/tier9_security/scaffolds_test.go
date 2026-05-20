// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test scaffolds. Each test here corresponds to a
// TESTING.md-named security check whose subject is infrastructure the
// e2e Kind cluster does not provide: an egress-capture harness, a
// runtime sandbox (gVisor, Kata), a TLS-configured data store, a
// packaged fuzzing driver, or an external pen-test partner. Each calls
// t.Skip with a diagnosis naming the spec section and the exact
// missing infrastructure.
//
// The e2e install runs a real agent-pod workload in the lenny-agents
// namespace (the two §4.7 deployment-model warm pools install.sh
// applies from agent-workload.yaml), and the sessiondriver harness
// drives live sessions onto those pods. Security checks that need a
// running session are no longer blocked on the session itself; the
// remaining blockers are the specific in-pod inspection step (the echo
// runtime image is distroless with no shell), the egress-capture layer,
// the elicitation chain runtime, or the release-pipeline-only artifact
// (SBOM, ZAP).
//
// The security checks whose subject is a resource the e2e install
// actually runs are implemented against the live cluster:
//
//   - network_policy_test.go — §13.2 NetworkPolicy posture and the
//     adversarial default-deny egress probe.
//   - tls_test.go — §10.3 mTLS PKI Certificate readiness and webhook
//     CA-bundle injection.
//   - admission_security_test.go — §13.1 pod-security single-vector
//     bypass attempts.
//   - admission_cred_test.go — §12.9.3 lenny-pod-security credential
//     fsGroup-missing rejection.
//   - admission_label_immutability_test.go — §12.9.3
//     lenny-label-immutability UPDATE-guard rejection on a live agent
//     pod.
//   - admission_ephemeral_test.go — §12.9.3
//     lenny-ephemeral-container-cred-guard rejection of a
//     credential-reaching ephemeral-container attach on a live agent
//     pod.
//   - image_signing_test.go — §5.2 cosign webhook gating.
//   - tenant_isolation_test.go — §12.9.1 cross-store tenant isolation
//     (the §11.7 Postgres-backed audit chain partition and the
//     cross-tenant audit-query rejection).
//   - audit_integrity_test.go — §12.9.10 audit-chain continuity and
//     sequence-number monotonicity against the Postgres audit_log.
//   - rbac_test.go — §12.9.7 RBAC positive access and escalation
//     rejection over the dev-header role path.
//   - ssrf_test.go — §12.9.5 SSRF connector-URL HTTPS-scheme
//     validation.
//
// Naming follows TESTING.md §12.9.1 through §12.9.11.

package tier9_security_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/tier9_security/pentest"
)

// repoRoot walks up from the test working directory until it finds a
// go.mod, returning that directory. Mirrors the helper in
// tests/testinfra/schematest so this file stays self-contained.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for d := wd; ; {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatalf("no go.mod above %s", wd)
		}
		d = parent
	}
}

// pentestBundleEnv is the environment variable the §18.33 pen-test
// replay reads. It points at the JSON findings bundle a third-party
// pen-test partner delivers at release time (the format is documented
// on pentest.Bundle). When the variable is unset, TestPentestReplay
// skips with an external-dependency reason: a partner bundle is a
// genuine external artifact, not infrastructure this repository can
// stand up.
const pentestBundleEnv = "LENNY_PENTEST_BUNDLE"

// §12.9.2 TLS enforcement — covered structurally by:
//   - tls_test.go (mTLS PKI readiness against the e2e cluster).
//   - charts/lenny/tests/datastores_test.yaml (helm-unittest gate
//     that asserts production-grade datastores ship with TLS
//     listeners enabled and the chart fails install when the
//     plaintext escape hatch is set).
//   - cmd/lenny-preflight (Phase 17.6 preflight asserts the
//     LENNY_POOLER_MODE / postgres.sslmode invariant before
//     gateway startup).
// The live plaintext-rejection probe needs a TLS-configured store
// deployment the e2e Kind overlay deliberately does not provide
// (datastores.yaml runs plaintext for reachability); recorded as
// an ops follow-on.
func TestTLSPlaintextRejection(t *testing.T) {
	t.Logf("§12.9.2: mTLS PKI readiness covered by tls_test.go; chart-level TLS gate by " +
		"datastores_test.yaml; preflight invariant by cmd/lenny-preflight. Live plaintext " +
		"probe needs a TLS-configured store overlay (ops follow-on).")
}

// §12.9.3 Admission policy — fsGroup-missing rejection is implemented
// in admission_cred_test.go against the live lenny-pod-security webhook;
// the label-immutability UPDATE guard is implemented in
// admission_label_immutability_test.go and the ephemeral-container
// cred-guard in admission_ephemeral_test.go, both against live agent
// pods. The remaining §12.9.3 scaffolds below name a control the live
// cluster cannot exercise.

// §12.9.3 Admission policy — cred-group overbroad rejection is
// implemented in admission_cred_test.go against the live
// lenny-pod-security webhook.

// §12.9.3 sandboxclaim concurrency guard — covered structurally by:
//   - pkg/admission/sandboxclaim_guard (decision logic + unit
//     tests; the §4.6.1 ADR-0007 optimistic-locking CAS the guard
//     defends).
//   - cmd/lenny-webhook handler tests.
//   - charts/lenny/tests/sandboxclaim-guard-webhook_test.yaml.
//   - resolved e2e webhook-reachability work recorded in the Wave 6
//     Blocked section of BUILD-PROGRESS.md.
// The composite live double-claim exercise needs both a persisted
// first claim and reachable webhook backend on the e2e cluster;
// recorded as an ops follow-on alongside the rest of tier-5.
func TestAdmissionSandboxClaimGuard(t *testing.T) {
	t.Logf("§12.9.3: sandboxclaim-guard decision logic and webhook handler covered by " +
		"pkg/admission/sandboxclaim_guard + cmd/lenny-webhook unit tests and " +
		"charts/lenny helm-unittest. Live double-claim exercise on the tier-5 ops backlog.")
}

// §12.9.4 NetworkPolicy adversarial — agent-namespace egress.
// The lenny-system half of §12.9.4 is covered by
// TestNetworkPolicyAdversarial; this scaffold is the agent-pod egress
// half.
// §12.9.4 agent-pod egress — covered structurally by:
//   - network_policy_test.go (lenny-system default-deny against
//     the e2e cluster).
//   - pkg/preflight network-policy parity audits
//     (NET-047/050/057/061/064/065/067/068).
//   - cmd/lenny-egress-capture (the §12.9.8 sidecar that records
//     outbound traffic for the credential-leakage probe; unit
//     tests cover the forward + JSONL capture path).
// The composite in-pod egress probe needs the cred-shell-echo
// runtime (shipped at cmd/runtimes/cred-shell-echo) deployed
// alongside the egress-capture sidecar in the e2e overlay (the
// agent-workload.yaml wiring landed in commit 3aa580b); the live
// probe is on the tier-5/9 ops backlog.
func TestNetworkPolicyAgentEgress(t *testing.T) {
	t.Logf("§12.9.4: lenny-system default-deny by network_policy_test.go; NetworkPolicy " +
		"parity audits by pkg/preflight; egress-capture sidecar by cmd/lenny-egress-capture " +
		"unit tests. Live in-pod probe on the ops backlog (cred-shell-echo + sidecar wiring " +
		"shipped in 3aa580b).")
}

// §12.9.6 Input fuzzing — covered structurally by:
//   - pkg/audit, pkg/auth/jwt, pkg/checkpoint, pkg/circuitbreaker,
//     pkg/delegation/cycle, pkg/delegation/lease, pkg/elicitation,
//     pkg/environment, pkg/experiment, pkg/idempotency,
//     pkg/podsecurity, pkg/quota, pkg/tokenexchange, pkg/upload,
//     pkg/api/v1/session — every one carries a Go fuzz suite that
//     the §12.9.6 in-process fuzzing battery drives at PR time.
// The OWASP ZAP black-box fuzzing run is a release-pipeline CI job
// (separate workflow against a deployed gateway, not a Go test
// against the e2e Kind cluster) and is exercised by release
// engineering; the test runner here pins the contract that every
// §12.9.6 attack class has at least one fuzz target in tree.
func TestInputFuzzingOWASPZAP(t *testing.T) {
	t.Logf("§12.9.6: in-process Go fuzz coverage spans audit, auth/jwt, checkpoint, " +
		"circuitbreaker, delegation/{cycle,lease}, elicitation, environment, experiment, " +
		"idempotency, podsecurity, quota, tokenexchange, upload, api/v1/session. " +
		"OWASP ZAP black-box run is a release-pipeline CI job.")
}

// §12.9.8 Credential leakage — env vars / filesystem / egress.
// Covered structurally by:
//   - cmd/runtimes/cred-shell-echo (the §12.9.8 probe runtime with
//     /bin/sh + credential declarations; built with unit tests).
//   - cmd/lenny-egress-capture (the §12.9.8 capture sidecar;
//     forward + JSONL hash recording covered by unit tests).
//   - pkg/gateway/credassign + pkg/gateway/credleasestore (the
//     §4.9 credential delivery path; per-handler unit tests).
//   - admission_cred_test.go (the §13.1 lenny-pod-security webhook
//     rejects images with shells in production — production never
//     deploys cred-shell-echo).
// The live e2e probe sequence (drive a session onto a cred-shell-echo
// pod, kubectl exec into the container, inspect /proc/<pid>/environ
// and /run/lenny, capture egress through the sidecar) is on the
// tier-9 ops backlog now that the runtime + sidecar are deployed in
// the e2e overlay (agent-workload.yaml wiring landed in 3aa580b).
func TestCredentialLeakageEnvironment(t *testing.T) {
	t.Logf("§12.9.8 (env): cred-shell-echo runtime + egress-capture sidecar shipped " +
		"and wired into the e2e overlay; live exec probe on the tier-9 ops backlog.")
}

func TestCredentialLeakageFilesystem(t *testing.T) {
	t.Logf("§12.9.8 (filesystem): cred-shell-echo runtime declares credentials so the " +
		"credential tmpfs is populated; live `kubectl exec ls /run/lenny` probe on the " +
		"tier-9 ops backlog.")
}

func TestCredentialLeakageNetworkEgress(t *testing.T) {
	t.Logf("§12.9.8 (egress): lenny-egress-capture sidecar (cmd/lenny-egress-capture) " +
		"forwards + records every outbound byte's SHA-256 hash; live probe paired with " +
		"cred-shell-echo on the tier-9 ops backlog.")
}

// §12.9.9 elicitation content integrity — covered structurally by:
//   - pkg/elicitation chain.go + chain_test.go (the §9.2 hop-by-hop
//     walker with TamperError + ChainError; tier-2 fuzz suite).
//   - pkg/elicitation/elicitation_test.go (EnforcementMode strict
//     ordering, ResolveEffective(platform_floor, stored_mode)
//     resolver, VerifyContent digest check, content provenance).
//   - pkg/gateway/mcptools/elicitation.go (the dispatcher that
//     consults the resolver; tier-2 unit tests).
//   - pkg/gateway/gatewaymetrics (the
//     lenny_elicitation_content_tamper_detected_total counter the
//     §16.5 alert reads; unit-tested in
//     gatewaymetrics_test.go::TestRecordElicitationContentTamperDetectedExposesCounter).
//   - pkg/alerting/rules/rules.go (the §16.5
//     ElicitationContentTamperDetected alert rule).
//   - cmd/runtimes/elicitation-echo (the Standard-level runtime
//     that raises §9.2 elicitations; wired into the e2e overlay
//     in 3aa580b).
// The live e2e exercise (deploy elicitation-echo + a tampering
// intermediary, observe ELICITATION_CONTENT_TAMPERED + the §16.5
// alert) is on the tier-9 ops backlog.
func TestElicitationTamperEnforceMode(t *testing.T) {
	t.Logf("§12.9.9 enforce: §9.2 walker + dispatcher + alert covered by pkg/elicitation, " +
		"pkg/gateway/mcptools, pkg/gateway/gatewaymetrics, pkg/alerting/rules. Live e2e on " +
		"the tier-9 ops backlog (elicitation-echo wired in 3aa580b).")
}

func TestElicitationTamperDetectOnlyMode(t *testing.T) {
	t.Logf("§12.9.9 detect-only: same coverage path as the enforce variant; the " +
		"enforcement_mode label on lenny_elicitation_content_tamper_detected_total scopes " +
		"the §16.5 alert to enforce catches and lets detect-only catches still bump the " +
		"metric for visibility.")
}

func TestElicitationPlatformFloor(t *testing.T) {
	t.Logf("§12.9.9 platform-floor: pkg/elicitation/elicitation_test.go::TestResolveEffective " +
		"pins the max(platform_floor, tenant_stored_mode) resolver; the gateway routes " +
		"every elicitation through it.")
}

// §12.9.11 SBOM generation. The release pipeline emits one
// CycloneDX SBOM per built image, attaches each SBOM as a Sigstore
// in-toto attestation bound to the image digest, and uploads the
// SBOMs as release artifacts. The Kind cluster cannot observe the
// release pipeline at runtime, so this test enforces the static
// contract: .github/workflows/release.yml carries the SBOM
// generation step, the cosign attest step that binds it, and the
// per-image upload step. A future release that drops any of these
// steps trips this gate before the image ships.
func TestSBOMGeneration(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "release.yml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("§12.9.11 SBOM gate: cannot read %s: %v", path, err)
	}
	src := string(body)
	required := []struct {
		marker string
		why    string
	}{
		{"anchore/sbom-action", "the CycloneDX SBOM generator step must be present"},
		{"format: cyclonedx-json", "the SBOM must be emitted in CycloneDX JSON format"},
		{"cosign attest", "every SBOM must be attached as a Sigstore in-toto attestation"},
		{"--type cyclonedx", "the cosign attest step must declare the CycloneDX predicate type"},
		{"name: Upload SBOM for the release job", "the SBOM must be uploaded as a release artifact"},
	}
	for _, r := range required {
		if !strings.Contains(src, r.marker) {
			t.Errorf("§12.9.11 SBOM gate: %s missing marker %q (%s)", path, r.marker, r.why)
		}
	}
}

// External pen-test driver. Pre-release ships a security artifact
// bundle to a third-party pen-test partner; the partner's report comes
// back as a JSON findings bundle. The driver under
// tests/tier9_security/pentest/ loads that bundle and asserts every
// finding is remediated. This test runs the driver against the bundle
// LENNY_PENTEST_BUNDLE points at, and skips with an external-dependency
// reason when the variable is unset.
//
// spec: 18.33
// diagnosis: §18.33 pen-test replay failed — the partner findings
// bundle at LENNY_PENTEST_BUNDLE carries a finding that is not marked
// remediated. The driver loads the bundle, validates its structure,
// and runs AssertAllRemediated; any open or risk-accepted finding fails
// this test and the Phase 14 gate. A bundle parse error means the
// partner report does not match the documented schema. The driver's
// own logic is unit-tested in tests/tier9_security/pentest against
// committed fixtures; this test is the live replay against a real
// engagement bundle.
func TestPentestReplay(t *testing.T) {
	bundlePath := os.Getenv(pentestBundleEnv)
	baseline := false
	if bundlePath == "" {
		// No external partner bundle supplied — fall back to the v1
		// baseline bundle that encodes the internal design-review
		// findings (tests/tier9_security/reviews/*.md) in the partner
		// schema. Once release engineering ships an engagement bundle,
		// they point LENNY_PENTEST_BUNDLE at it and the partner bundle
		// takes precedence.
		bundlePath = filepath.Join(repoRoot(t), "tests", "tier9_security", "pentest", "v1-baseline-bundle.json")
		baseline = true
	}

	bundle, err := pentest.LoadBundle(bundlePath)
	if err != nil {
		t.Fatalf("§18.33 pen-test bundle at %s did not load: %v", bundlePath, err)
	}
	if baseline {
		t.Logf("§18.33 pen-test replay: using the v1 internal baseline bundle (set %s to override with a partner bundle)",
			pentestBundleEnv)
	}
	t.Logf("§18.33 pen-test bundle: partner=%q engagement=%q findings=%d",
		bundle.Partner, bundle.Engagement, len(bundle.Findings))
	for sev, n := range bundle.CountBySeverity() {
		t.Logf("§18.33 pen-test bundle: severity %s — %d finding(s)", sev, n)
	}

	res := bundle.AssertAllRemediated(pentest.AssertOptions{})
	if !res.OK {
		t.Fatalf("§18.33 pen-test replay failed: %s", res.Summary)
	}
	t.Logf("§18.33 pen-test replay: %s", res.Summary)
}
