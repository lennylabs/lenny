// SPDX-License-Identifier: MIT

package runtime

import (
	"encoding/json"
	"testing"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// TestApplyCRDFields_spec_5_1 verifies the §5.1 CRD-owned field subset
// maps onto the registry runtime: type, image, integration level,
// execution mode, isolation profile, allowed resource classes,
// supported providers, the credential-capabilities block, and the
// object's domain labels.
func TestApplyCRDFields_spec_5_1(t *testing.T) {
	rt := &lennyv1.Runtime{
		Spec: lennyv1.RuntimeSpec{
			Type:                   "agent",
			Image:                  "registry.example.com/langgraph@sha256:" + hex64,
			IntegrationLevel:       "full",
			ExecutionMode:          "task",
			IsolationProfile:       "microvm",
			DeploymentModel:        "embedded",
			AllowedResourceClasses: []string{"small", "medium"},
			SupportedProviders:     []string{"anthropic_direct", "aws_bedrock"},
			CredentialCapabilities: &lennyv1.CredentialCapabilities{
				HotRotation:  true,
				ProxyDialect: []string{"anthropic", "openai"},
			},
		},
	}
	rt.Name = "langgraph-runtime"
	rt.Labels = map[string]string{"team": "platform"}

	var dst runtimestore.Runtime
	applyCRDFields(&dst, rt)

	if dst.Name != "langgraph-runtime" {
		t.Errorf("name = %q, want langgraph-runtime", dst.Name)
	}
	if dst.Type != runtimestore.TypeAgent {
		t.Errorf("type = %q, want agent", dst.Type)
	}
	if dst.Image != rt.Spec.Image {
		t.Errorf("image = %q, want %q", dst.Image, rt.Spec.Image)
	}
	if dst.IntegrationLevel != runtimestore.IntegrationLevelFull {
		t.Errorf("integrationLevel = %q, want full", dst.IntegrationLevel)
	}
	if dst.ExecutionMode != runtimestore.ExecutionModeTask {
		t.Errorf("executionMode = %q, want task", dst.ExecutionMode)
	}
	if dst.IsolationProfile != isolation.ProfileMicrovm {
		t.Errorf("isolationProfile = %q, want microvm", dst.IsolationProfile)
	}
	if len(dst.AllowedResourceClasses) != 2 || dst.AllowedResourceClasses[0] != "small" {
		t.Errorf("allowedResourceClasses = %v", dst.AllowedResourceClasses)
	}
	if len(dst.SupportedProviders) != 2 || dst.SupportedProviders[1] != "aws_bedrock" {
		t.Errorf("supportedProviders = %v", dst.SupportedProviders)
	}
	if dst.CredentialCapabilities == nil || !dst.CredentialCapabilities.HotRotation {
		t.Fatalf("credentialCapabilities = %+v, want hotRotation true", dst.CredentialCapabilities)
	}
	if len(dst.CredentialCapabilities.ProxyDialect) != 2 {
		t.Errorf("proxyDialect = %v", dst.CredentialCapabilities.ProxyDialect)
	}
	if dst.Labels["team"] != "platform" {
		t.Errorf("labels = %v, want team=platform", dst.Labels)
	}
}

// TestApplyCRDFields_PreservesNonCRDFields_spec_5_1 verifies the update
// path leaves §5.1 fields the CRD does not model (the agent interface,
// the description text, minPlatformVersion) intact, so an admin-API
// configuration layered on top of the CRD base survives a re-reconcile.
// spec: §5.1 — F-26.9.2 added capabilities/runtimeOptionsSchemaRef/
// delegationPolicyRef to the CRD; those are now CRD-canonical and
// therefore not preserved here.
func TestApplyCRDFields_PreservesNonCRDFields_spec_5_1(t *testing.T) {
	dst := runtimestore.Runtime{
		Name:               "scanner",
		Description:        "configured via admin API",
		AgentInterface:     &runtimestore.AgentInterface{},
		MinPlatformVersion: "1.4.0",
	}
	rt := &lennyv1.Runtime{Spec: lennyv1.RuntimeSpec{Type: "agent", Image: "img@sha256:" + hex64, IntegrationLevel: "basic"}}
	rt.Name = "scanner"

	applyCRDFields(&dst, rt)

	if dst.Description != "configured via admin API" {
		t.Errorf("description wiped: %q", dst.Description)
	}
	if dst.AgentInterface == nil {
		t.Error("agentInterface wiped")
	}
	if dst.MinPlatformVersion != "1.4.0" {
		t.Errorf("minPlatformVersion wiped: %q", dst.MinPlatformVersion)
	}
}

// TestApplyCRDFields_MirrorsWorkspaceTier_spec_12_9 verifies the §12.9 /
// §6.4 workspaceTier mirror: a `T4` Runtime CRD value mirrors onto the
// registry runtime so §5.2 cross-tenant-reuse rejection (line 396) and
// the §6.4 T4 dedicated-node injection both see the same value the
// deployer declared. An empty CRD value falls back to the empty
// (registry T3 default) value.
func TestApplyCRDFields_MirrorsWorkspaceTier_spec_12_9(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want runtimestore.WorkspaceTier
	}{
		{"t4-explicit", "T4", runtimestore.WorkspaceTierT4},
		{"t3-explicit", "T3", runtimestore.WorkspaceTierT3},
		{"empty-defaults-to-t3", "", runtimestore.WorkspaceTier("")},
	} {
		t.Run(c.name, func(t *testing.T) {
			rt := &lennyv1.Runtime{Spec: lennyv1.RuntimeSpec{
				Type:             "agent",
				Image:            "img@sha256:" + hex64,
				IntegrationLevel: "basic",
				WorkspaceTier:    c.in,
			}}
			var dst runtimestore.Runtime
			applyCRDFields(&dst, rt)
			if dst.WorkspaceTier != c.want {
				t.Errorf("WorkspaceTier = %q, want %q", dst.WorkspaceTier, c.want)
			}
		})
	}
}

// TestApplyCRDFields_MirrorsSetupCommandPolicy_spec_7_5 verifies the §5.1
// / §7.5 setupCommandPolicy mirror from the CRD onto the runtimestore
// runtime: every field (mode, shell, allowlist, blocklist, maxCommands)
// round-trips, slices are copied (not aliased), and a nil CRD block
// mirrors to a nil registry block (no policy declared). spec: §7.5 lines
// 481-490 — F-7.5.10.
func TestApplyCRDFields_MirrorsSetupCommandPolicy_spec_7_5(t *testing.T) {
	if got := setupCommandPolicyFromCRD(nil); got != nil {
		t.Errorf("nil setupCommandPolicy CRD block mapped to %+v, want nil", got)
	}
	src := &lennyv1.SetupCommandPolicy{
		Mode:        "allowlist",
		Shell:       false,
		Allowlist:   []string{"npm", "make"},
		Blocklist:   []string{"curl"},
		MaxCommands: 5,
	}
	got := setupCommandPolicyFromCRD(src)
	if got == nil {
		t.Fatal("setupCommandPolicyFromCRD(non-nil) = nil")
	}
	if got.Mode != runtimestore.SetupCommandModeAllowlist {
		t.Errorf("Mode = %q, want allowlist", got.Mode)
	}
	if got.Shell != false {
		t.Errorf("Shell = %v, want false", got.Shell)
	}
	if got.MaxCommands != 5 {
		t.Errorf("MaxCommands = %d, want 5", got.MaxCommands)
	}
	if len(got.Allowlist) != 2 || got.Allowlist[0] != "npm" || got.Allowlist[1] != "make" {
		t.Errorf("Allowlist = %v, want [npm make]", got.Allowlist)
	}
	if len(got.Blocklist) != 1 || got.Blocklist[0] != "curl" {
		t.Errorf("Blocklist = %v, want [curl]", got.Blocklist)
	}
	// Slice aliasing must not leak back into the CRD source.
	got.Allowlist[0] = "tampered"
	got.Blocklist[0] = "tampered"
	if src.Allowlist[0] != "npm" {
		t.Error("Allowlist slice aliased to CRD: mutation leaked back")
	}
	if src.Blocklist[0] != "curl" {
		t.Error("Blocklist slice aliased to CRD: mutation leaked back")
	}

	// And exercise the applyCRDFields path end-to-end.
	rt := &lennyv1.Runtime{Spec: lennyv1.RuntimeSpec{
		Type:               "agent",
		Image:              "img@sha256:" + hex64,
		IntegrationLevel:   "basic",
		SetupCommandPolicy: src,
	}}
	var dst runtimestore.Runtime
	applyCRDFields(&dst, rt)
	if dst.SetupCommandPolicy == nil || dst.SetupCommandPolicy.MaxCommands != 5 {
		t.Errorf("applyCRDFields did not plumb SetupCommandPolicy: %+v", dst.SetupCommandPolicy)
	}
}

// TestApplyCRDFields_MirrorsArchivePolicy_spec_7_4 verifies the §7.4 /
// §13.4 archivePolicy mirror from the CRD onto the runtimestore runtime:
// the AllowSymlinks field round-trips, a nil CRD block mirrors to a nil
// registry block, and applyCRDFields plumbs the field end-to-end.
// spec: §7.4 lines 458, 462; §13.4 lines 663-672 — F-7.4.4.
func TestApplyCRDFields_MirrorsArchivePolicy_spec_7_4(t *testing.T) {
	if got := archivePolicyFromCRD(nil); got != nil {
		t.Errorf("nil archivePolicy CRD block mapped to %+v, want nil", got)
	}
	src := &lennyv1.ArchivePolicy{AllowSymlinks: true}
	got := archivePolicyFromCRD(src)
	if got == nil {
		t.Fatal("archivePolicyFromCRD(non-nil) = nil")
	}
	if !got.AllowSymlinks {
		t.Errorf("AllowSymlinks = %v, want true", got.AllowSymlinks)
	}

	rt := &lennyv1.Runtime{Spec: lennyv1.RuntimeSpec{
		Type:             "agent",
		Image:            "img@sha256:" + hex64,
		IntegrationLevel: "basic",
		ArchivePolicy:    src,
	}}
	var dst runtimestore.Runtime
	applyCRDFields(&dst, rt)
	if dst.ArchivePolicy == nil || !dst.ArchivePolicy.AllowSymlinks {
		t.Errorf("applyCRDFields did not plumb ArchivePolicy: %+v", dst.ArchivePolicy)
	}
}

// TestCredentialCapabilitiesFromCRD verifies the nil-block mapping and
// that the proxyDialect slice is copied, not aliased to the CRD slice.
func TestCredentialCapabilitiesFromCRD(t *testing.T) {
	if got := credentialCapabilitiesFromCRD(nil); got != nil {
		t.Errorf("nil block mapped to %+v, want nil", got)
	}
	src := &lennyv1.CredentialCapabilities{ProxyDialect: []string{"anthropic"}}
	got := credentialCapabilitiesFromCRD(src)
	if got == nil || len(got.ProxyDialect) != 1 {
		t.Fatalf("got %+v", got)
	}
	got.ProxyDialect[0] = "mutated"
	if src.ProxyDialect[0] != "anthropic" {
		t.Error("proxyDialect slice aliased to CRD: mutation leaked back")
	}
}

// TestDomainLabels_spec_10_6 verifies the §10.6 runtimeSelector label
// mirror drops the Kubernetes/Helm machinery labels the chart's
// lenny.labels helper stamps and keeps domain labels (including the
// lenny.dev/* markers).
func TestDomainLabels_spec_10_6(t *testing.T) {
	in := map[string]string{
		"app.kubernetes.io/name":       "lenny",
		"app.kubernetes.io/managed-by": "Helm",
		"helm.sh/chart":                "lenny-0.1.0",
		"lenny.dev/reference-runtime":  "true",
		"team":                         "platform",
	}
	out := domainLabels(in)
	if _, ok := out["app.kubernetes.io/name"]; ok {
		t.Error("kept app.kubernetes.io/ label")
	}
	if _, ok := out["helm.sh/chart"]; ok {
		t.Error("kept helm.sh/ label")
	}
	if out["lenny.dev/reference-runtime"] != "true" {
		t.Error("dropped lenny.dev/ domain label")
	}
	if out["team"] != "platform" {
		t.Error("dropped custom domain label")
	}
}

// TestDomainLabels_AllSystem returns nil when only machinery labels are
// present, so a chart-managed runtime with no domain labels mirrors to a
// nil label set rather than carrying Helm noise into the registry.
func TestDomainLabels_AllSystem(t *testing.T) {
	in := map[string]string{
		"app.kubernetes.io/name": "lenny",
		"helm.sh/chart":          "lenny-0.1.0",
	}
	if out := domainLabels(in); out != nil {
		t.Errorf("all-machinery labels mapped to %v, want nil", out)
	}
	if out := domainLabels(nil); out != nil {
		t.Errorf("nil labels mapped to %v, want nil", out)
	}
}

// TestCapabilitiesFromCRD_spec_5_1 verifies the §5.1 capabilities block
// mirrors from the CRD onto the runtimestore: interaction + injection
// (supported + modes) + preConnect round-trip; a nil CRD block mirrors to
// nil; the modes slice is copied, not aliased to the CRD slice.
// spec: §5.1 lines 60-64; §26.3 lines 150-155 — F-26.3.1 / F-26.9.2 /
// F-26.10.2 / F-26.11.2.
func TestCapabilitiesFromCRD_spec_5_1(t *testing.T) {
	if got := capabilitiesFromCRD(nil); got != nil {
		t.Errorf("nil block mapped to %+v, want nil", got)
	}
	src := &lennyv1.RuntimeCapabilitiesCRD{
		Interaction: "multi_turn",
		Injection: &lennyv1.InjectionCapabilityCRD{
			Supported: true,
			Modes:     []string{"immediate", "queued"},
		},
		PreConnect: false,
	}
	got := capabilitiesFromCRD(src)
	if got == nil {
		t.Fatal("capabilitiesFromCRD(non-nil) = nil")
	}
	if got.Interaction != runtimestore.InteractionMultiTurn {
		t.Errorf("Interaction = %q, want multi_turn", got.Interaction)
	}
	if !got.Injection.Supported {
		t.Error("Injection.Supported = false, want true")
	}
	if len(got.Injection.Modes) != 2 ||
		got.Injection.Modes[0] != runtimestore.InjectionImmediate ||
		got.Injection.Modes[1] != runtimestore.InjectionQueued {
		t.Errorf("Injection.Modes = %v, want [immediate, queued]", got.Injection.Modes)
	}
	if got.PreConnect {
		t.Error("PreConnect = true, want false")
	}
	// Mutating the registry copy must not leak back to the CRD slice.
	got.Injection.Modes[0] = "mutated"
	if src.Injection.Modes[0] != "immediate" {
		t.Error("Injection.Modes aliased to CRD: mutation leaked back")
	}
}

// TestRuntimeOptionsSchemaFromRef_spec_5_1 verifies the §5.1 / §26 schema
// URL is rendered as a JSON {"$ref": "<url>"} fragment the §14 validator
// can dereference. An empty ref returns nil.
// spec: §5.1 line 92; §26.9 line 408 — F-26.9.2.
func TestRuntimeOptionsSchemaFromRef_spec_5_1(t *testing.T) {
	if got := runtimeOptionsSchemaFromRef(""); got != nil {
		t.Errorf("empty ref returned %s, want nil", got)
	}
	const url = "https://schemas.lenny.dev/runtime-options/mastra/v1.json"
	got := runtimeOptionsSchemaFromRef(url)
	var doc map[string]string
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("ref schema not valid JSON: %v", err)
	}
	if doc["$ref"] != url {
		t.Errorf("schema $ref = %q, want %q", doc["$ref"], url)
	}
}

// TestApplyCRDFields_MirrorsCapabilitiesAndSchemaAndDelegation_spec_5_1
// verifies the end-to-end plumbing: the §5.1 capabilities block, the
// §5.1 / §26 runtimeOptionsSchemaRef URL, and the §5.1 / §26.11
// delegationPolicyRef all land on the runtimestore runtime when the CRD
// declares them. spec: §5.1 lines 60-92; §26.11 line 467 — F-26.9.2 /
// F-26.10.2 / F-26.11.2.
func TestApplyCRDFields_MirrorsCapabilitiesAndSchemaAndDelegation_spec_5_1(t *testing.T) {
	rt := &lennyv1.Runtime{Spec: lennyv1.RuntimeSpec{
		Type:             "agent",
		Image:            "img@sha256:" + hex64,
		IntegrationLevel: "full",
		Capabilities: &lennyv1.RuntimeCapabilitiesCRD{
			Interaction: "multi_turn",
			Injection: &lennyv1.InjectionCapabilityCRD{
				Supported: true,
				Modes:     []string{"immediate", "queued"},
			},
		},
		RuntimeOptionsSchemaRef: "https://schemas.lenny.dev/runtime-options/crewai/v1.json",
		DelegationPolicyRef:     "crewai-default",
	}}
	var dst runtimestore.Runtime
	applyCRDFields(&dst, rt)
	if dst.Capabilities == nil || dst.Capabilities.Interaction != runtimestore.InteractionMultiTurn {
		t.Errorf("Capabilities not plumbed: %+v", dst.Capabilities)
	}
	if !dst.Capabilities.Injection.Supported {
		t.Errorf("Injection.Supported = false, want true")
	}
	if len(dst.RuntimeOptionsSchema) == 0 {
		t.Errorf("RuntimeOptionsSchema not plumbed")
	}
	if dst.DelegationPolicyRef != "crewai-default" {
		t.Errorf("DelegationPolicyRef = %q, want %q", dst.DelegationPolicyRef, "crewai-default")
	}
}

// hex64 is a 64-hex digest body used to satisfy the §5.3 digest-pinned
// image references the Runtime CRD pattern requires.
const hex64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
