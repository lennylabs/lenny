// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test scaffolds. Each test here corresponds to a
// TESTING.md-named security check whose subject is infrastructure the
// e2e Kind cluster does not provide: a running agent-pod workload, a
// runtime sandbox (gVisor, Kata), a TLS-configured data store, a
// packaged fuzzing driver, or an external pen-test partner. Each calls
// t.Skip with a diagnosis naming the spec section and the exact
// missing infrastructure.
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

import "testing"

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

// §12.9.3 Admission policy — fsGroup-missing rejection. Covered in part
// by admission_security_test.go (the host-sharing and capability
// vectors); the fsGroup vector needs the agent-pod template path.
func TestAdmissionPolicyFsGroupMissing(t *testing.T) {
	t.Skip("not implemented: §12.9.3 POD_SPEC_CRED_FSGROUP_MISSING — the lenny-pod-security webhook's " +
		"fsGroup check fires on the agent-pod template the WarmPoolController generates; exercising it as " +
		"an adversarial case needs a SandboxWarmPool reconcile producing a pod template, which the warm-pod " +
		"runtime-container model (an open blocker) does not run on the e2e cluster. " +
		"TestAdmissionSecurityBypassAttempts covers the host-sharing and capability §13.1 vectors")
}

// §12.9.3 Admission policy — cred-group overbroad rejection.
func TestAdmissionPolicyCredGroupOverbroad(t *testing.T) {
	t.Skip("not implemented: §12.9.3 POD_SPEC_CRED_GROUP_OVERBROAD — requires an agent pod whose " +
		"non-adapter, non-agent container declares the lenny-cred-readers GID; building that case needs " +
		"the WarmPoolController agent-pod template path and a running agent-pod workload, which the " +
		"warm-pod runtime-container model (an open blocker) does not run on the e2e cluster")
}

// §12.9.3 Admission policy — ephemeral-container cred-UID rejection.
func TestAdmissionEphemeralContainerCredUIDForbidden(t *testing.T) {
	t.Skip("not implemented: §12.9.3 EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN — the " +
		"lenny-ephemeral-container-cred-guard webhook intercepts UPDATE on pods/ephemeralcontainers; the " +
		"rejection needs a live agent pod to attach an ephemeral container to, which the warm-pod " +
		"runtime-container model (an open blocker) does not run on the e2e cluster")
}

// §12.9.3 Admission policy — label immutability on UPDATE.
func TestAdmissionLabelImmutability(t *testing.T) {
	t.Skip("not implemented: §12.9.3 lenny-label-immutability UPDATE guard — the label-mutation rejection " +
		"fires on UPDATE of an existing agent pod carrying lenny.dev/managed; the e2e cluster runs no " +
		"agent-pod workload to mutate (the warm-pod runtime-container model is an open blocker). The " +
		"webhook's configuration (failurePolicy, CREATE+UPDATE scope) is asserted by the tier-5 " +
		"TestLabelImmutability")
}

// §12.9.3 Admission policy — sandboxclaim concurrency guard.
func TestAdmissionSandboxClaimGuard(t *testing.T) {
	t.Skip("not implemented: §12.9.3 lenny-sandboxclaim-guard — requires a concurrent SandboxClaim " +
		"injector racing two claims for one warm pod slot, which needs a running warm pool with claimable " +
		"slots; the warm-pod runtime-container model (an open blocker) does not run on the e2e cluster")
}

// §12.9.4 NetworkPolicy adversarial — agent-namespace egress.
// The lenny-system half of §12.9.4 is covered by
// TestNetworkPolicyAdversarial; this scaffold is the agent-pod egress
// half.
func TestNetworkPolicyAgentEgress(t *testing.T) {
	t.Skip("not implemented: §12.9.4 agent-pod egress probes — requires a running agent pod in " +
		"lenny-agents to attempt forbidden egress (arbitrary internet, kube-system CoreDNS, the LLM proxy " +
		"port from a non-proxy pool); the warm-pod runtime-container model (an open blocker) schedules no " +
		"agent-pod workload on the e2e cluster. The lenny-system default-deny enforcement is covered by " +
		"TestNetworkPolicyAdversarial")
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
	t.Skip("not implemented: §12.9.8 credential leakage / env vars — requires a running agent pod with " +
		"the lenny-cred-readers group convention so /proc/<pid>/environ can be inspected for leaked " +
		"upstream credentials; the warm-pod runtime-container model (an open blocker) runs no agent-pod " +
		"workload on the e2e cluster")
}

// §12.9.8 Credential leakage — filesystem.
func TestCredentialLeakageFilesystem(t *testing.T) {
	t.Skip("not implemented: §12.9.8 credential leakage / filesystem — requires a running agent pod with " +
		"the documented 0440 group-owned /run/lenny/credentials.json so file modes and ownership can be " +
		"inspected; the warm-pod runtime-container model (an open blocker) runs no agent-pod workload on " +
		"the e2e cluster")
}

// §12.9.8 Credential leakage — network egress.
func TestCredentialLeakageNetworkEgress(t *testing.T) {
	t.Skip("not implemented: §12.9.8 credential leakage / network egress — requires an egress-capture " +
		"harness and a running agent pod issuing LLM calls so the captured traffic can be inspected for " +
		"credential material; the warm-pod runtime-container model (an open blocker) runs no agent-pod " +
		"workload on the e2e cluster")
}

// §12.9.9 Elicitation content integrity — enforce mode.
func TestElicitationTamperEnforceMode(t *testing.T) {
	t.Skip("not implemented: §12.9.9 elicitation tamper / enforce mode — requires the §9.2 elicitation " +
		"chain reachable with a seeded session and a tampering intermediate agent pod so a tampered " +
		"payload can be replayed and the SHA-256 integrity check rejecting it with " +
		"ELICITATION_CONTENT_TAMPERED can be observed; the warm-pod runtime-container model (an open " +
		"blocker) runs no agent-pod workload on the e2e cluster")
}

// §12.9.9 Elicitation content integrity — detect-only mode.
func TestElicitationTamperDetectOnlyMode(t *testing.T) {
	t.Skip("not implemented: §12.9.9 elicitation tamper / detect-only mode — requires the elicitation " +
		"chain with an agent-pod tampering intermediary plus the alerting pipeline so the " +
		"ElicitationContentIntegrityPermissiveTamper alert can be asserted on a tampered payload; the " +
		"warm-pod runtime-container model (an open blocker) runs no agent-pod workload on the e2e cluster")
}

// §12.9.9 Elicitation content integrity — platform floor.
func TestElicitationPlatformFloor(t *testing.T) {
	t.Skip("not implemented: §12.9.9 elicitation platform floor — requires the " +
		"max(platform_floor, tenant_stored_mode) resolver exercised end to end with an agent-pod " +
		"elicitation flow per tenant and the clamp-event emission path; the warm-pod runtime-container " +
		"model (an open blocker) runs no agent-pod workload on the e2e cluster")
}

// §12.9.11 SBOM generation. Pre-release only.
func TestSBOMGeneration(t *testing.T) {
	t.Skip("not implemented: §12.9.11 SBOM — this is not a live-cluster behaviour test. SBOM generation " +
		"is a release-pipeline build step (the SBOM generator, artifact storage, and the per-image " +
		"attestation run at image-build time); there is nothing on a running Kind cluster to assert it " +
		"against")
}

// External pen-test driver. Pre-release ships an artifact bundle to the
// external pen-test partner. The pen-test driver under
// tests/tier9_security/pentest/ is the harness for replaying the
// partner's findings against future builds.
func TestPentestReplay(t *testing.T) {
	t.Skip("not implemented: §12.9 external pen-test driver — this is not a live-cluster behaviour test. " +
		"It requires a third-party pen-test partner's findings bundle and the external pen-test runner " +
		"that replays those findings against a production-shaped deployment; there is no partner bundle " +
		"and no replay runner to drive from a Go test on the Kind cluster")
}
