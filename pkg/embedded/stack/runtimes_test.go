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

// The §17.4 echo warm pool is no longer seeded into the bootstrap payload:
// the no-Postgres development profile registers no PoolScalingController to
// materialize a poolstore row, so the echo SandboxTemplate/SandboxWarmPool
// pair is applied directly (echoPoolObjects). The §5.2 single-pod / §13.2
// cluster-default field mapping the seed used to carry is now asserted on
// echoPoolObjects in agentplacement_test.go (TestEchoPoolObjects*).

// TestBuildBootstrapSeedCarriesNoPool_spec_4_6_2 asserts the §17.4 Embedded
// Mode bootstrap seed carries no pool. The PoolScalingController that would
// materialize a seeded poolstore row is unregistered under the no-Postgres
// development profile, so a seeded pool would never become a SandboxWarmPool
// CRD; the echo pool is applied directly instead. The seed therefore declares
// no pool, only the runtime registry records. spec: §4.6.2 (the
// PoolScalingController is the Postgres→CRD channel), §17.4 (no-Postgres
// development profile).
func TestBuildBootstrapSeedCarriesNoPool_spec_4_6_2(t *testing.T) {
	seed := buildBootstrapSeed("")
	// The seed still registers the echo runtime record; only the pool is
	// dropped. Confirm the echo runtime is present so the removal is scoped.
	var hasEcho bool
	for _, rt := range seed.Runtimes {
		if rt.Name == EchoRuntimeName {
			hasEcho = true
		}
	}
	if !hasEcho {
		t.Error("seed dropped the echo runtime record; only the pool should be removed")
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
		return // unreachable after Fatal; satisfies the nil-deref analyzer
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
		return // unreachable after Fatal; satisfies the nil-deref analyzer
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
