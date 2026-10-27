// SPDX-License-Identifier: MIT

package k3s

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// apiready.go holds the API server readiness wait both launchers run after
// the admin kubeconfig exists. k3s writes its admin kubeconfig before the
// embedded kube-apiserver serves requests: in the interval between the two,
// the k3s supervisor answers every API request with HTTP 503 and the body
// "starting" or "apiserver not ready". A warm restart is worse, because the
// kubeconfig from the earlier run is already on disk (the Linux data
// directory) or inside the persisted container, so a file-presence check
// returns at once. The Launcher contract is that Start returns once the API
// server is reachable, and the CRD install that follows Start fails on the
// first 503 it receives. Start therefore polls the API server's /readyz
// endpoint through the kubeconfig until it answers 200, bounded by the same
// ReadyTimeout deadline as the kubeconfig wait.
//
// spec: §17.4 (lenny up brings the embedded k3s up and installs the CRDs
// against it; a warm lenny up restarts the persisted substrate).

// apiReadyPollInterval is the delay between /readyz probes. It is a var so
// a unit test can shorten it.
var apiReadyPollInterval = time.Second

// apiReadyProbeTimeout bounds each /readyz round trip, so a probe against a
// published port that accepts the connection but never answers does not
// consume the whole readiness budget in one call.
const apiReadyProbeTimeout = 5 * time.Second

// apiReadyFunc waits until the API server named by the kubeconfig at path
// is ready, or deadline passes. It is the injection point the launchers
// carry so unit tests that fake the substrate do not probe a real server.
type apiReadyFunc func(ctx context.Context, kubeconfigPath string, deadline time.Time) error

// waitAPIServerReady polls GET /readyz on the API server the kubeconfig at
// kubeconfigPath names, authenticating with the kubeconfig's credentials,
// until the server answers 200 or deadline passes. Every failure before the
// deadline (an unreadable kubeconfig, a refused connection, a 503 from the
// k3s supervisor) is treated as not-ready-yet and retried, because each is
// a normal state while k3s bootstraps. The error returned at the deadline
// carries the last observed failure so the operator sees why the server
// never became ready.
func waitAPIServerReady(ctx context.Context, kubeconfigPath string, deadline time.Time) error {
	var last error
	for {
		last = probeAPIServerReady(ctx, kubeconfigPath)
		if last == nil {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("embedded k3s: API server not ready by the readiness deadline: %w", last)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("embedded k3s: wait for API server readiness: %w", ctx.Err())
		case <-time.After(apiReadyPollInterval):
		}
	}
}

// probeAPIServerReady issues one bounded GET /readyz and returns nil only
// on HTTP 200. A non-200 answer returns an error naming the status and the
// response body, which is where the k3s supervisor reports "starting" or
// "apiserver not ready".
func probeAPIServerReady(ctx context.Context, kubeconfigPath string) error {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("load kubeconfig %s: %w", kubeconfigPath, err)
	}
	cfg.Timeout = apiReadyProbeTimeout
	client, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return fmt.Errorf("build API server client: %w", err)
	}
	reqCtx, cancel := context.WithTimeout(ctx, apiReadyProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(cfg.Host, "/")+"/readyz", nil)
	if err != nil {
		return fmt.Errorf("build /readyz request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET /readyz: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /readyz: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
