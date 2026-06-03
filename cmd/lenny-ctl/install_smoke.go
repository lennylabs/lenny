// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	lenny "github.com/lennylabs/lenny/sdks/client/go/lenny"
)

// install_smoke.go implements the post-install smoke-test phase of the
// §24.20 installation wizard: "runs a smoke test against the chat
// reference runtime" (§24.20 line 299). The phase runs after `helm
// install` in the wizard's phase order (detection → question → preview →
// preflight → helm install → bootstrap seed → smoke test, §24.20 line
// 295). It polls the gateway /healthz endpoint until the gateway reports
// ready, then issues a lenny/create_session MCP round-trip against the
// chat runtime and asserts a non-error response. On failure it prints the
// rollback procedure so a broken install is not reported as successful.
// F-24.20.4.

// defaultSmokeRuntime is the reference runtime the smoke test exercises
// (§24.20 line 299). It is fixed to the §26 chat runtime.
const defaultSmokeRuntime = "chat"

// defaultSmokeHealthTimeout bounds the /healthz poll loop (§24.20 line 299
// suggested-resolution: "polls /healthz for up to 120s").
const defaultSmokeHealthTimeout = 120 * time.Second

// defaultSmokePollInterval is the gap between /healthz attempts.
const defaultSmokePollInterval = 3 * time.Second

// smokeTarget is the resolved input to the smoke-test phase.
type smokeTarget struct {
	// gatewayURL is the base URL of the freshly installed gateway. Empty
	// means the wizard could not determine a reachable URL and the phase
	// is skipped rather than failed.
	gatewayURL string
	// runtime is the reference runtime to exercise (chat).
	runtime string
	// token is the bearer for the MCP round-trip. Empty restricts the
	// phase to the /healthz probe, because lenny/create_session requires
	// authentication and a fresh install may not yet expose an admin
	// token (F-17.6.3).
	token string
	// userID is the subject the smoke session is created for.
	userID string
	// healthTimeout bounds the /healthz poll loop.
	healthTimeout time.Duration
	// pollInterval is the gap between /healthz attempts.
	pollInterval time.Duration
}

// rollbackInfo names the release the smoke test would roll back on
// failure.
type rollbackInfo struct {
	release   string
	namespace string
}

// smokeTester runs the smoke-test phase against a resolved target. It is
// an interface so cmdInstall drives the real HTTP/MCP implementation while
// tests inject a fake. spec: §24.20 line 299. F-24.20.4.
type smokeTester interface {
	run(ctx context.Context, tgt smokeTarget, stdout, stderr io.Writer) error
}

// smokeTargetFromAnswers resolves the smoke-test target from the wizard
// answers and the environment. The gateway URL prefers LENNY_API_URL (so
// an operator can point the probe at a port-forward or internal address)
// and otherwise derives https://<domain> from the gateway Ingress host.
// The token comes from LENNY_API_TOKEN; absent, the phase runs health-only.
// F-24.20.4.
func smokeTargetFromAnswers(a installAnswers) smokeTarget {
	url := os.Getenv("LENNY_API_URL")
	if url == "" && a.Domain != "" {
		url = "https://" + a.Domain
	}
	return smokeTarget{
		gatewayURL:    url,
		runtime:       defaultSmokeRuntime,
		token:         os.Getenv("LENNY_API_TOKEN"),
		userID:        "lenny-install-smoke",
		healthTimeout: defaultSmokeHealthTimeout,
		pollInterval:  defaultSmokePollInterval,
	}
}

// runSmokeTest executes the smoke-test phase, printing progress and, on
// failure, the rollback procedure. A target with no gateway URL is a skip
// (return 0): the install succeeded and there is no reachable endpoint to
// probe. A probe or round-trip failure returns 1 so the wizard does not
// report a broken install as successful. spec: §24.20 line 299. F-24.20.4.
func runSmokeTest(ctx context.Context, t smokeTester, tgt smokeTarget, rb rollbackInfo, stdout, stderr io.Writer) int {
	if tgt.gatewayURL == "" {
		fmt.Fprintln(stdout, "# Smoke test: skipped (no gateway URL; set LENNY_API_URL or a gateway domain to enable)")
		return 0
	}
	fmt.Fprintf(stdout, "# Smoke test: probing %s/healthz and exercising the %q runtime\n", tgt.gatewayURL, tgt.runtime)
	if err := t.run(ctx, tgt, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "lenny-ctl install: smoke test failed: %v\n", err)
		printRollback(stderr, rb)
		return 1
	}
	fmt.Fprintln(stdout, "# Smoke test: passed")
	return 0
}

// printRollback writes the rollback procedure the operator runs when the
// smoke test fails after `helm install` mutated the cluster. spec: §24.20
// line 299 suggested-resolution ("on failure, print the rollback
// procedure"). F-24.20.4.
func printRollback(w io.Writer, rb rollbackInfo) {
	fmt.Fprintln(w, "lenny-ctl install: the release was installed but the smoke test did not pass.")
	fmt.Fprintln(w, "  To roll back the release:")
	fmt.Fprintf(w, "    helm uninstall %s --namespace %s\n", rb.release, rb.namespace)
	fmt.Fprintln(w, "  Then inspect the gateway before retrying:")
	fmt.Fprintf(w, "    kubectl --namespace %s get pods\n", rb.namespace)
	fmt.Fprintf(w, "    kubectl --namespace %s logs deploy/%s-gateway\n", rb.namespace, rb.release)
}

// httpSmokeTester is the production smoke-test implementation. It polls
// /healthz with its HTTP client and, when a token is present, runs the MCP
// lenny/create_session round-trip through the Go client SDK. The session
// hook is injectable so a test can drive the orchestration without a live
// MCP endpoint. F-24.20.4.
type httpSmokeTester struct {
	httpClient *http.Client
	// session overrides the SDK round-trip in tests. Nil uses sdkCreateSession.
	session func(ctx context.Context, tgt smokeTarget) error
}

// run polls /healthz to the deadline, then runs the MCP round-trip when a
// token is available. spec: §24.20 line 299. F-24.20.4.
func (h *httpSmokeTester) run(ctx context.Context, tgt smokeTarget, stdout, stderr io.Writer) error {
	if err := h.waitHealthy(ctx, tgt, stdout); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "#   gateway /healthz is ready")
	if tgt.token == "" {
		fmt.Fprintln(stdout, "#   MCP round-trip skipped (no admin token; set LENNY_API_TOKEN to exercise create_session)")
		return nil
	}
	sess := h.session
	if sess == nil {
		sess = sdkCreateSession
	}
	if err := sess(ctx, tgt); err != nil {
		return fmt.Errorf("create_session against %q runtime: %w", tgt.runtime, err)
	}
	fmt.Fprintf(stdout, "#   create_session against %q runtime succeeded\n", tgt.runtime)
	return nil
}

// waitHealthy polls GET {gatewayURL}/healthz until it returns a 2xx or the
// timeout elapses. A non-2xx or transport error is retried on the poll
// interval; the deadline turns into a failure so an unreachable gateway is
// reported. F-24.20.4.
func (h *httpSmokeTester) waitHealthy(ctx context.Context, tgt smokeTarget, stdout io.Writer) error {
	client := h.httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	interval := tgt.pollInterval
	if interval <= 0 {
		interval = defaultSmokePollInterval
	}
	timeout := tgt.healthTimeout
	if timeout <= 0 {
		timeout = defaultSmokeHealthTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		code, err := probeHealthz(ctx, client, tgt.gatewayURL)
		if err == nil && code >= 200 && code < 300 {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("/healthz returned HTTP %d", code)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("gateway did not become healthy within %s: %w", timeout, lastErr)
		case <-time.After(interval):
		}
	}
}

// probeHealthz issues a single GET /healthz and returns the status code.
func probeHealthz(ctx context.Context, client *http.Client, baseURL string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// sdkCreateSession runs the lenny/create_session MCP round-trip through the
// Go client SDK, the same path `lenny session new` uses. F-24.20.4.
func sdkCreateSession(ctx context.Context, tgt smokeTarget) error {
	client, err := lenny.New(tgt.gatewayURL, lenny.WithAuth(lenny.BearerToken(tgt.token)))
	if err != nil {
		return err
	}
	_, err = client.MCP().CreateSession(ctx, tgt.runtime, tgt.userID)
	return err
}
