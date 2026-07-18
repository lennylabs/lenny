// SPDX-License-Identifier: MIT

// Tier-11 spec/code-consistency checks for the §4.9 cross-tenant
// credential-delivery controls. The §4.9 field-locality wording, the
// spec/18 build-sequence bullet, the lenny-direct-mode-isolation webhook
// chart resource scope, and the guard package doc must all agree that
// the credential-delivery combination fields (deliveryMode,
// isolationProfile, spiffeBinding) are authored on the SandboxTemplate
// pool definition, and that a CredentialPool is a Postgres-backed
// admin-API object a ValidatingAdmissionWebhook cannot intercept and
// that carries no spiffeBinding. These invariants have no existing
// tier-11 coverage. No external infrastructure required.

package tier11_docs_test

import (
	"strings"
	"testing"
)

// section49Text returns the text of §4.9 (Credential Leasing Service) of
// spec/04_system-components.md, from its "### 4.9 Credential Leasing
// Service" heading to the end of the file. §4.9 is the final top-level
// subsection of the file, so there is no following "### " heading to
// bound it. §4.9.2 (the credential audit-event catalog) is a subsection
// inside these bounds, so the audit-event row is included.
func section49Text(t *testing.T, root string) string {
	t.Helper()
	content := readRepoFile(t, root, "spec/04_system-components.md")
	const startMark = "### 4.9 Credential Leasing Service"
	start := strings.Index(content, startMark)
	if start < 0 {
		t.Fatalf("spec/04: §4.9 heading %q not found", startMark)
	}
	return content[start:]
}

// TestSection49WebhookScopesSandboxTemplateOnly asserts the two §4.9
// layer-2 ValidatingAdmissionWebhook sentences (the direct + standard
// rejection and the proxy + spiffeBinding: disabled rejection) name
// SandboxTemplate resources only, never a CredentialPool. A
// CredentialPool is a Postgres-backed admin-API object the chart-scoped
// (sandboxtemplates) webhook cannot intercept, so naming it as a
// webhook-rejected carrier is a spec-vs-code contradiction.
//
// diagnosis: A failure means a §4.9 layer-2 sentence again names a
// CredentialPool as a resource the lenny-direct-mode-isolation webhook
// rejects, contradicting the shipped chart rule scoped to
// sandboxtemplates and the guard package doc's reasoning that
// SandboxTemplate is the only admitted carrier.
//
// spec: 4.9 (field-locality of the two layer-2 webhook rejections)
func TestSection49WebhookScopesSandboxTemplateOnly(t *testing.T) {
	root := repoRoot(t)
	text := section49Text(t, root)

	// The direct + standard layer-2 sentence rejects SandboxTemplate only.
	const directStandardLayer2 = "rejects `SandboxTemplate` resources carrying this combination"
	if !strings.Contains(text, directStandardLayer2) {
		t.Errorf("§4.9 is missing the corrected direct + standard layer-2 sentence %q", directStandardLayer2)
	}
	// The proxy + spiffeBinding: disabled layer-2 sentence rejects
	// SandboxTemplate only.
	const proxySpiffeLayer2 = "reject `SandboxTemplate` resources carrying `deliveryMode: proxy` + `spiffeBinding: disabled`"
	if !strings.Contains(text, proxySpiffeLayer2) {
		t.Errorf("§4.9 is missing the corrected proxy + spiffeBinding layer-2 sentence %q", proxySpiffeLayer2)
	}
	// The justifying clause explaining why a CredentialPool is not a
	// webhook-intercepted carrier is present.
	const justification = "A `CredentialPool` is a Postgres-backed admin-API object that carries no `isolationProfile`"
	if !strings.Contains(text, justification) {
		t.Errorf("§4.9 is missing the justification that a CredentialPool is not a webhook-interceptable resource: %q", justification)
	}
	// Neither layer-2 sentence names a CredentialPool as a rejected
	// resource. The old wording paired the two as "SandboxTemplate and
	// CredentialPool resources"; the preflight sentence used "registered
	// CredentialPool resources". Neither "`CredentialPool` resources"
	// construction may reappear anywhere in §4.9.
	if strings.Contains(text, "`CredentialPool` resources") {
		t.Errorf("§4.9 names `CredentialPool` resources as a webhook/preflight carrier; the webhook is scoped to sandboxtemplates and a CredentialPool cannot be intercepted")
	}
	if strings.Contains(text, "and `CredentialPool` resources") {
		t.Errorf("§4.9 restores the old \"SandboxTemplate and CredentialPool resources\" layer-2 wording")
	}
}

// TestSection49PreflightScansSandboxTemplates asserts the §4.9
// lenny-preflight sentence scans SandboxTemplate pool definitions for
// deliveryMode: proxy + spiffeBinding: disabled, not registered
// CredentialPool resources. A CredentialPool is not a Kubernetes
// resource and carries no spiffeBinding; the SandboxTemplate CRD carries
// both deliveryMode and spiffeBinding, so the scan targets it.
//
// diagnosis: A failure means the §4.9 preflight sentence again scans
// CredentialPool resources for spiffeBinding, a field a CredentialPool
// does not carry, so the described preflight scan cannot be implemented
// against the resource named.
//
// spec: 4.9 (lenny-preflight scan target and combination)
func TestSection49PreflightScansSandboxTemplates(t *testing.T) {
	root := repoRoot(t)
	text := section49Text(t, root)

	const preflightSentence = "scans all `SandboxTemplate` pool definitions at install and upgrade time and fails when any pool in a multi-tenant deployment carries `deliveryMode: proxy` + `spiffeBinding: disabled`"
	if !strings.Contains(text, preflightSentence) {
		t.Errorf("§4.9 is missing the corrected preflight scan sentence %q", preflightSentence)
	}
	if strings.Contains(text, "scans all registered `CredentialPool`") {
		t.Errorf("§4.9 preflight sentence again scans registered CredentialPool resources for spiffeBinding, which a CredentialPool does not carry")
	}
}

// TestSection49NoCredentialPoolSpiffeBindingCarrier asserts no §4.9 text,
// including the §4.9.2 audit-event catalog row, names a CredentialPool as
// the carrier of `spiffeBinding: disabled`. Every occurrence of
// `spiffeBinding: disabled` in §4.9 must be attributed to a
// SandboxTemplate pool definition / warm-pool admin resource, never a
// CredentialPool. The check inspects the text immediately preceding each
// occurrence: the legitimate §4.9 mentions of CredentialPool (the
// Postgres-backed-admin-object justification, and the session-start gate
// that reads each resolved CredentialPool's effective deliveryMode) do
// not attribute the disabled spiffeBinding value to a CredentialPool.
//
// diagnosis: A failure means a §4.9 sentence (or the §4.9.2 audit row)
// again describes a CredentialPool carrying spiffeBinding: disabled, a
// field the CredentialPool admin resource does not have, so the spec
// names a data-model relationship the code cannot express.
//
// spec: 4.9 (no CredentialPool is a spiffeBinding carrier), 4.9.2 (audit
// row carrier)
func TestSection49NoCredentialPoolSpiffeBindingCarrier(t *testing.T) {
	root := repoRoot(t)
	text := section49Text(t, root)

	const marker = "spiffeBinding: disabled"
	// The carrier noun precedes the value; a 100-char lookback captures
	// the "<Resource> ... carrying/with deliveryMode: proxy +
	// spiffeBinding: disabled" clause without reaching an unrelated
	// earlier sentence.
	const lookback = 100
	found := false
	for i := 0; ; {
		idx := strings.Index(text[i:], marker)
		if idx < 0 {
			break
		}
		abs := i + idx
		found = true
		lo := abs - lookback
		if lo < 0 {
			lo = 0
		}
		window := text[lo:abs]
		if strings.Contains(window, "CredentialPool") {
			t.Errorf("§4.9 names a CredentialPool as the carrier of %q (context: %q)", marker, window)
		}
		i = abs + len(marker)
	}
	if !found {
		t.Fatalf("§4.9 contains no %q occurrence; the field-locality assertion has nothing to verify (spec restructured)", marker)
	}
}

// TestBuildSequenceOptInSingleTenantOnly asserts the spec/18
// lenny-direct-mode-isolation build-sequence bullet no longer states the
// proxy + spiffeBinding: disabled combination is rejected "unless
// `allowProxyModeSpiffeBindingDisabled` is set", which contradicted
// §4.9's rule that the opt-in cannot be set in multi-tenant mode. The
// corrected bullet qualifies the opt-in to single-tenant / development
// mode.
//
// diagnosis: A failure means spec/18 again states the multi-tenant
// rejection is skipped when the opt-in is set, contradicting §4.9's
// "the opt-in field cannot be set in multi-tenant mode".
//
// spec: 18 (build-sequence webhook bullet), 4.9 (multi-tenant opt-in
// rule)
func TestBuildSequenceOptInSingleTenantOnly(t *testing.T) {
	root := repoRoot(t)
	text := readRepoFile(t, root, "spec/18_build-sequence.md")

	if strings.Contains(text, "unless `allowProxyModeSpiffeBindingDisabled` is set") {
		t.Errorf("spec/18 again states the multi-tenant rejection is skipped when the opt-in is set, contradicting §4.9")
	}
	const corrected = "The `allowProxyModeSpiffeBindingDisabled` opt-in permits combination (b) only in single-tenant or development mode; it cannot be set in multi-tenant mode."
	if !strings.Contains(text, corrected) {
		t.Errorf("spec/18 is missing the corrected opt-in qualification %q", corrected)
	}
}

// TestWebhookChartAndGuardDocAgreeSandboxTemplateScope asserts the
// shipped lenny-direct-mode-isolation webhook chart rule is scoped to
// the sandboxtemplates resource only, and the guard package doc states
// SandboxTemplate is the only admitted carrier and the chart rule is
// scoped to sandboxtemplates. This pins the code side of the §4.9
// field-locality wording: the spec, the chart, and the guard doc all
// agree the webhook intercepts SandboxTemplate resources only.
//
// diagnosis: A failure means the webhook chart resource scope or the
// guard package doc drifted from the §4.9 wording that the webhook
// intercepts SandboxTemplate resources only: either the chart matched
// another resource (e.g. credentialpools, which the apiserver cannot
// route to a webhook) or the guard doc no longer states SandboxTemplate
// is the only admitted carrier.
//
// spec: 4.9 (webhook is scoped to SandboxTemplate), 13.2
func TestWebhookChartAndGuardDocAgreeSandboxTemplateScope(t *testing.T) {
	root := repoRoot(t)
	chart := readRepoFile(t, root, "charts/lenny/templates/admission-policies/direct-mode-isolation-webhook.yaml")
	guard := readRepoFile(t, root, "pkg/admission/direct_mode_isolation/guard.go")

	if !strings.Contains(chart, `resources: ["sandboxtemplates"]`) {
		t.Errorf("webhook chart is not scoped to the sandboxtemplates resource")
	}
	if strings.Contains(chart, "credentialpools") {
		t.Errorf("webhook chart matches credentialpools, which is not a Kubernetes resource a ValidatingAdmissionWebhook can intercept")
	}
	if !strings.Contains(guard, "the SandboxTemplate is the only admitted resource that can carry a") {
		t.Errorf("guard package doc no longer states SandboxTemplate is the only admitted carrier")
	}
	if !strings.Contains(guard, "Scoping the chart rule to sandboxtemplates") {
		t.Errorf("guard package doc no longer states the chart rule is scoped to sandboxtemplates")
	}
}

// TestFieldLocalityDriftDetection exercises the field-locality assertions
// against synthetic drifted text to prove each would fail against a spec
// that reintroduced the CredentialPool field-locality error, rather than
// merely line-covering the pass path.
//
// diagnosis: A failure here means the carrier and wording checks no
// longer detect the drift they are meant to catch, so the
// spec-consistency tests above would silently pass on a drifted spec.
//
// spec: 4.9, 18
func TestFieldLocalityDriftDetection(t *testing.T) {
	// (a) A layer-2 sentence that names "SandboxTemplate and
	// CredentialPool resources" must trip the "`CredentialPool`
	// resources" guard.
	drifted := "the webhook rejects `SandboxTemplate` and `CredentialPool` resources carrying `deliveryMode: proxy` + `spiffeBinding: disabled`"
	if !strings.Contains(drifted, "`CredentialPool` resources") {
		t.Errorf("drift detector missed a CredentialPool resources layer-2 wording")
	}

	// (b) The carrier lookback must flag a CredentialPool that carries
	// spiffeBinding: disabled and must not flag a SandboxTemplate carrier.
	const marker = "spiffeBinding: disabled"
	const lookback = 100
	carrierNamed := func(s string) bool {
		idx := strings.Index(s, marker)
		if idx < 0 {
			return false
		}
		lo := idx - lookback
		if lo < 0 {
			lo = 0
		}
		return strings.Contains(s[lo:idx], "CredentialPool")
	}
	bad := "registered or updated a `CredentialPool` with `deliveryMode: proxy` + `spiffeBinding: disabled`"
	if !carrierNamed(bad) {
		t.Errorf("carrier lookback missed a CredentialPool carrying spiffeBinding: disabled")
	}
	good := "a warm-pool admin resource whose `SandboxTemplate` pool definition carries `deliveryMode: proxy` + `spiffeBinding: disabled`"
	if carrierNamed(good) {
		t.Errorf("carrier lookback falsely flagged a SandboxTemplate carrier as a CredentialPool")
	}
	// The legitimate session-start gate sentence names a CredentialPool
	// as a deliveryMode carrier and the bound pod as the spiffeBinding
	// carrier; it uses bare `spiffeBinding` (no ": disabled" value) and
	// must not trip the marker-based check at all.
	gate := "the gateway evaluates each resolved `CredentialPool`'s effective `deliveryMode` against the bound pod's `isolationProfile` and `spiffeBinding`"
	if strings.Contains(gate, marker) {
		t.Errorf("session-start gate sentence unexpectedly contains %q; the carrier check would false-positive", marker)
	}

	// (c) The spec/18 opt-in drift: the old "unless ... is set" wording
	// must be caught.
	oldBullet := "rejects (b) when `tenancy.mode: multi`, unless `allowProxyModeSpiffeBindingDisabled` is set."
	if !strings.Contains(oldBullet, "unless `allowProxyModeSpiffeBindingDisabled` is set") {
		t.Errorf("drift detector missed the old spec/18 opt-in wording")
	}
}
