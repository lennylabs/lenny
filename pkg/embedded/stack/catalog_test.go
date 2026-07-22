// SPDX-License-Identifier: MIT

package stack

import (
	"strings"
	"testing"
)

func TestReferenceRuntimesCoversCatalog(t *testing.T) {
	rts := ReferenceRuntimes()
	// §26.1 lists nine reference runtimes. Spot-check the names rather
	// than the count so the test states which entries are expected.
	wantNames := []string{
		"claude-code", "gemini-cli", "codex", "cursor-cli", "chat",
		"langgraph", "mastra", "openai-assistants", "crewai",
	}
	got := map[string]ReferenceRuntime{}
	for _, rt := range rts {
		got[rt.Name] = rt
	}
	for _, name := range wantNames {
		if _, ok := got[name]; !ok {
			t.Errorf("reference catalog is missing %q", name)
		}
	}
	if len(rts) != len(wantNames) {
		t.Errorf("catalog has %d runtimes, want %d", len(rts), len(wantNames))
	}
}

func TestReferenceRuntimesIntegrationLevels(t *testing.T) {
	for _, rt := range ReferenceRuntimes() {
		// §26.1: every reference runtime, including chat, is Full.
		want := "full"
		if rt.IntegrationLevel != want {
			t.Errorf("%s integrationLevel = %q, want %q", rt.Name, rt.IntegrationLevel, want)
		}
		if !strings.HasPrefix(rt.Image, "ghcr.io/lennylabs/runtime-") {
			t.Errorf("%s image %q is not a canonical Lenny registry path", rt.Name, rt.Image)
		}
		// §5.1 / §13.1: the gateway rejects a tag-only image reference.
		// Every catalog image must be digest-pinned.
		if !strings.Contains(rt.Image, "@sha256:") {
			t.Errorf("%s image %q is not digest-pinned", rt.Name, rt.Image)
		}
	}
}

// TestEchoRuntimeEntry_spec_15_4_4 asserts the §15.4.4 echo conformance
// exemplar is seeded as a credential-free, Basic-level, single-pod warm
// pool runtime distinct from the §26 reference catalog. spec: §15.4.4,
// §5.2, §17.4.
func TestEchoRuntimeEntry_spec_15_4_4(t *testing.T) {
	echo := EchoRuntime()
	if echo.Name != EchoRuntimeName {
		t.Errorf("echo name = %q, want %q", echo.Name, EchoRuntimeName)
	}
	// echo-embedded is Basic-level (cmd/runtimes/echo-embedded), unlike the
	// Full §26 reference runtimes.
	if echo.IntegrationLevel != "basic" {
		t.Errorf("echo integrationLevel = %q, want basic", echo.IntegrationLevel)
	}
	// The image is the canonical echo-embedded repository, digest-pinned.
	if !strings.HasPrefix(echo.Image, echoImageRepository) {
		t.Errorf("echo image %q is not the canonical echo-embedded repository", echo.Image)
	}
	if !strings.Contains(echo.Image, "@sha256:") {
		t.Errorf("echo image %q is not digest-pinned", echo.Image)
	}
	// §5.2 hot-pool taxonomy: a single-pod warm pool (warmCount: 1) so the
	// WarmPoolController pre-warms one echo pod.
	if echo.DefaultPoolConfig == nil || echo.DefaultPoolConfig.WarmCount != 1 {
		t.Errorf("echo defaultPoolConfig = %+v, want warmCount 1", echo.DefaultPoolConfig)
	}
	if echo.DefaultPoolConfig.ResourceClass != "small" {
		t.Errorf("echo resourceClass = %q, want small", echo.DefaultPoolConfig.ResourceClass)
	}
	// Credential-free: no LLM provider, no supportedProviders, no
	// credentialCapabilities. §13: the runtime leases no credentials.
	if len(echo.SupportedProviders) != 0 {
		t.Errorf("echo declares supportedProviders %v, want none (credential-free)", echo.SupportedProviders)
	}
	if echo.CredentialCapabilities != nil {
		t.Errorf("echo declares credentialCapabilities %+v, want none (credential-free)", echo.CredentialCapabilities)
	}
	// echo carries its own credential-free marker rather than the §26
	// reference-runtime marker.
	if echo.Labels["lenny.dev/reference-runtime"] == "true" {
		t.Errorf("echo carries the §26 reference-runtime marker, want the echo-runtime marker only: %v", echo.Labels)
	}
	if echo.Labels["lenny.dev/echo-runtime"] != "true" {
		t.Errorf("echo missing the echo-runtime marker label, got %v", echo.Labels)
	}
}

// TestEchoRuntimeNotInReferenceCatalog_spec_26_1 asserts echo is declared
// outside the §26 referenceRuntimes slice so the §26-only loops
// (placeholderPinnedRuntimes, the bootstrap-seed reference loop) do not
// treat the runnable echo record as a placeholder-pinned §26 entry. spec:
// §26.1, §15.4.4.
func TestEchoRuntimeNotInReferenceCatalog_spec_26_1(t *testing.T) {
	for _, rt := range ReferenceRuntimes() {
		if rt.Name == EchoRuntimeName {
			t.Fatalf("echo must not appear in the §26 reference catalog")
		}
	}
	// echo's sentinel image must not be the §26 placeholder digest, so the
	// placeholder-pin scan never lists echo.
	if hasPlaceholderDigest(EchoRuntime().Image) {
		t.Error("echo image must not carry the §26 placeholder digest")
	}
	for _, name := range placeholderPinnedRuntimes() {
		if name == EchoRuntimeName {
			t.Error("placeholderPinnedRuntimes must not list the runnable echo runtime")
		}
	}
}

// TestChatRuntimeInjectionImmediateOnly asserts the chat reference
// runtime's capabilities.injection block is supported:true with
// modes:[immediate] exactly, no queued, distinguishing chat from the
// coding-agent default of [immediate, queued]. spec: §26.7 lines 323-325.
//
// diagnosis: a failure means the chat entry's Capabilities.Injection
// block drifted from the spec (queued crept back in, supported flipped
// to false, or the field was dropped entirely), which would let the
// gateway advertise queued-injection support for a runtime that cannot
// honor it.
func TestChatRuntimeInjectionImmediateOnly(t *testing.T) {
	rts := ReferenceRuntimes()
	var chat *ReferenceRuntime
	for i, rt := range rts {
		if rt.Name == "chat" {
			chat = &rts[i]
			break
		}
	}
	if chat == nil {
		t.Fatal("reference catalog is missing the chat runtime")
	}
	if chat.Capabilities == nil || chat.Capabilities.Injection == nil {
		t.Fatal("chat runtime has no capabilities.injection block")
	}
	inj := chat.Capabilities.Injection
	if !inj.Supported {
		t.Error("chat capabilities.injection.supported = false, want true")
	}
	want := []string{"immediate"}
	if len(inj.Modes) != len(want) {
		t.Fatalf("chat capabilities.injection.modes = %v, want %v", inj.Modes, want)
	}
	for i, m := range want {
		if inj.Modes[i] != m {
			t.Errorf("chat capabilities.injection.modes = %v, want %v", inj.Modes, want)
		}
	}
}

// TestChatRuntimeMaxUploadSize asserts the chat reference runtime's
// limits.maxUploadSizeBytes registers as 10 MB (10485760 bytes),
// distinct from the 500 MB (524288000 bytes) the §26.2 shared
// coding-agent block declares. spec: §26.7 line 340 ("maxUploadSize:
// 10MB # file attachments only; no archive uploads").
//
// diagnosis: a failure means the chat entry's Limits block is missing
// or carries a value other than 10485760, so the registered chat
// manifest would either omit the §26.7 upload cap entirely (falling
// back to the platform default, which is unbounded per
// runtimestore.Limits) or advertise the coding-agent 500 MB cap
// instead of the chat-specific 10 MB cap.
func TestChatRuntimeMaxUploadSize(t *testing.T) {
	rts := ReferenceRuntimes()
	var chat *ReferenceRuntime
	for i, rt := range rts {
		if rt.Name == "chat" {
			chat = &rts[i]
			break
		}
	}
	if chat == nil {
		t.Fatal("reference catalog is missing the chat runtime")
	}
	if chat.Limits == nil {
		t.Fatal("chat runtime has no limits block; want maxUploadSizeBytes: 10485760")
	}
	const wantBytes = 10 * 1024 * 1024
	if chat.Limits.MaxUploadSizeBytes != wantBytes {
		t.Errorf("chat limits.maxUploadSizeBytes = %d, want %d (10 MB)", chat.Limits.MaxUploadSizeBytes, wantBytes)
	}
	// The chat cap must be the runtime's own 10 MB figure, not a copy of
	// the §26.2 shared coding-agent 500 MB cap.
	const codingAgentBytes = 524288000
	if chat.Limits.MaxUploadSizeBytes == codingAgentBytes {
		t.Errorf("chat limits.maxUploadSizeBytes = %d matches the coding-agent 500 MB cap, want the chat-specific 10 MB cap", chat.Limits.MaxUploadSizeBytes)
	}
}

// TestChatRuntimeSessionAndInputWaitLimits asserts the chat reference
// runtime's limits.maxSessionAgeSeconds and
// limits.maxRequestInputWaitSeconds register at the runtime-specific
// values the manifest declares alongside maxUploadSize, rather than
// being left unset and falling back to the platform default. spec:
// §26.7 lines 338-341 ("limits: maxSessionAge: 3600 maxUploadSize: 10MB
// ... maxRequestInputWaitSeconds: 600").
//
// diagnosis: a failure means the chat entry's Limits block omits
// MaxSessionAgeSeconds or MaxRequestInputWaitSeconds (or carries a
// value other than 3600/600), so the registered chat manifest would
// fall back to the platform default session age and input-wait
// ceiling instead of the runtime-specific caps the §26.7 limits block
// declares.
func TestChatRuntimeSessionAndInputWaitLimits(t *testing.T) {
	rts := ReferenceRuntimes()
	var chat *ReferenceRuntime
	for i, rt := range rts {
		if rt.Name == "chat" {
			chat = &rts[i]
			break
		}
	}
	if chat == nil {
		t.Fatal("reference catalog is missing the chat runtime")
	}
	if chat.Limits == nil {
		t.Fatal("chat runtime has no limits block; want maxSessionAgeSeconds: 3600, maxRequestInputWaitSeconds: 600")
	}
	const wantSessionAge = 3600
	if chat.Limits.MaxSessionAgeSeconds != wantSessionAge {
		t.Errorf("chat limits.maxSessionAgeSeconds = %d, want %d", chat.Limits.MaxSessionAgeSeconds, wantSessionAge)
	}
	const wantInputWait = 600
	if chat.Limits.MaxRequestInputWaitSeconds != wantInputWait {
		t.Errorf("chat limits.maxRequestInputWaitSeconds = %d, want %d", chat.Limits.MaxRequestInputWaitSeconds, wantInputWait)
	}
}

// TestChatRuntimeSupportedProvidersAndProxyDialect asserts the chat
// reference runtime's supportedProviders and
// credentialCapabilities.proxyDialect slices register exactly the three
// providers/dialects the manifest declares: anthropic_direct,
// openai_direct, and gcp_vertex_gemini, paired with the anthropic,
// openai, and google LLM-proxy dialects. spec: §26.7 lines 331-337
// ("supportedProviders: - anthropic_direct - openai_direct -
// gcp_vertex_gemini" / "credentialCapabilities: hotRotation: true
// proxyDialect: [anthropic, openai, google]").
//
// diagnosis: a failure means the chat entry's SupportedProviders or
// CredentialCapabilities.ProxyDialect slice dropped, reordered, or
// substituted a provider/dialect, so the registered chat manifest would
// advertise a provider combination the runtime does not actually cover
// (or omit one it does), even though nothing else in this suite checks
// the manifest-level provider/dialect pairing declared for chat.
func TestChatRuntimeSupportedProvidersAndProxyDialect(t *testing.T) {
	rts := ReferenceRuntimes()
	var chat *ReferenceRuntime
	for i, rt := range rts {
		if rt.Name == "chat" {
			chat = &rts[i]
			break
		}
	}
	if chat == nil {
		t.Fatal("reference catalog is missing the chat runtime")
	}

	wantProviders := []string{"anthropic_direct", "openai_direct", "gcp_vertex_gemini"}
	if len(chat.SupportedProviders) != len(wantProviders) {
		t.Fatalf("chat supportedProviders = %v, want %v", chat.SupportedProviders, wantProviders)
	}
	for i, p := range wantProviders {
		if chat.SupportedProviders[i] != p {
			t.Errorf("chat supportedProviders = %v, want %v", chat.SupportedProviders, wantProviders)
		}
	}

	if chat.CredentialCapabilities == nil {
		t.Fatal("chat runtime has no credentialCapabilities block; want proxyDialect: [anthropic, openai, google]")
	}
	wantDialects := []string{"anthropic", "openai", "google"}
	gotDialects := chat.CredentialCapabilities.ProxyDialect
	if len(gotDialects) != len(wantDialects) {
		t.Fatalf("chat credentialCapabilities.proxyDialect = %v, want %v", gotDialects, wantDialects)
	}
	for i, d := range wantDialects {
		if gotDialects[i] != d {
			t.Errorf("chat credentialCapabilities.proxyDialect = %v, want %v", gotDialects, wantDialects)
		}
	}
}

func TestReferenceRuntimesReturnsCopy(t *testing.T) {
	first := ReferenceRuntimes()
	first[0].Name = "mutated"
	second := ReferenceRuntimes()
	if second[0].Name == "mutated" {
		t.Error("ReferenceRuntimes returned a slice sharing backing storage")
	}
}

func TestBuildBootstrapSeed(t *testing.T) {
	seed := buildBootstrapSeed("")
	// §17.4: lenny up creates the default tenant.
	if len(seed.Tenants) != 1 || seed.Tenants[0].ID != defaultTenant {
		t.Errorf("seed tenants = %+v, want the default tenant", seed.Tenants)
	}
	// The built-in user is seeded as a platform-admin in the default
	// tenant.
	if len(seed.Users) != 1 {
		t.Fatalf("seed users = %+v, want one built-in user", seed.Users)
	}
	if seed.Users[0].TenantID != defaultTenant {
		t.Errorf("built-in user tenant = %q, want %q", seed.Users[0].TenantID, defaultTenant)
	}
	hasAdmin := false
	for _, r := range seed.Users[0].Roles {
		if r == "platform-admin" {
			hasAdmin = true
		}
	}
	if !hasAdmin {
		t.Errorf("built-in user roles = %v, want platform-admin", seed.Users[0].Roles)
	}
	// Every §26 reference runtime plus the §15.4.4 echo exemplar is seeded
	// as a type:agent record.
	if len(seed.Runtimes) != len(referenceRuntimes)+1 {
		t.Errorf("seed has %d runtimes, want %d (the §26 catalog plus echo)", len(seed.Runtimes), len(referenceRuntimes)+1)
	}
	for _, rt := range seed.Runtimes {
		if rt.Type != "agent" {
			t.Errorf("runtime %s seeded with type %q, want agent", rt.Name, rt.Type)
		}
		if rt.Image == "" {
			t.Errorf("runtime %s seeded without an image", rt.Name)
		}
		// §5.1 line 51: labels are required from v1. The gateway bootstrap
		// handler rejects a create without them, so every seeded runtime
		// must carry at least one label or `lenny up` fails to install.
		if len(rt.Labels) == 0 {
			t.Errorf("runtime %s seeded without labels (§5.1 line 51 requires them)", rt.Name)
		}
		// The §26 reference runtimes carry the reference-runtime marker;
		// echo is the §15.4.4 conformance exemplar rather than a §26
		// reference runtime, so it carries its own credential-free marker.
		if rt.Name == EchoRuntimeName {
			if rt.Labels["lenny.dev/echo-runtime"] != "true" {
				t.Errorf("echo runtime missing the echo-runtime marker label, got %v", rt.Labels)
			}
			continue
		}
		if rt.Labels["lenny.dev/reference-runtime"] != "true" {
			t.Errorf("runtime %s missing the reference-runtime marker label, got %v", rt.Name, rt.Labels)
		}
	}
}
