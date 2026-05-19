// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test scaffolds. Each test here corresponds to a
// TESTING.md-named security check whose subject is infrastructure the
// e2e Kind cluster does not provide: a live gateway session driven onto
// an agent pod, an egress-capture harness, a runtime sandbox (gVisor,
// Kata), a TLS-configured data store, a packaged fuzzing driver, or an
// external pen-test partner. Each calls t.Skip with a diagnosis naming
// the spec section and the exact missing infrastructure.
//
// The e2e install runs a real agent-pod workload in the lenny-agents
// namespace (the two §4.7 deployment-model warm pools install.sh
// applies from agent-workload.yaml). The warm pods reach the idle phase
// but no live session is driven onto them, so security checks whose
// subject is an active session, an egress flow, or an elicitation
// exchange remain scaffolded below.
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
	"testing"

	"github.com/lennylabs/lenny/tests/tier9_security/pentest"
)

// pentestBundleEnv is the environment variable the §18.33 pen-test
// replay reads. It points at the JSON findings bundle a third-party
// pen-test partner delivers at release time (the format is documented
// on pentest.Bundle). When the variable is unset, TestPentestReplay
// skips with an external-dependency reason: a partner bundle is a
// genuine external artifact, not infrastructure this repository can
// stand up.
const pentestBundleEnv = "LENNY_PENTEST_BUNDLE"

// §12.9.2 TLS enforcement (plaintext rejection on the data-store
// links). The mTLS-PKI half of §12.9.2 is covered by tls_test.go; this
// scaffold is the plaintext-rejection half against the data-store
// listeners.
func TestTLSPlaintextRejection(t *testing.T) {
	t.Skip("not implemented: §12.9.2 plaintext-connection rejection — the production contract is that " +
		"Postgres, PgBouncer, Redis, MinIO, OTLP, and the gateway-to-token-service / gateway-to-lenny-ops " +
		"gRPC links reject plaintext. The e2e Kind cluster runs its in-cluster data stores plaintext BY " +
		"DESIGN (datastores.yaml + e2e-values.yaml: Postgres sslmode=disable, redis:// without TLS, MinIO " +
		"useSSL:false) so the gateway can reach them without a PKI; there is no TLS-only listener to probe " +
		"for a plaintext refusal. Exercising this needs a TLS-configured store deployment, which the e2e " +
		"overlay deliberately does not provide. The §10.3 mTLS PKI readiness is covered by " +
		"TestMTLSCertificatesReady")
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

// §12.9.3 Admission policy — sandboxclaim concurrency guard.
func TestAdmissionSandboxClaimGuard(t *testing.T) {
	t.Skip("not implemented: §12.9.3 lenny-sandboxclaim-guard — the §4.6.1 concurrency rule rejects a " +
		"second SandboxClaim CREATE for a Sandbox that already has a non-terminal claim, so exercising it " +
		"needs a persisted first claim against a live Sandbox. The e2e cluster runs real Sandboxes, but " +
		"the lenny-sandboxclaim-guard webhook backend is not reachable from the API server on this " +
		"install: a CREATE against sandboxclaims fails with `failed calling webhook`/context-deadline " +
		"rather than reaching the guard. Exercising the rule needs the webhook's API-server connectivity " +
		"repaired and a claim-seeding harness")
}

// §12.9.4 NetworkPolicy adversarial — agent-namespace egress.
// The lenny-system half of §12.9.4 is covered by
// TestNetworkPolicyAdversarial; this scaffold is the agent-pod egress
// half.
func TestNetworkPolicyAgentEgress(t *testing.T) {
	t.Skip("not implemented: §12.9.4 agent-pod egress probes — exercising forbidden agent-pod egress " +
		"(arbitrary internet, kube-system CoreDNS, the LLM proxy port from a non-proxy pool) needs a " +
		"connectivity-probe step run from inside a lenny-agents pod. The e2e warm-pool pods run an echo " +
		"runtime image with no shell or curl, and the agent-workload overlay deploys no egress-probe " +
		"sidecar, so there is no in-namespace vantage point to issue the probes from. The lenny-system " +
		"default-deny enforcement is covered by TestNetworkPolicyAdversarial")
}

// §12.9.6 Input fuzzing. OWASP ZAP runs against the REST and MCP
// surfaces with the project's policy. Fixed seed for reproducibility.
func TestInputFuzzingOWASPZAP(t *testing.T) {
	t.Skip("not implemented: §12.9.6 OWASP ZAP fuzzing — this is not a live-cluster behaviour test. It " +
		"requires a packaged ZAP automation-framework run (the ZAP container, the project ZAP policy " +
		"file, and the fixed-seed configuration) driven as a separate CI job against the gateway's REST " +
		"and MCP surfaces; it cannot be expressed as a Go test against the Kind cluster")
}

// §12.9.8 Credential leakage — environment variables.
func TestCredentialLeakageEnvironment(t *testing.T) {
	t.Skip("not implemented: §12.9.8 credential leakage / env vars — inspecting an agent process's " +
		"/proc/<pid>/environ for leaked upstream credentials needs a live gateway session bound onto an " +
		"agent pod so the credential-delivery path has run. The e2e warm-pool pods reach the idle phase " +
		"but no session is driven onto them, and the distroless runtime image has no shell to read " +
		"/proc from. Exercising this needs the gateway session drive plus an in-pod inspection step")
}

// §12.9.8 Credential leakage — filesystem.
func TestCredentialLeakageFilesystem(t *testing.T) {
	t.Skip("not implemented: §12.9.8 credential leakage / filesystem — asserting the documented 0440 " +
		"group-owned /run/lenny/credentials.json needs a live gateway session bound onto an agent pod " +
		"so the credential file has been materialized. The e2e warm-pool pods reach the idle phase with " +
		"no session, so the credential tmpfs is not populated, and the distroless runtime image has no " +
		"shell to stat the file. Exercising this needs the gateway session drive plus an in-pod " +
		"file-mode inspection step")
}

// §12.9.8 Credential leakage — network egress.
func TestCredentialLeakageNetworkEgress(t *testing.T) {
	t.Skip("not implemented: §12.9.8 credential leakage / network egress — inspecting agent LLM-call " +
		"traffic for credential material needs an egress-capture harness on the agent-pod network path " +
		"and a live session issuing LLM calls. The e2e overlay deploys no traffic-capture sidecar or " +
		"proxy, and the warm-pool pods run no session. Exercising this needs the capture harness and a " +
		"gateway session driven onto an agent pod")
}

// §12.9.9 Elicitation content integrity — enforce mode.
func TestElicitationTamperEnforceMode(t *testing.T) {
	t.Skip("not implemented: §12.9.9 elicitation tamper / enforce mode — observing the SHA-256 " +
		"integrity check reject a tampered payload with ELICITATION_CONTENT_TAMPERED needs the §9.2 " +
		"elicitation chain driven end to end: a seeded session and an intermediate agent that replays a " +
		"tampered payload. The e2e cluster runs warm agent pods but drives no session and no elicitation " +
		"exchange, so the elicitation chain is never entered")
}

// §12.9.9 Elicitation content integrity — detect-only mode.
func TestElicitationTamperDetectOnlyMode(t *testing.T) {
	t.Skip("not implemented: §12.9.9 elicitation tamper / detect-only mode — asserting the " +
		"ElicitationContentIntegrityPermissiveTamper alert on a tampered payload needs the elicitation " +
		"chain driven with a tampering intermediary plus the alerting pipeline wired. The e2e cluster " +
		"runs warm agent pods but drives no session and no elicitation exchange, and the e2e overlay " +
		"deploys no alerting pipeline to assert the alert against")
}

// §12.9.9 Elicitation content integrity — platform floor.
func TestElicitationPlatformFloor(t *testing.T) {
	t.Skip("not implemented: §12.9.9 elicitation platform floor — exercising the " +
		"max(platform_floor, tenant_stored_mode) resolver and the clamp-event emission path needs a " +
		"per-tenant elicitation flow driven end to end. The e2e cluster runs warm agent pods but drives " +
		"no session and no elicitation exchange, so the resolver is never reached on a live request")
}

// §12.9.11 SBOM generation. Pre-release only.
func TestSBOMGeneration(t *testing.T) {
	t.Skip("not implemented: §12.9.11 SBOM — this is not a live-cluster behaviour test. SBOM generation " +
		"is a release-pipeline build step (the SBOM generator, artifact storage, and the per-image " +
		"attestation run at image-build time); there is nothing on a running Kind cluster to assert it " +
		"against")
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
	if bundlePath == "" {
		// A third-party pen-test partner's findings bundle is a genuine
		// external dependency: the platform cannot produce it, and there
		// is no engagement artifact checked into this repository. The
		// driver's parsing and assertion logic is covered by the unit
		// tests in tests/tier9_security/pentest against committed
		// fixtures; this skip is for the live-engagement replay only.
		t.Skipf("not-yet-applicable: phase-14: external pen-test bundle absent — set %s to the "+
			"path of the partner's JSON findings bundle to run the replay. The driver itself is "+
			"unit-tested in tests/tier9_security/pentest; a real engagement bundle is a third-party "+
			"deliverable this repository does not vendor.", pentestBundleEnv)
	}

	bundle, err := pentest.LoadBundle(bundlePath)
	if err != nil {
		t.Fatalf("§18.33 pen-test bundle at %s=%s did not load: %v", pentestBundleEnv, bundlePath, err)
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
