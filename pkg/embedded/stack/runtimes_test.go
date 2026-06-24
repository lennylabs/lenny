// SPDX-License-Identifier: MIT

package stack

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// TestInstallReferenceRuntimesGrantFailureNamesRuntimes_spec_24_3
// asserts that when a reference-runtime tenant-access grant fails, the
// returned error names the failing runtime(s) rather than a bare count,
// so an operator with no §24.3 CLI retry loop can act on it. F-24.3.4.
func TestInstallReferenceRuntimesGrantFailureNamesRuntimes_spec_24_3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/admin/bootstrap":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/tenant-access"):
			// Fail every grant so the joined error names every runtime.
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"FORBIDDEN","message":"nope"}}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	err := installReferenceRuntimes(context.Background(), srv.URL, "", io.Discard)
	if err == nil {
		t.Fatal("expected an error when grants fail")
	}
	msg := err.Error()
	// The error must name a concrete failing runtime, not only a count.
	if !strings.Contains(msg, referenceRuntimes[0].Name) {
		t.Errorf("error does not name the failing runtime %q: %v", referenceRuntimes[0].Name, msg)
	}
	for _, rt := range referenceRuntimes {
		if !strings.Contains(msg, rt.Name) {
			t.Errorf("error omits failing runtime %q: %v", rt.Name, msg)
		}
	}
}

// TestInstallReferenceRuntimesAllGrantsSucceed_spec_24_3 asserts the
// happy path returns no error when every grant succeeds.
func TestInstallReferenceRuntimesAllGrantsSucceed_spec_24_3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if err := installReferenceRuntimes(context.Background(), srv.URL, "", io.Discard); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

// TestInstallReferenceRuntimesWarnsOnPlaceholderDigest_spec_26_3 asserts
// the bootstrap output warns the operator that placeholder-pinned
// reference runtimes register but cannot start a session until re-pinned.
// F-26.3.6.
func TestInstallReferenceRuntimesWarnsOnPlaceholderDigest_spec_26_3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := installReferenceRuntimes(context.Background(), srv.URL, "", &out); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "[WARN]") || !strings.Contains(got, "placeholder image digest") {
		t.Errorf("output should warn about placeholder digests: %q", got)
	}
	// The whole catalog is placeholder-pinned today, so every runtime is named.
	for _, rt := range referenceRuntimes {
		if !strings.Contains(got, rt.Name) {
			t.Errorf("warning omits placeholder-pinned runtime %q: %q", rt.Name, got)
		}
	}
	if !strings.Contains(got, "lenny image import") {
		t.Errorf("warning should point at the remediation: %q", got)
	}
}

// TestBuildBootstrapSeedSeedsEchoPool_spec_5_2 asserts the §17.4 Embedded
// Mode seed creates exactly one echo warm pool referencing the echo
// runtime, with the §5.2 single-pod hot-pool count, the §17.4
// local-fidelity `standard` (runc) isolation plus the allowStandardIsolation
// opt-in, and the §13.2 cluster-default DNS opt-out the embedded substrate
// requires. spec: §5.2, §13.2, §17.4.
func TestBuildBootstrapSeedSeedsEchoPool_spec_5_2(t *testing.T) {
	seed := buildBootstrapSeed("")
	if len(seed.Pools) != 1 {
		t.Fatalf("seed has %d pools, want exactly the echo pool", len(seed.Pools))
	}
	p := seed.Pools[0]
	if p.RuntimeRef != EchoRuntimeName {
		t.Errorf("echo pool runtimeRef = %q, want %q", p.RuntimeRef, EchoRuntimeName)
	}
	// §5.2 hot pool: warmCount 1 yields minWarm = maxWarm = 1, so the
	// WarmPoolController pre-warms exactly one pod.
	if p.WarmCount != 1 {
		t.Errorf("echo pool warmCount = %d, want 1 (single-pod hot pool)", p.WarmCount)
	}
	// §17.4 local fidelity: the embedded single-node cluster runs runc, so
	// the pool sets standard isolation and the allowStandardIsolation opt-in
	// the gateway admission path requires for an explicit standard profile.
	if p.IsolationProfile != "standard" {
		t.Errorf("echo pool isolationProfile = %q, want standard", p.IsolationProfile)
	}
	if !p.AllowStandardIsolation {
		t.Error("echo pool must set allowStandardIsolation so the gateway admits the explicit standard profile")
	}
	// §13.2: cluster-default opts the pool's pods out of the dedicated
	// lenny-system CoreDNS the embedded substrate does not run.
	if p.DNSPolicy != "cluster-default" {
		t.Errorf("echo pool dnsPolicy = %q, want cluster-default", p.DNSPolicy)
	}
}

// TestEchoSeedPoolMatchesBootstrapSeed_spec_17_4 asserts the exported
// EchoSeedPool accessor returns the §17.4 echo warm pool as the poolstore
// row mapped field-for-field from buildBootstrapSeed, so a materialization
// witness driving off the accessor exercises the exact row the embedded
// stack seeds and cannot drift from it undetected. spec: §5.2 (warm pool),
// §13.2 (per-pool DNS), §17.4 (Embedded Mode seed).
func TestEchoSeedPoolMatchesBootstrapSeed_spec_17_4(t *testing.T) {
	seeded := buildBootstrapSeed("").Pools[0]
	pool := EchoSeedPool()

	if pool.Name != seeded.Name {
		t.Errorf("EchoSeedPool name = %q, want %q (the seeded echo pool name)", pool.Name, seeded.Name)
	}
	// The seed names the §5.2 hot pool echo-pool-embedded, matching the
	// working Kind precedent; a row named "echo" would resolve a different
	// SandboxWarmPool than the embedded stack materializes.
	if pool.Name != "echo-pool-embedded" {
		t.Errorf("EchoSeedPool name = %q, want echo-pool-embedded", pool.Name)
	}
	if pool.RuntimeRef != seeded.RuntimeRef || pool.RuntimeRef != EchoRuntimeName {
		t.Errorf("EchoSeedPool runtimeRef = %q, want %q", pool.RuntimeRef, EchoRuntimeName)
	}
	if pool.WarmCount != seeded.WarmCount || pool.WarmCount != 1 {
		t.Errorf("EchoSeedPool warmCount = %d, want 1 (single-pod hot pool)", pool.WarmCount)
	}
	if pool.ResourceClass != seeded.ResourceClass {
		t.Errorf("EchoSeedPool resourceClass = %q, want %q", pool.ResourceClass, seeded.ResourceClass)
	}
	// §13.2 restricted egress carried from the seed; a witness that omits it
	// diverges from the row the embedded stack emits.
	if string(pool.EgressProfile) != seeded.EgressProfile || string(pool.EgressProfile) != "restricted" {
		t.Errorf("EchoSeedPool egressProfile = %q, want restricted", pool.EgressProfile)
	}
	if string(pool.IsolationProfile) != seeded.IsolationProfile || string(pool.IsolationProfile) != "standard" {
		t.Errorf("EchoSeedPool isolationProfile = %q, want standard", pool.IsolationProfile)
	}
	if !pool.AllowStandardIsolation || pool.AllowStandardIsolation != seeded.AllowStandardIsolation {
		t.Error("EchoSeedPool must set allowStandardIsolation so the gateway admits the explicit standard profile")
	}
	if pool.DNSPolicy != seeded.DNSPolicy || pool.DNSPolicy != poolstore.DNSPolicyClusterDefault {
		t.Errorf("EchoSeedPool dnsPolicy = %q, want cluster-default", pool.DNSPolicy)
	}
	// The seed sets no ExecutionMode, so the mapped row carries the empty
	// default; a witness that fabricates `session` diverges from the seed.
	if pool.ExecutionMode != "" {
		t.Errorf("EchoSeedPool executionMode = %q, want empty (the seed sets none)", pool.ExecutionMode)
	}
}

// TestBuildBootstrapSeedEchoPoolDNSPolicyAdmissible_spec_13_2 asserts the
// seeded echo pool's dnsPolicy passes the §13.2 poolstore admission rule:
// cluster-default is admitted only on a `standard` (runc) pool, which the
// echo pool is. spec: §13.2.
func TestBuildBootstrapSeedEchoPoolDNSPolicyAdmissible_spec_13_2(t *testing.T) {
	p := buildBootstrapSeed("").Pools[0]
	pool := poolstore.Pool{
		Name:             p.Name,
		IsolationProfile: isolation.Profile(p.IsolationProfile),
		DNSPolicy:        p.DNSPolicy,
	}
	if err := poolstore.ValidateDNSPolicy(pool); err != nil {
		t.Fatalf("echo pool dnsPolicy is not §13.2-admissible: %v", err)
	}
}

// TestBuildBootstrapSeedInjectsEchoDigest_spec_4_7 asserts the bring-up's
// import-time-resolved echo image reference overwrites the echo seed's sentinel
// image, so the seeded digest equals the applied Runtime CR's and the
// containerd image's. The digest-pinned IfNotPresent pull requires all three to
// agree. spec: §4.7 (digest-pinned pod image), §15.4.4 (echo exemplar).
func TestBuildBootstrapSeedInjectsEchoDigest_spec_4_7(t *testing.T) {
	const resolved = "ghcr.io/lennylabs/runtime-echo-embedded@sha256:" +
		"abcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcab"
	seed := buildBootstrapSeed(resolved)
	var echo *seedRuntime
	for i := range seed.Runtimes {
		if seed.Runtimes[i].Name == EchoRuntimeName {
			echo = &seed.Runtimes[i]
		}
	}
	if echo == nil {
		t.Fatal("seed has no echo runtime")
	}
	if echo.Image != resolved {
		t.Errorf("echo seed image = %q, want the import-time-resolved digest %q", echo.Image, resolved)
	}
}

// TestBuildBootstrapSeedEchoDigestSentinelWhenUnresolved_spec_4_7 asserts that
// when the import did not resolve a digest (substrate down or import failed),
// the echo seed keeps its sentinel placeholder rather than an empty image,
// which the bootstrap handler would reject. On the substrate-down path the
// gateway keeps the in-process echo executor (AgentNamespace stays unset because
// it is gated on k3sEnabled). On the k3s-up-but-import-failed path the
// Runtime-CR apply is skipped (gated on a resolved digest), so a session against
// the sentinel record resolves no warm pool and fails the create rather than
// pulling the unpullable sentinel image. spec: §4.7.
func TestBuildBootstrapSeedEchoDigestSentinelWhenUnresolved_spec_4_7(t *testing.T) {
	seed := buildBootstrapSeed("")
	var echo *seedRuntime
	for i := range seed.Runtimes {
		if seed.Runtimes[i].Name == EchoRuntimeName {
			echo = &seed.Runtimes[i]
		}
	}
	if echo == nil {
		t.Fatal("seed has no echo runtime")
	}
	if echo.Image != echoImageSentinel {
		t.Errorf("echo seed image with no resolved digest = %q, want the sentinel %q", echo.Image, echoImageSentinel)
	}
}

// TestInstallReferenceRuntimesSeedsResolvedEchoDigest_spec_4_7 asserts the
// resolved echo image reference reaches the gateway bootstrap call rather than
// being dropped in installReferenceRuntimes. The fake gateway captures the
// posted seed and the test confirms the echo record carries the resolved
// digest. spec: §4.7 (digest-pinned pod image), §15.4.4 (echo exemplar).
func TestInstallReferenceRuntimesSeedsResolvedEchoDigest_spec_4_7(t *testing.T) {
	const resolved = "ghcr.io/lennylabs/runtime-echo-embedded@sha256:" +
		"def0def0def0def0def0def0def0def0def0def0def0def0def0def0def0def0d"
	var postedEchoImage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/admin/bootstrap" {
			var seed bootstrapSeed
			if err := json.NewDecoder(r.Body).Decode(&seed); err != nil {
				t.Errorf("decode posted seed: %v", err)
			}
			for _, rt := range seed.Runtimes {
				if rt.Name == EchoRuntimeName {
					postedEchoImage = rt.Image
				}
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if err := installReferenceRuntimes(context.Background(), srv.URL, resolved, io.Discard); err != nil {
		t.Fatalf("installReferenceRuntimes: %v", err)
	}
	if postedEchoImage != resolved {
		t.Errorf("posted echo image = %q, want the resolved digest %q", postedEchoImage, resolved)
	}
}

// TestInstallReferenceRuntimesGrantsEcho_spec_26_1 asserts the
// default-tenant grant loop covers the seeded echo runtime in addition to
// the §26 reference runtimes, so `lenny session new --runtime echo` is
// reachable for the default tenant the tier-4 smoke runs against. spec:
// §26.1, §15.4.4.
func TestInstallReferenceRuntimesGrantsEcho_spec_26_1(t *testing.T) {
	var grantedNames []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant-access") {
			// /v1/admin/runtimes/<name>/tenant-access
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/admin/runtimes/"), "/")
			grantedNames = append(grantedNames, parts[0])
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if err := installReferenceRuntimes(context.Background(), srv.URL, "", io.Discard); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	hasEcho := false
	for _, n := range grantedNames {
		if n == EchoRuntimeName {
			hasEcho = true
		}
	}
	if !hasEcho {
		t.Errorf("default-tenant grant did not cover the echo runtime; granted %v", grantedNames)
	}
	// Every §26 reference runtime is still granted alongside echo.
	for _, rt := range referenceRuntimes {
		found := false
		for _, n := range grantedNames {
			if n == rt.Name {
				found = true
			}
		}
		if !found {
			t.Errorf("default-tenant grant dropped reference runtime %q; granted %v", rt.Name, grantedNames)
		}
	}
}

// TestInstallReferenceRuntimesWarnDoesNotListEcho_spec_26_3 asserts the
// placeholder-pin WARN stays scoped to the §26 reference runtimes so the
// runnable echo runtime is not listed as un-startable. spec: §26.3, §15.4.4.
func TestInstallReferenceRuntimesWarnDoesNotListEcho_spec_26_3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := installReferenceRuntimes(context.Background(), srv.URL, "", &out); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	// The WARN line names the placeholder-pinned runtimes; echo is runnable
	// and must not appear among them. Scope the assertion to the WARN line
	// so the install summary line (which mentions "echo runtime") does not
	// trip it.
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "[WARN]") && strings.Contains(line, EchoRuntimeName) {
			t.Errorf("placeholder-pin WARN must not list the runnable echo runtime: %q", line)
		}
	}
}

// TestPlaceholderPinnedRuntimes_spec_26_3 covers the digest detector and
// the catalog scan it backs.
func TestPlaceholderPinnedRuntimes_spec_26_3(t *testing.T) {
	if !hasPlaceholderDigest("ghcr.io/lennylabs/runtime-chat:1.0.0" + placeholderDigest) {
		t.Error("hasPlaceholderDigest should detect the sentinel suffix")
	}
	if hasPlaceholderDigest("ghcr.io/lennylabs/runtime-chat@sha256:abc123") {
		t.Error("hasPlaceholderDigest should not flag a real digest")
	}
	pinned := placeholderPinnedRuntimes()
	if len(pinned) != len(referenceRuntimes) {
		t.Errorf("every catalog entry is placeholder-pinned today: got %d of %d", len(pinned), len(referenceRuntimes))
	}
}
