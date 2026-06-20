// SPDX-License-Identifier: MIT

package stack

import (
	"strings"
	"testing"
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
