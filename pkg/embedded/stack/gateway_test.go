// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// argValue returns the value following flag in args, or "" when the
// flag is absent.
func argValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func TestGatewayArgsBaseFlags(t *testing.T) {
	args := gatewayArgs(gatewaySpec{HTTPAddr: "127.0.0.1:8080"})
	joined := strings.Join(args, " ")
	for _, want := range []string{"-dev-mode", "-multi-tenant", "-addr 127.0.0.1:8080"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
}

func TestGatewayArgsOmitsBearerTrustWhenNoKeyFile(t *testing.T) {
	args := gatewayArgs(gatewaySpec{HTTPAddr: "127.0.0.1:8080"})
	if _, ok := argValue(args, "-bearer-trust-hmac-key-file"); ok {
		t.Error("gatewayArgs passed -bearer-trust-hmac-key-file without an OIDC key file")
	}
}

func TestGatewayArgsPassesBearerTrustKeyFile(t *testing.T) {
	const keyFile = "/home/alice/.lenny/oidc/signing.key"
	args := gatewayArgs(gatewaySpec{HTTPAddr: "127.0.0.1:8080", OIDCKeyFile: keyFile})
	got, ok := argValue(args, "-bearer-trust-hmac-key-file")
	if !ok {
		t.Fatal("gatewayArgs did not pass -bearer-trust-hmac-key-file")
	}
	if got != keyFile {
		t.Errorf("-bearer-trust-hmac-key-file = %q, want %q", got, keyFile)
	}
}

// TestGatewayArgsThreadsGRPCAddr covers the §4.7/§8.6/§9.1 GatewayControl
// listener bind: when the stack computes the gRPC listen address (an empty
// host so the listener binds all host interfaces, reachable from the Docker
// VM), gatewayArgs threads it through -grpc-addr so the in-cluster agent-pod
// adapter can dial the gateway callback across the host/Docker boundary. The
// listener is enabled only when an embedded cluster exists to host the
// adapter that dials it.
//
// spec: §4.7, §8.6, §9.1, §17.4 (the gateway serves the adapter→gateway
// control surface the in-cluster adapter dials across the host/Docker
// boundary).
func TestGatewayArgsThreadsGRPCAddr_spec_4_7(t *testing.T) {
	const addr = ":50061"
	args := gatewayArgs(gatewaySpec{HTTPAddr: "127.0.0.1:8080", GRPCAddr: addr})
	got, ok := argValue(args, "-grpc-addr")
	if !ok {
		t.Fatal("gatewayArgs did not pass -grpc-addr when the spec set it")
	}
	if got != addr {
		t.Errorf("-grpc-addr = %q, want %q", got, addr)
	}
}

// TestGatewayArgsAllowsInsecureRedis covers the §12.4/§17.4 Redis AUTH-and-TLS
// opt-out: the §17.4 embedded Redis is the loopback-only miniredis exempt from
// the production AUTH and TLS invariant and emits a passwordless, plaintext
// redis:// URL, so the embedded gateway must pass -redis-allow-insecure.
// Without it redisconn fails closed (ErrAuthRequired) and the gateway never
// becomes healthy, which is the integration defect this asserts against. The
// flag carries no value, so it is checked by membership rather than argValue.
//
// spec: §12.4 (Redis AUTH and TLS are required on every Redis instance), §17.4
// (Embedded Mode Redis is exempt and runs loopback-only).
func TestGatewayArgsAllowsInsecureRedis_spec_12_4(t *testing.T) {
	args := gatewayArgs(gatewaySpec{HTTPAddr: "127.0.0.1:8080"})
	found := false
	for _, a := range args {
		if a == "-redis-allow-insecure" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("gatewayArgs %q omits -redis-allow-insecure; the embedded gateway "+
			"would fail the §12.4 AUTH-and-TLS startup invariant against the "+
			"passwordless embedded Redis", strings.Join(args, " "))
	}
}

// TestGatewayArgsOmitsGRPCAddrWhenUnset confirms an empty GRPCAddr leaves
// the GatewayControl listener disabled, so a stack without an embedded
// cluster (no in-cluster adapter to serve) does not bind the listener.
//
// spec: §4.7 (the GatewayControl listener is bound only when there is an
// in-cluster adapter to dial it).
func TestGatewayArgsOmitsGRPCAddrWhenUnset(t *testing.T) {
	args := gatewayArgs(gatewaySpec{HTTPAddr: "127.0.0.1:8080"})
	if _, ok := argValue(args, "-grpc-addr"); ok {
		t.Error("gatewayArgs passed -grpc-addr with no address set")
	}
}

// envValue returns the value of the last KEY=VALUE entry for key, or ""
// when absent. Last wins, matching exec's later-entry precedence.
func envValue(env []string, key string) (string, bool) {
	val, ok := "", false
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") {
			val, ok = strings.TrimPrefix(e, key+"="), true
		}
	}
	return val, ok
}

// spec: §17.4 line 163 / F-17.4.7 — the embedded gateway is pointed at
// the file-backed soft-HSM master key so encrypted state survives a
// restart.
func TestGatewayEnvPassesKMSMasterKeyFile_spec_17_4_163(t *testing.T) {
	const path = "/home/alice/.lenny/kms/master.key"
	env := gatewayEnv(gatewaySpec{KMSMasterKeyFile: path}, nil)
	got, ok := envValue(env, "LENNY_KMS_MASTER_KEY_FILE")
	if !ok || got != path {
		t.Fatalf("LENNY_KMS_MASTER_KEY_FILE = %q (set=%v), want %q", got, ok, path)
	}
}

// spec: §17.4 line 165 / F-17.4.8 — the embedded gateway selects the
// local-filesystem object store rooted at the artifacts directory.
func TestGatewayEnvSelectsFilesystemArtifactStore_spec_17_4_165(t *testing.T) {
	const dir = "/home/alice/.lenny/artifacts"
	env := gatewayEnv(gatewaySpec{ArtifactsDir: dir}, nil)
	if got, ok := envValue(env, "LENNY_OBJECT_STORAGE_PROVIDER"); !ok || got != "filesystem" {
		t.Fatalf("LENNY_OBJECT_STORAGE_PROVIDER = %q (set=%v), want filesystem", got, ok)
	}
	if got, ok := envValue(env, "LENNY_OBJECT_STORAGE_FILESYSTEM_ROOT"); !ok || got != dir {
		t.Fatalf("LENNY_OBJECT_STORAGE_FILESYSTEM_ROOT = %q (set=%v), want %q", got, ok, dir)
	}
}

// TestStartGatewayLaunchesChild covers startGateway: the launch path
// assembles the gateway argv and env from the spec and starts the child
// process. It points BinPath at a parked sleeper binary so the process
// actually starts without needing the real gateway binary or the embedded
// backends, then tears it down through the returned handle.
//
// spec: §17.4 (Embedded Mode supervises the production gateway as a managed
// child process).
func TestStartGatewayLaunchesChild_spec_17_4(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	gw, err := startGateway(gatewaySpec{
		BinPath:  self,
		HTTPAddr: "127.0.0.1:0",
		LogPath:  t.TempDir() + "/gateway.log",
	})
	if err != nil {
		t.Fatalf("startGateway: %v", err)
	}
	t.Cleanup(func() { _ = gw.Stop() })
	if gw.PID() <= 0 {
		t.Fatalf("startGateway returned a handle with non-positive PID %d", gw.PID())
	}
}

// TestProbeHealthz covers probeHealthz against the gateway liveness
// endpoint: a 2xx answer returns nil, a >=300 answer returns an error
// naming the status code, and an unreachable address returns the dial
// error. This is the per-attempt probe waitGatewayHealthy loops on.
//
// spec: §24.19 (the gateway health probe).
func TestProbeHealthz(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		if err := probeHealthz(context.Background(), srv.URL); err != nil {
			t.Errorf("probeHealthz on a 200 endpoint = %v, want nil", err)
		}
	})
	t.Run("unhealthy_status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		if err := probeHealthz(context.Background(), srv.URL); err == nil {
			t.Error("probeHealthz on a 503 endpoint = nil, want an error")
		}
	})
	t.Run("dial_error", func(t *testing.T) {
		// A closed server: the address is reserved-but-refused, so the GET
		// fails to connect rather than answering.
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close()
		if err := probeHealthz(context.Background(), url); err == nil {
			t.Error("probeHealthz against a closed server = nil, want a dial error")
		}
	})
}

// TestWaitGatewayHealthyReturnsWhenHealthy covers the happy path of the
// liveness wait: an endpoint that answers 2xx makes the loop return nil
// before the timeout.
//
// spec: §24.19, §17.4 (lenny up waits for a healthy gateway before
// reporting the stack ready).
func TestWaitGatewayHealthyReturnsWhenHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := waitGatewayHealthy(context.Background(), srv.URL, 5*time.Second); err != nil {
		t.Errorf("waitGatewayHealthy on a healthy gateway = %v, want nil", err)
	}
}

// TestWaitGatewayHealthyTimesOut covers the deadline path: an endpoint
// that never answers 2xx makes the loop return a timeout error wrapping
// the last probe failure. This is the path the embedded smoke failure
// surfaced — a gateway that never becomes healthy makes lenny up fail.
//
// spec: §24.19, §17.4.
func TestWaitGatewayHealthyTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	err := waitGatewayHealthy(context.Background(), srv.URL, 1500*time.Millisecond)
	if err == nil {
		t.Fatal("waitGatewayHealthy against a never-healthy gateway = nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "did not become healthy") {
		t.Errorf("error = %q, want it to name the unhealthy gateway", err)
	}
}

// TestWaitGatewayHealthyHonorsContextCancel covers the cancellation path:
// a cancelled context makes the wait return the context error rather than
// spinning until the timeout.
//
// spec: §24.19 (the bring-up honors cancellation).
func TestWaitGatewayHealthyHonorsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitGatewayHealthy(ctx, srv.URL, 10*time.Second); err == nil {
		t.Error("waitGatewayHealthy with a cancelled context = nil, want the context error")
	}
}

// TestResolveBin covers resolveBin and its two named wrappers
// (resolveGatewayBin, resolveControllerBin): an explicit path to a real
// file resolves to that path, an explicit path that does not exist
// errors, and a sibling binary that does not exist anywhere errors with a
// build hint.
func TestResolveBin(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "lenny-gateway")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Run("explicit_existing", func(t *testing.T) {
		got, err := resolveGatewayBin(bin)
		if err != nil || got != bin {
			t.Errorf("resolveGatewayBin(%q) = (%q, %v), want (%q, nil)", bin, got, err, bin)
		}
	})
	t.Run("explicit_missing", func(t *testing.T) {
		if _, err := resolveControllerBin(filepath.Join(dir, "does-not-exist")); err == nil {
			t.Error("resolveControllerBin on a missing explicit path = nil, want an error")
		}
	})
	t.Run("not_found_anywhere", func(t *testing.T) {
		// A name no sibling/cwd/PATH lookup resolves yields the build hint.
		_, err := resolveBin("", "lenny-nonexistent-binary-xyz")
		if err == nil {
			t.Fatal("resolveBin for a missing sibling = nil, want an error")
		}
		if !strings.Contains(err.Error(), "go build") {
			t.Errorf("error = %q, want it to carry the build hint", err)
		}
	})
}

// The dev-mode and embedded-mode gates are always present; the
// persistence env vars are omitted when their spec fields are empty so a
// non-embedded caller is unaffected.
func TestGatewayEnvDefaults(t *testing.T) {
	env := gatewayEnv(gatewaySpec{}, nil)
	if got, ok := envValue(env, "LENNY_DEV_MODE"); !ok || got != "true" {
		t.Fatalf("LENNY_DEV_MODE = %q (set=%v), want true", got, ok)
	}
	if _, ok := envValue(env, "LENNY_KMS_MASTER_KEY_FILE"); ok {
		t.Error("LENNY_KMS_MASTER_KEY_FILE set without a key file")
	}
	if _, ok := envValue(env, "LENNY_OBJECT_STORAGE_PROVIDER"); ok {
		t.Error("LENNY_OBJECT_STORAGE_PROVIDER set without an artifacts dir")
	}
}
