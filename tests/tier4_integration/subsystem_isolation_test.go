// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: drives the §4.1 gateway per-subsystem
// isolation guarantee against the cmd/lenny-gateway subprocess. It
// saturates the real Upload Handler subsystem's per-replica concurrency
// limiter and asserts two things the §4.1 partial-degradation contract
// requires: a saturated Upload Handler sheds further uploads with 503
// SUBSYSTEM_UNAVAILABLE, and the Stream Proxy plus other non-upload
// request paths keep serving normally (they are not starved). It reads
// the §16.1 per-subsystem metrics to confirm the saturation is observed
// (lenny_upload_queue_depth) and that the per-subsystem circuit-state
// series is exported.
//
// This exercises the real gateway wiring (pkg/gateway/core/subsystem +
// pkg/gateway/sessionserver/upload.go acquireUploadSlot) rather than the
// in-process worker-pool model in
// tests/tier7a_load_local/scenarios/bulkhead_thread_pool_isolation or
// the unrelated §11.6 operator-managed circuit breaker.
package tier4_integration_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// isolationTenant is the dev-header tenant every request in this test
// authenticates as; the gateway boots with --no-environment-policy
// allow-all so a created session needs no provisioned environment.
const isolationTenant = "acme"

// spec: §4.1 (Per-subsystem isolation guarantees) — "A saturated Upload
// Handler cannot consume goroutines needed by the Stream Proxy." and
// "The Upload Handler can trip to half-open or open state — returning
// 503 for uploads — while the Stream Proxy and MCP Fabric continue
// serving normally. This is the primary mechanism for partial gateway
// degradation."
//
// diagnosis: a failure here means the gateway's §4.1 Upload Handler
// subsystem boundary does not isolate upload saturation from the other
// subsystems. Either a saturated Upload Handler fails to shed load with
// 503 SUBSYSTEM_UNAVAILABLE (the acquireUploadSlot gate in
// pkg/gateway/sessionserver/upload.go over pkg/gateway/core/subsystem's
// Limiter), or upload saturation starves the Stream Proxy / non-upload
// paths so they hang or 503 too, or the §16.1 per-subsystem metrics do
// not reflect the in-flight saturation.
func TestUploadHandlerSaturationDoesNotStarveOtherSubsystems(t *testing.T) {
	// Pin the §4.1 Upload Handler concurrency limit to 1 so a single
	// in-flight upload saturates the limiter deterministically. The
	// gateway reads this from the LENNY_EXTRACTION_THRESHOLD_* env
	// (pkg/gateway/metrics/extractionthreshold), the same knob the
	// gateway.extractionThresholds.uploadHandler.activeConcurrent Helm
	// value sets. Set before Start so the spawned subprocess inherits it.
	t.Setenv("LENNY_EXTRACTION_THRESHOLD_UPLOAD_HANDLER_ACTIVE_CONCURRENT", "1")

	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")
	base := gw.BaseURL()

	// The session whose in-flight slow upload will hold the single
	// Upload Handler slot.
	holderID, holderToken := createIsolationSession(t, base)

	// Start a slow upload: the request body is an io.Pipe the test never
	// finishes writing, so the handler blocks streaming the body while
	// it holds the one Upload Handler concurrency slot. The slot is
	// acquired at handler entry and released only when the request
	// completes, so it stays held for the whole in-flight request.
	holdCtx, cancelHold := context.WithCancel(context.Background())
	defer cancelHold()
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	holdDone := make(chan struct{})
	go func() {
		defer close(holdDone)
		req, _ := http.NewRequestWithContext(holdCtx, http.MethodPost,
			base+"/v1/sessions/"+holderID+"/upload", pr)
		req.Header.Set("X-Lenny-Tenant-ID", isolationTenant)
		req.Header.Set("X-Lenny-Upload-Token", holderToken)
		req.Header.Set("Content-Type", "application/octet-stream")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	// Push one byte so the transport flushes the request and the server
	// starts reading the body, then leave the pipe open so the read
	// blocks and the slot stays held.
	go func() { _, _ = pw.Write([]byte("x")) }()

	// Wait until the §16.1 lenny_upload_queue_depth gauge shows the
	// in-flight upload: the Upload Handler is now saturated. The gauge is
	// updated synchronously as the request enters the subsystem.
	waitUploadQueueDepth(t, base, 1, 10*time.Second)

	// A second upload must shed load with 503 SUBSYSTEM_UNAVAILABLE
	// rather than queue behind the held slot. It targets a fresh session
	// so the §11.1 per-session upload cap cannot be the rejecting gate;
	// the §4.1 subsystem gate is evaluated first regardless.
	otherID, otherToken := createIsolationSession(t, base)
	status, code := doIsolationUpload(t, base, otherID, otherToken, "second upload")
	if status != http.StatusServiceUnavailable || code != "SUBSYSTEM_UNAVAILABLE" {
		t.Fatalf("saturated Upload Handler: want 503 SUBSYSTEM_UNAVAILABLE, got %d %q", status, code)
	}

	// The Stream Proxy keeps serving while the Upload Handler is
	// saturated: opening the session event stream returns 200 promptly.
	assertStreamProxyHealthy(t, base, holderID)

	// A plain non-upload request also serves normally (not starved).
	if got := isolationSessionGet(t, base, holderID); got != http.StatusOK {
		t.Fatalf("GET session while Upload Handler saturated: want 200, got %d", got)
	}

	// The §16.1 metrics reflect the saturation and the isolation: the
	// upload-handler queue-depth gauge is non-zero while the slow upload
	// holds the slot, and the per-subsystem circuit-state series is
	// exported. Pure concurrency saturation sheds load through the
	// limiter without tripping the breaker, so the circuit state stays
	// closed (0) — the 503 is back-pressure, not a downstream outage.
	metricsText := scrapeMetrics(t, base)
	if !strings.Contains(metricsText, `lenny_gateway_subsystem_circuit_state{subsystem="upload_handler"}`) {
		t.Fatalf("lenny_gateway_subsystem_circuit_state{subsystem=\"upload_handler\"} not exported in /metrics")
	}
	if v, ok := unlabeledGauge(metricsText, "lenny_upload_queue_depth"); !ok || v < 1 {
		t.Fatalf("lenny_upload_queue_depth = %v (present=%v) while a slow upload holds the slot, want >= 1", v, ok)
	}

	// Release the held slot; the Upload Handler recovers and admits a
	// fresh upload, confirming the 503 was transient back-pressure
	// isolated to the Upload Handler rather than a broken subsystem.
	cancelHold()
	_ = pw.Close()
	<-holdDone
	waitUploadQueueDepth(t, base, 0, 10*time.Second)

	recoverID, recoverToken := createIsolationSession(t, base)
	status, code = doIsolationUpload(t, base, recoverID, recoverToken, "recovery upload")
	if status != http.StatusCreated {
		t.Fatalf("after releasing the held slot, upload should recover: want 201, got %d (code %q)", status, code)
	}
}

// createIsolationSession creates a session and returns its id and the
// single-use §7.1 uploadToken the create response mints.
func createIsolationSession(t *testing.T, base string) (id, uploadToken string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/sessions",
		strings.NewReader(`{"runtimeRef":"claude-code","userId":"alice@acme.com"}`))
	req.Header.Set("X-Lenny-Tenant-ID", isolationTenant)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create session: want 201, got %d (body %s)", resp.StatusCode, body)
	}
	var created struct {
		ID          string `json:"id"`
		UploadToken string `json:"uploadToken"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("create session: empty id (body %s)", body)
	}
	return created.ID, created.UploadToken
}

// doIsolationUpload issues a small POST /v1/sessions/{id}/upload and
// returns the HTTP status plus the error envelope code (empty on 2xx).
func doIsolationUpload(t *testing.T, base, id, token, content string) (status int, errCode string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/sessions/"+id+"/upload",
		strings.NewReader(content))
	req.Header.Set("X-Lenny-Tenant-ID", isolationTenant)
	req.Header.Set("X-Lenny-Upload-Token", token)
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload %s: %v", content, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &env)
		return resp.StatusCode, env.Error.Code
	}
	return resp.StatusCode, ""
}

// assertStreamProxyHealthy opens the §15.1 session event stream (the
// §4.1 Stream Proxy subsystem) and fails unless it returns 200 within a
// short budget, proving the Stream Proxy is not starved by the saturated
// Upload Handler.
func assertStreamProxyHealthy(t *testing.T, base, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/sessions/"+id+"/events", nil)
	req.Header.Set("X-Lenny-Tenant-ID", isolationTenant)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream proxy attach starved while Upload Handler saturated: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("stream proxy attach while Upload Handler saturated: want 200, got %d (body %s)", resp.StatusCode, body)
	}
}

// isolationSessionGet issues GET /v1/sessions/{id} and returns the
// status, exercising a non-upload path that must serve normally while
// the Upload Handler is saturated.
func isolationSessionGet(t *testing.T, base, id string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/sessions/"+id, nil)
	req.Header.Set("X-Lenny-Tenant-ID", isolationTenant)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET session starved while Upload Handler saturated: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// waitUploadQueueDepth polls /metrics until lenny_upload_queue_depth
// equals want (or the deadline expires). The gauge is set synchronously
// as an upload enters or leaves the §4.1 Upload Handler subsystem.
func waitUploadQueueDepth(t *testing.T, base string, want float64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last float64
	var seen bool
	for time.Now().Before(deadline) {
		text := scrapeMetrics(t, base)
		if v, ok := unlabeledGauge(text, "lenny_upload_queue_depth"); ok {
			last, seen = v, true
			if v == want {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("lenny_upload_queue_depth did not reach %v within %s (last=%v seen=%v)", want, timeout, last, seen)
}

// scrapeMetrics returns the gateway /metrics exposition text.
func scrapeMetrics(t *testing.T, base string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/metrics", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("scrape /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scrape /metrics: want 200, got %d", resp.StatusCode)
	}
	return string(body)
}

// unlabeledGauge parses the value of a Prometheus gauge emitted with no
// labels (a bare "name value" line). It skips the HELP/TYPE comment
// lines and any labeled series of the same family.
func unlabeledGauge(metricsText, name string) (float64, bool) {
	sc := bufio.NewScanner(strings.NewReader(metricsText))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	prefix := name + " "
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		return v, true
	}
	return 0, false
}
