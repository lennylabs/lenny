// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test scaffolds. Each test here corresponds to a
// TESTING.md-named security check whose subject is infrastructure the
// dev-mode control-plane install does not provide: a running agent-pod
// workload, external data stores (Postgres, Redis, MinIO, KMS), a
// penetration-testing driver, or a runtime sandbox (gVisor, Kata).
// Each calls t.Skip with a diagnosis naming the spec section and the
// exact missing infrastructure.
//
// The security checks whose subject is a control-plane resource the
// dev-mode install actually runs are implemented against the live Kind
// cluster: see network_policy_test.go (§13.2 NetworkPolicy),
// tls_test.go (§10.3 mTLS PKI and webhook CA injection),
// admission_security_test.go (§13.1 pod-security bypass attempts), and
// image_signing_test.go (§5.2 cosign webhook gating).
//
// Naming follows TESTING.md §12.9.1 through §12.9.11.

package tier9_security_test

import "testing"

// §12.9.1 Tenant isolation (cross-store). A composed adversarial
// scenario: seed tenants A and B with rich state on every store, then
// for each store and each operation, attempt cross-tenant reads and
// writes through every code path. Every attempt must fail with the
// documented isolation error.
func TestTenantIsolationCrossStore(t *testing.T) {
	t.Skip("not implemented: §12.9.1 cross-store tenant isolation — requires every store implementation (Postgres, Redis, MinIO) backed by real data stores, which the dev-mode in-memory install does not provide, plus a seed helper for tenants A/B and the adversarial paths (REST, MCP, OpenAI Chat/Responses, admin, audit query, drift, lenny-ops)")
}

// §12.9.2 TLS enforcement (plaintext rejection on the data-store
// links). The mTLS-PKI half of §12.9.2 is covered by tls_test.go; this
// scaffold is the plaintext-rejection half against the external
// listeners.
func TestTLSPlaintextRejection(t *testing.T) {
	t.Skip("not implemented: §12.9.2 plaintext-connection rejection — requires the external data-store listeners (Postgres, PgBouncer, Redis, MinIO, OTLP) and the gateway-to-token-service / gateway-to-lenny-ops gRPC links carrying real traffic; the dev-mode install runs in-memory stores with no plaintext listener to probe. The §10.3 mTLS PKI readiness is covered by TestMTLSCertificatesReady")
}

// §12.9.3 Admission policy — fsGroup-missing rejection. Covered in part
// by admission_security_test.go (the host-sharing and capability
// vectors); the fsGroup vector needs the agent-pod template path.
func TestAdmissionPolicyFsGroupMissing(t *testing.T) {
	t.Skip("not implemented: §12.9.3 POD_SPEC_CRED_FSGROUP_MISSING — the lenny-pod-security webhook's fsGroup check fires on the agent-pod template the WarmPoolController generates; exercising it as an adversarial case needs a SandboxWarmPool reconcile producing a pod template, which the dev-mode control-plane-only install does not run. TestAdmissionSecurityBypassAttempts covers the host-sharing and capability §13.1 vectors")
}

// §12.9.3 Admission policy — cred-group overbroad rejection.
func TestAdmissionPolicyCredGroupOverbroad(t *testing.T) {
	t.Skip("not implemented: §12.9.3 POD_SPEC_CRED_GROUP_OVERBROAD — requires an agent pod whose non-adapter, non-agent container declares the lenny-cred-readers GID; building that case needs the WarmPoolController agent-pod template path and a running agent-pod workload absent from the dev-mode install")
}

// §12.9.3 Admission policy — ephemeral-container cred-UID rejection.
func TestAdmissionEphemeralContainerCredUIDForbidden(t *testing.T) {
	t.Skip("not implemented: §12.9.3 EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN — the lenny-ephemeral-container-cred-guard webhook intercepts UPDATE on pods/ephemeralcontainers; the rejection needs a live agent pod to attach an ephemeral container to, which the dev-mode control-plane-only install does not run")
}

// §12.9.3 Admission policy — label immutability on UPDATE.
func TestAdmissionLabelImmutability(t *testing.T) {
	t.Skip("not implemented: §12.9.3 lenny-label-immutability UPDATE guard — the label-mutation rejection fires on UPDATE of an existing agent pod carrying lenny.dev/managed; the dev-mode install runs no agent-pod workload to mutate. The webhook's configuration (failurePolicy, CREATE+UPDATE scope) is asserted by the tier-5 TestLabelImmutability")
}

// §12.9.3 Admission policy — sandboxclaim concurrency guard.
func TestAdmissionSandboxClaimGuard(t *testing.T) {
	t.Skip("not implemented: §12.9.3 lenny-sandboxclaim-guard — requires a concurrent SandboxClaim injector racing two claims for one warm pod slot, which needs a running warm pool with claimable slots absent from the dev-mode control-plane-only install")
}

// §12.9.4 NetworkPolicy adversarial — agent-namespace egress.
// The lenny-system half of §12.9.4 is covered by
// TestNetworkPolicyAdversarial; this scaffold is the agent-pod egress
// half.
func TestNetworkPolicyAgentEgress(t *testing.T) {
	t.Skip("not implemented: §12.9.4 agent-pod egress probes — requires a running agent pod in lenny-agents to attempt forbidden egress (arbitrary internet, kube-system CoreDNS, the LLM proxy port from a non-proxy pool); the dev-mode install schedules no agent-pod workload. The lenny-system default-deny enforcement is covered by TestNetworkPolicyAdversarial")
}

// §12.9.5 SSRF and callback validation. Callback URLs are tested with a
// range of adversarial inputs and rejected appropriately.
func TestSSRFCallbackValidation(t *testing.T) {
	t.Skip("not implemented: §12.9.5 SSRF callback validation — requires the gateway callback-URL validator reachable with a seeded session and the adversarial corpus (HTTP, IP literals, localhost, 169.254.169.254, DNS-rebinding, metadata hostnames, private IPs); driving it needs a session-creating client path against a fully-provisioned gateway")
}

// §12.9.6 Input fuzzing. OWASP ZAP runs against the REST and MCP
// surfaces with the project's policy. Fixed seed for reproducibility.
func TestInputFuzzingOWASPZAP(t *testing.T) {
	t.Skip("not implemented: §12.9.6 OWASP ZAP fuzzing — requires a packaged ZAP automation-framework run, the project ZAP policy, and a seeded gateway with REST and MCP surfaces serving real traffic")
}

// §12.9.7 RBAC. Every documented role's access is positively asserted;
// every escalation attempt fails.
func TestRBACRolePositiveAccess(t *testing.T) {
	t.Skip("not implemented: §12.9.7 RBAC positive assertions — requires the OIDC verifier wired to an identity provider, the role mapping, and every documented role exercised against gateway endpoints, none of which the dev-mode install provisions")
}

// §12.9.7 RBAC — escalation rejection.
func TestRBACEscalationDenied(t *testing.T) {
	t.Skip("not implemented: §12.9.7 RBAC escalation rejection — requires the gateway role-ceiling middleware reachable with authenticated principals at distinct role levels, which needs the OIDC verifier and a provisioned gateway")
}

// §12.9.8 Credential leakage — environment variables.
func TestCredentialLeakageEnvironment(t *testing.T) {
	t.Skip("not implemented: §12.9.8 credential leakage / env vars — requires a running agent pod with the lenny-cred-readers group convention so /proc/<pid>/environ can be inspected for leaked upstream credentials; the dev-mode install runs no agent-pod workload")
}

// §12.9.8 Credential leakage — filesystem.
func TestCredentialLeakageFilesystem(t *testing.T) {
	t.Skip("not implemented: §12.9.8 credential leakage / filesystem — requires a running agent pod with the documented 0440 group-owned /run/lenny/credentials.json so file modes and ownership can be inspected; the dev-mode install runs no agent-pod workload")
}

// §12.9.8 Credential leakage — network egress.
func TestCredentialLeakageNetworkEgress(t *testing.T) {
	t.Skip("not implemented: §12.9.8 credential leakage / network egress — requires an egress-capture harness and a running agent pod issuing LLM calls so the captured traffic can be inspected for credential material")
}

// §12.9.9 Elicitation content integrity — enforce mode.
func TestElicitationTamperEnforceMode(t *testing.T) {
	t.Skip("not implemented: §12.9.9 elicitation tamper / enforce mode — requires the §9.2 elicitation chain reachable with a seeded session so a tampered payload can be replayed and the SHA-256 integrity check rejecting it with ELICITATION_CONTENT_TAMPERED can be observed")
}

// §12.9.9 Elicitation content integrity — detect-only mode.
func TestElicitationTamperDetectOnlyMode(t *testing.T) {
	t.Skip("not implemented: §12.9.9 elicitation tamper / detect-only mode — requires the elicitation chain plus the alerting pipeline so the ElicitationContentIntegrityPermissiveTamper alert can be asserted on a tampered payload")
}

// §12.9.9 Elicitation content integrity — platform floor.
func TestElicitationPlatformFloor(t *testing.T) {
	t.Skip("not implemented: §12.9.9 elicitation platform floor — requires the max(platform_floor, tenant_stored_mode) resolver reachable with per-tenant stored modes and the clamp-event emission path, which needs a fully-provisioned gateway with tenant state")
}

// §12.9.10 Audit chain integrity — chain continuity.
func TestAuditChainContinuity(t *testing.T) {
	t.Skip("not implemented: §12.9.10 audit chain continuity — requires the §11.7 hash-chain implementation backed by a real audit ledger (Postgres) and the gap-detection alert wiring; the dev-mode in-memory install does not persist an audit chain to verify")
}

// §12.9.10 Audit chain integrity — sequence monotonicity.
func TestAuditSequenceMonotonicity(t *testing.T) {
	t.Skip("not implemented: §12.9.10 audit sequence monotonicity — requires the audit ledger backed by Postgres and a million-event generation harness so sequence-number monotonicity can be checked at scale")
}

// §12.9.11 SBOM generation. Pre-release only.
func TestSBOMGeneration(t *testing.T) {
	t.Skip("not implemented: §12.9.11 SBOM — requires the release-pipeline SBOM generator, artifact storage, and the per-image attestation; this is a pre-release build-pipeline check, not a live-cluster behaviour")
}

// External pen-test driver. Pre-release ships an artifact bundle to the
// external pen-test partner. The pen-test driver under
// tests/tier9_security/pentest/ is the harness for replaying the
// partner's findings against future builds.
func TestPentestReplay(t *testing.T) {
	t.Skip("not implemented: §12.9 external pen-test driver — requires a third-party pen-test runner replaying the partner's findings against a live production-shaped deployment")
}
