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
	// PostgresDSN and RedisURL point the gateway at the embedded
	// backends through its standard configuration flags.
	PostgresDSN string
	RedisURL    string
	// Kubeconfig is the embedded k3s admin kubeconfig. Empty when the
	// embedded cluster did not start.
	Kubeconfig string
	// LogPath is the gateway log file.
	LogPath string
}

// startGateway launches the production gateway configured against the
// embedded backends. §17.4: Embedded Mode uses the production gateway;
// only the driver selection differs. The gateway is told to use its
// embedded backends through the same --postgres-dsn and --redis-url
// configuration a cluster deployment uses, and dev mode is enabled so
// the §17.4 dev-header auth path and the relaxed-TLS startup are
// active.
func startGateway(spec gatewaySpec) (*managedProcess, error) {
	args := []string{
		"-addr", spec.HTTPAddr,
		"-dev-mode",
		"-multi-tenant",
		// §10.6: dev mode derives allow-all, but passing it explicitly
		// keeps the gateway's startup assertion satisfied regardless of
		// the dev-mode default.
		"-no-environment-policy", "allow-all",
	}
	env := append(
		os.Environ(),
		// LENNY_DEV_MODE is the §17.4 unified security-relaxation gate.
		"LENNY_DEV_MODE=true",
		"LENNY_POSTGRES_DSN="+spec.PostgresDSN,
		"LENNY_REDIS_URL="+spec.RedisURL,
		// LENNY_EMBEDDED_MODE signals the §17.4 mode=embedded platform
		// flag the storage, KMS, and identity interfaces consume to
		// pick their embedded backends.
		"LENNY_EMBEDDED_MODE=true",
	)
	if spec.Kubeconfig != "" {
		env = append(env, "KUBECONFIG="+spec.Kubeconfig)
	}
	return startProcess(processSpec{
		Name:    "gateway",
		BinPath: spec.BinPath,
		Args:    args,
		Env:     env,
		LogPath: spec.LogPath,
	})
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
