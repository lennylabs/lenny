// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// gatewaySpec configures the embedded gateway child process.
type gatewaySpec struct {
	// BinPath is the production lenny-gateway binary.
	BinPath string
	// HTTPAddr is the loopback host:port the gateway serves plaintext
	// HTTP on. §17.4 fronts it with the TLS-terminating proxy.
	HTTPAddr string
	// GRPCAddr is the host:port the gateway's §8.6/§9.1 GatewayControl
	// gRPC listener binds. It is bound on all host interfaces (an empty
	// host, ":<port>") rather than loopback so an in-cluster agent-pod
	// adapter under the Docker VM can reach it across the host/Docker
	// boundary; binding loopback would make it unreachable from the Docker
	// VM. The controller stamps the launcher's externally-reachable address
	// (GatewayHost():<port>) onto pods so the adapter dials the right host.
	// Empty leaves the GatewayControl listener disabled (no embedded
	// cluster, so no in-cluster adapter to serve). spec: §4.7, §8.6, §9.1.
	GRPCAddr string
	// AgentNamespace is the §5 warm-pool/Sandbox namespace the gateway
	// places sessions into. When set, the gateway resolves the warm pool
	// from this namespace and routes every started session onto a warm pod
	// over the §4.7 adapter boundary instead of the in-process echo
	// executor. Empty leaves the gateway on the in-process echo executor
	// (no embedded cluster to place into). spec: §17.4, §4.7.
	AgentNamespace string
	// PostgresDSN and RedisURL point the gateway at the embedded
	// backends through its standard configuration flags.
	PostgresDSN string
	RedisURL    string
	// Kubeconfig is the embedded k3s admin kubeconfig. On the Linux
	// launcher it is k3s' generated kubeconfig; on the Docker-backed
	// launcher (macOS and Windows) it is the host-rewritten kubeconfig
	// whose server URL points at the published host port, so the gateway
	// reaches the in-container API server across the host/Docker boundary.
	// Empty when the embedded cluster did not start.
	Kubeconfig string
	// LogPath is the gateway log file.
	LogPath string
	// OIDCKeyFile is the embedded OIDC provider's persisted HMAC signing
	// key. The gateway is told to trust it as an additional §10.2 Bearer
	// verifier so a token from `lenny token print` is accepted on the
	// Authorization header. Empty leaves the gateway on its Token
	// Service signer alone.
	OIDCKeyFile string
	// KMSMasterKeyFile is the §17.4 file-backed soft-HSM master key
	// (~/.lenny/kms/master.key). Pointing the gateway at it makes the
	// local KEK survive a restart, so credentials encrypted under one
	// `lenny up` still decrypt on the next — the §17.4 line 186 "lenny
	// down without --purge preserves state" guarantee. F-17.4.7.
	KMSMasterKeyFile string
	// ArtifactsDir is the §17.4 local-filesystem object-storage root
	// (~/.lenny/artifacts/). Pointing the gateway at it persists uploaded
	// files and workspace snapshots across a restart instead of losing
	// them with the in-memory store. F-17.4.8.
	ArtifactsDir string
}

// startGateway launches the production gateway configured against the
// embedded backends. §17.4: Embedded Mode uses the production gateway;
// only the driver selection differs. The gateway is told to use its
// embedded backends through the same --postgres-dsn and --redis-url
// configuration a cluster deployment uses, and dev mode is enabled so
// the §17.4 dev-header auth path and the relaxed-TLS startup are
// active.
//
// When spec.OIDCKeyFile is set, the gateway is also told to trust the
// embedded OIDC provider's signing key as an additional §10.2 Bearer
// verifier through --bearer-trust-hmac-key-file, so a bearer minted by
// `lenny token print` verifies on the gateway's Authorization header.
func startGateway(spec gatewaySpec) (*managedProcess, error) {
	return startProcess(processSpec{
		Name:    "gateway",
		BinPath: spec.BinPath,
		Args:    gatewayArgs(spec),
		Env:     gatewayEnv(spec, os.Environ()),
		LogPath: spec.LogPath,
	})
}

// gatewayEnv builds the environment for the embedded gateway child
// process by extending base (typically os.Environ()) with the §17.4
// embedded-mode driver selectors. It is separated from startGateway so
// the env construction is testable without launching a process.
func gatewayEnv(spec gatewaySpec, base []string) []string {
	env := append(
		append([]string(nil), base...),
		// LENNY_DEV_MODE is the §17.4 unified security-relaxation gate.
		"LENNY_DEV_MODE=true",
		"LENNY_POSTGRES_DSN="+spec.PostgresDSN,
		"LENNY_REDIS_URL="+spec.RedisURL,
		// LENNY_EMBEDDED_MODE signals the §17.4 mode=embedded platform
		// flag the storage, KMS, and identity interfaces consume to
		// pick their embedded backends.
		"LENNY_EMBEDDED_MODE=true",
	)
	if spec.KMSMasterKeyFile != "" {
		// §17.4 line 163 file-backed soft-HSM: the local KEK seed loads
		// from (or is generated into) this file so encrypted state
		// outlives a restart. F-17.4.7.
		env = append(env, "LENNY_KMS_MASTER_KEY_FILE="+spec.KMSMasterKeyFile)
	}
	if spec.ArtifactsDir != "" {
		// §17.4 line 165 local-filesystem object storage: uploads and
		// snapshots persist under this directory across a restart.
		// F-17.4.8.
		env = append(
			env,
			"LENNY_OBJECT_STORAGE_PROVIDER=filesystem",
			"LENNY_OBJECT_STORAGE_FILESYSTEM_ROOT="+spec.ArtifactsDir,
		)
	}
	if spec.Kubeconfig != "" {
		env = append(env, "KUBECONFIG="+spec.Kubeconfig)
	}
	return env
}

// gatewayArgs builds the command-line arguments for the embedded
// gateway child process from spec. It is separated from startGateway
// so the argument construction is testable without launching a
// process.
func gatewayArgs(spec gatewaySpec) []string {
	args := []string{
		"-addr", spec.HTTPAddr,
		"-dev-mode",
		"-multi-tenant",
		// §10.6: dev mode derives allow-all, but passing it explicitly
		// keeps the gateway's startup assertion satisfied regardless of
		// the dev-mode default.
		"-no-environment-policy", "allow-all",
		// §12.4 mandates Redis AUTH and TLS on every Redis instance and the
		// gateway fails closed at startup when neither is present. The §17.4
		// embedded Redis is the loopback-only miniredis that is exempt from
		// that invariant (it emits a passwordless, plaintext redis:// URL),
		// so the embedded gateway must opt out explicitly; -dev-mode does not
		// imply it. Without this the gateway refuses to start (redisconn
		// ErrAuthRequired) and never becomes healthy. spec: §12.4, §17.4
		// (Embedded Mode Redis is exempt from the production AUTH and TLS
		// requirements).
		"-redis-allow-insecure",
	}
	if spec.OIDCKeyFile != "" {
		args = append(args, "-bearer-trust-hmac-key-file", spec.OIDCKeyFile)
	}
	if spec.GRPCAddr != "" {
		// §8.6 GatewayControl listener: serves the adapter→gateway control
		// surface (ExtendLease, platform/connector tool bridges) the
		// in-cluster agent-pod adapter dials across the host/Docker boundary.
		// The mTLS material flags (--adapter-tls-cert/key/ca) are left unset,
		// so the listener serves plaintext — the §4.7 documented
		// local-development path — while the address wiring that crosses the
		// boundary is the same code path production uses. spec: §4.7, §8.6.
		args = append(args, "-grpc-addr", spec.GRPCAddr)
	}
	if spec.AgentNamespace != "" {
		// §4.7 pod placement: with -agent-namespace the gateway resolves the
		// warm pool from this namespace and places each started session on a
		// warm pod via the §4.7 adapter instead of the in-process echo
		// executor (cmd/lenny-gateway/runtime_select.go selects the echo
		// executor only when the flag is unset). Set by the stack solely when
		// the embedded substrate is up; an empty namespace leaves the gateway
		// on the in-process echo executor. spec: §17.4, §4.7.
		args = append(args, "-agent-namespace", spec.AgentNamespace)
	}
	return args
}

// resolveGatewayBin locates the production gateway binary. An explicit
// path is used as given. Otherwise the search looks alongside the
// running lenny binary and on PATH for lenny-gateway.
func resolveGatewayBin(explicit string) (string, error) {
	return resolveBin(explicit, "lenny-gateway")
}

// resolveControllerBin locates the production controller binary.
func resolveControllerBin(explicit string) (string, error) {
	return resolveBin(explicit, "lenny-controller")
}

// resolveBin resolves a sibling binary. It checks, in order: the
// explicit path, the directory holding the running executable, the
// current working directory, and PATH.
func resolveBin(explicit, name string) (string, error) {
	if explicit != "" {
		if fi, err := os.Stat(explicit); err == nil && !fi.IsDir() {
			return explicit, nil
		}
		return "", fmt.Errorf("embedded: %s binary %q not found", name, explicit)
	}
	if self, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(self), name)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand, nil
		}
	}
	if wd, err := os.Getwd(); err == nil {
		cand := filepath.Join(wd, name)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("embedded: %s binary not found alongside lenny or on PATH; "+
		"build it with 'go build ./cmd/%s' or pass its path", name, name)
}

// probeHealthz issues a single GET to the gateway's unauthenticated
// liveness endpoint and returns nil when it answers 2xx.
func probeHealthz(ctx context.Context, baseURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("gateway healthz returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// waitGatewayHealthy polls the gateway liveness endpoint until it
// answers or timeout elapses.
func waitGatewayHealthy(ctx context.Context, baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := probeHealthz(probeCtx, baseURL)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("embedded: gateway did not become healthy within %s: %w", timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
