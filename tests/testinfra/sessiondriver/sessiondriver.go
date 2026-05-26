// SPDX-License-Identifier: MIT

// Package sessiondriver is a shared live-session test harness used by
// the tier-8 chaos and tier-9 security suites that need a real gateway
// session driven onto a real warm pod. It spins up (or attaches to) the
// e2e Kind cluster, port-forwards the lenny-gateway Service to a host
// port, and exposes a *Driver whose methods cover the §15.1 session
// REST surface (create, start, message, events stream, terminate).
//
// The harness targets the dev-mode gateway the tier-5 install brings
// up: the in-cluster gateway honours the X-Lenny-Tenant-ID,
// X-Lenny-Roles, and X-Lenny-User-ID dev-header identity, so a test
// drives sessions without minting JWTs. Tests that need a synthetic
// tenant bootstrap one via POST /v1/admin/bootstrap and clean it up in
// t.Cleanup.
//
// Usage:
//
//	d := sessiondriver.New(t)            // installs + forwards on demand
//	defer d.Close()
//	d.BootstrapTenant(ctx, "alice-tenant")
//	sess := d.CreateAndStart(ctx, "alice-tenant", "echo-runtime-sidecar")
//	d.SendMessage(ctx, "alice-tenant", sess.ID, "hello")
//	// ... chaos action / security probe ...
//	d.Terminate(ctx, "alice-tenant", sess.ID)
//
// New() calls kind.InstallLenny under the hood, which skips the calling
// test cleanly when docker / kind / kubectl / the chart / the running
// release are missing. Tests therefore never have to guard themselves
// for the prerequisite tools.
package sessiondriver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// gatewayServicePort is the port the lenny-gateway Service listens on
// (the chart's service template sets port 8080).
const gatewayServicePort = 8080

// defaultRuntime is the runtime ref the harness uses when a test does
// not specify one. agent-workload.yaml deploys echo-runtime-sidecar in
// the lenny-agents namespace; the gateway routes a session against it
// onto the matching warm pool.
const defaultRuntime = "echo-runtime-sidecar"

// EchoRuntimeSidecar is the runtime ref agent-workload.yaml registers
// for the §4.7 sidecar deployment model.
const EchoRuntimeSidecar = "echo-runtime-sidecar"

// EchoRuntimeEmbedded is the runtime ref agent-workload.yaml registers
// for the §4.7 embedded deployment model.
const EchoRuntimeEmbedded = "echo-runtime-embedded"

// Driver is the live-session harness handle. Construct it with New;
// call Close to release the port-forward when done. Driver methods are
// safe to call concurrently from multiple goroutines.
type Driver struct {
	cluster *kind.Cluster
	baseURL string
	stopPF  func()
	hc      *http.Client

	// bootstrappedTenants tracks the synthetic tenants the driver
	// created so Close can DELETE them in best-effort cleanup. The
	// underlying admin endpoint is idempotent on missing rows.
	mu                  sync.Mutex
	bootstrappedTenants map[string]struct{}
}

// Session is the parsed §15.1 POST /v1/sessions response — the subset
// of fields a tier-8/9 test interacts with.
type Session struct {
	ID            string          `json:"id"`
	TenantID      string          `json:"tenantId"`
	UserID        string          `json:"userId,omitempty"`
	RuntimeRef    string          `json:"runtimeRef,omitempty"`
	State         string          `json:"state"`
	UploadToken   string          `json:"uploadToken,omitempty"`
	CreatedAt     string          `json:"createdAt,omitempty"`
	UpdatedAt     string          `json:"updatedAt,omitempty"`
	FailureClass  string          `json:"failureClass,omitempty"`
	WorkspacePlan json.RawMessage `json:"workspacePlan,omitempty"`
}

// MessageResponse is the parsed §15.1 POST /v1/sessions/{id}/messages
// response. The body wraps the §15.4 `delivery_receipt` envelope per
// F-7.2.10.
type MessageResponse struct {
	SessionID       string          `json:"sessionId"`
	DeliveryReceipt DeliveryReceipt `json:"deliveryReceipt"`
	Output          json.RawMessage `json:"output,omitempty"`
}

// DeliveryReceipt mirrors the §15.4 lines 1725-1737 schema returned by
// every send_message call (the MCP tool and the REST endpoint).
type DeliveryReceipt struct {
	MessageID   string `json:"messageId"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	DeliveredAt string `json:"deliveredAt,omitempty"`
	QueueDepth  int    `json:"queueDepth,omitempty"`
}

// Event is one frame from the SSE /v1/sessions/{id}/events stream. The
// driver decodes the SSE envelope (id / event / data) and surfaces them
// alongside the raw data payload.
type Event struct {
	// Seq is the SSE id field, as a uint64. The §15.1 events stream
	// numbers events monotonically per session; the harness uses it as
	// the Last-Event-ID on reconnect.
	Seq uint64
	// Type is the SSE event field — "transcript.entry", "lifecycle",
	// "elicitation", etc.
	Type string
	// Data is the raw JSON payload of the event. Tests json.Unmarshal
	// it into the type they expect.
	Data json.RawMessage
}

// Options configures a New() call. The zero value picks sensible
// defaults: the chart-installed lenny-gateway Service is forwarded, a
// 30s HTTP timeout, and the default echo runtime.
type Options struct {
	// HTTPTimeout caps how long the driver's HTTP client waits for any
	// single request (excluding the event stream, which is unbounded
	// by design). Default 30s.
	HTTPTimeout time.Duration
}

// New constructs a Driver bound to the e2e Kind cluster. It calls
// kind.InstallLenny under the hood (which skips the calling test when
// the cluster is not up), port-forwards the gateway Service, and
// registers a t.Cleanup that runs Close. Tests that need to keep the
// driver alive across cleanup boundaries call NewKept instead.
func New(t testing.TB, opts ...Options) *Driver {
	t.Helper()
	d := NewKept(t, opts...)
	t.Cleanup(d.Close)
	return d
}

// NewKept is New without the t.Cleanup registration. It is for the rare
// caller that needs Close to run in a specific order relative to other
// cleanups (for example, a chaos test that explicitly closes the
// driver before re-asserting cluster health).
func NewKept(t testing.TB, opts ...Options) *Driver {
	t.Helper()
	o := Options{}
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.HTTPTimeout == 0 {
		o.HTTPTimeout = 30 * time.Second
	}

	c := kind.InstallLenny(t)
	baseURL, stop := c.PortForward(t, "svc/lenny-gateway", "lenny-system", gatewayServicePort)

	d := &Driver{
		cluster:             c,
		baseURL:             baseURL,
		stopPF:              stop,
		hc:                  &http.Client{Timeout: o.HTTPTimeout},
		bootstrappedTenants: make(map[string]struct{}),
	}
	// Confirm the port-forward terminates at a healthy gateway before
	// the first call. The kind.PortForward helper already waits for the
	// kubectl listener to bind, but that does not prove the gateway pod
	// itself is responsive. A 200 from /v1/admin/health is the right
	// readiness signal (it does not require auth in dev mode).
	if err := d.waitGatewayReady(context.Background(), 30*time.Second); err != nil {
		t.Fatalf("sessiondriver: gateway not ready at %s: %v", baseURL, err)
	}
	return d
}

// BaseURL returns the http://127.0.0.1:<port> the gateway is reachable
// at on the host. Tests that need raw HTTP access (for a chaos probe
// that simulates a partial-write failure, for example) use this with
// a custom http.Client.
func (d *Driver) BaseURL() string { return d.baseURL }

// Cluster returns the underlying kind.Cluster handle so tests can run
// kubectl reads (a chaos test that deletes the agent pod uses the
// handle directly).
func (d *Driver) Cluster() *kind.Cluster { return d.cluster }

// Close releases the port-forward and best-effort deletes the
// synthetic tenants the driver bootstrapped. Safe to call multiple
// times; subsequent calls are no-ops.
func (d *Driver) Close() {
	d.mu.Lock()
	tenants := make([]string, 0, len(d.bootstrappedTenants))
	for t := range d.bootstrappedTenants {
		tenants = append(tenants, t)
	}
	d.bootstrappedTenants = nil
	stop := d.stopPF
	d.stopPF = nil
	d.mu.Unlock()
	for _, ten := range tenants {
		// Best-effort: a 404 is fine, the row was already removed.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = d.delete(ctx, "/v1/admin/tenants/"+ten, platformAdmin())
		cancel()
	}
	if stop != nil {
		stop()
	}
}

// BootstrapTenant ensures the named tenant exists in the gateway's
// tenants store via POST /v1/admin/bootstrap, returning nil on
// success. The driver records the tenant for Close to delete in
// best-effort cleanup, so a test that wants the tenant to survive past
// the driver should call the admin API directly.
//
// The bootstrap endpoint is idempotent: bootstrapping a tenant that
// already exists is a no-op. The call runs as the built-in
// platform-admin in the platform tenant.
func (d *Driver) BootstrapTenant(ctx context.Context, tenantID string) error {
	body := fmt.Sprintf(`{"tenants":[{"id":%q}]}`, tenantID)
	res, err := d.doRequest(ctx, http.MethodPost, "/v1/admin/bootstrap",
		platformAdmin(), strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("bootstrap tenant %q: %w", tenantID, err)
	}
	defer res.Body.Close()
	rb, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusMultiStatus {
		return fmt.Errorf("bootstrap tenant %q: status %d, body %s",
			tenantID, res.StatusCode, string(rb))
	}
	d.mu.Lock()
	if d.bootstrappedTenants != nil {
		d.bootstrappedTenants[tenantID] = struct{}{}
	}
	d.mu.Unlock()
	return nil
}

// CreateSession issues POST /v1/sessions for the tenant with the
// runtimeRef, returning the §15.1 session row. The session lands in
// state running (the minimal gateway skips the ready intermediate).
// When the gateway is wired to a pod binder (the e2e install is), the
// session is also placed on a warm pod before the response returns.
//
// Use CreateAndStart for the convenience POST /v1/sessions/start path
// when a test does not need the explicit two-step lifecycle.
func (d *Driver) CreateSession(ctx context.Context, tenantID, runtimeRef string) (*Session, error) {
	if runtimeRef == "" {
		runtimeRef = defaultRuntime
	}
	// §5.3 standard isolation maps to the Kind cluster's runc handler;
	// the e2e agent-workload.yaml only ships SandboxTemplates with
	// `isolationProfile: standard` so the gateway-side ResolvePool
	// match needs the same profile on the session record. Without
	// this override the gateway falls back to its default
	// (`sandboxed`) and the lookup misses every pool.
	body := fmt.Sprintf(`{"runtimeRef":%q,"isolationProfile":"standard"}`, runtimeRef)
	res, err := d.doRequest(ctx, http.MethodPost, "/v1/sessions",
		tenantAdmin(tenantID), strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	defer res.Body.Close()
	rb, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create session for tenant %q: status %d, body %s",
			tenantID, res.StatusCode, string(rb))
	}
	var s Session
	if err := json.Unmarshal(rb, &s); err != nil {
		return nil, fmt.Errorf("decode session response: %w; body %s", err, string(rb))
	}
	return &s, nil
}

// CreateAndStart issues POST /v1/sessions/start — the §15.1
// convenience path that bundles create + finalize + start in one call.
// Returns the running session with the placed-pod identity recorded in
// the gateway's sessionstore.
//
// Retries up to 6 times with linear backoff while the gateway returns a
// transient 503 pool-not-ready envelope: POD_CLAIM_FAILED, the §5.2
// line 519 WARM_POOL_EXHAUSTED (no idle pods or no free concurrent
// slot), or the §5.2 lines 602-625 RUNTIME_UNAVAILABLE (the pool is
// still bootstrapping). The §4.6 WarmPoolController scales the pool
// asynchronously after a claim drains it, so a follow-up test that
// arrives before the next warm pod settles sees one of these envelopes.
// The retry window covers the observed sub-second replenish on the
// tier-5/8/9 Kind path.
func (d *Driver) CreateAndStart(ctx context.Context, tenantID, runtimeRef string) (*Session, error) {
	if runtimeRef == "" {
		runtimeRef = defaultRuntime
	}
	// §5.3 standard isolation maps to the Kind cluster's runc handler;
	// the e2e agent-workload.yaml only ships SandboxTemplates with
	// `isolationProfile: standard` so the gateway-side ResolvePool
	// match needs the same profile on the session record. Without
	// this override the gateway falls back to its default
	// (`sandboxed`) and the lookup misses every pool.
	body := fmt.Sprintf(`{"runtimeRef":%q,"isolationProfile":"standard"}`, runtimeRef)
	var (
		lastStatus int
		lastBody   []byte
	)
	for attempt := 0; attempt < 6; attempt++ {
		res, err := d.doRequest(ctx, http.MethodPost, "/v1/sessions/start",
			tenantAdmin(tenantID), strings.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create-and-start session: %w", err)
		}
		rb, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode == http.StatusCreated {
			var s Session
			if err := json.Unmarshal(rb, &s); err != nil {
				return nil, fmt.Errorf("decode session response: %w; body %s", err, string(rb))
			}
			return &s, nil
		}
		lastStatus = res.StatusCode
		lastBody = rb
		if res.StatusCode != http.StatusServiceUnavailable || !isPoolNotReady(rb) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("create-and-start session for tenant %q: status %d, body %s",
		tenantID, lastStatus, string(lastBody))
}

// isPoolNotReady reports whether a 503 body carries one of the transient
// pool-not-ready error codes that a session-start retry is expected to
// clear once the §4.6 WarmPoolController settles the next warm pod.
func isPoolNotReady(body []byte) bool {
	for _, code := range [][]byte{
		[]byte("POD_CLAIM_FAILED"),
		[]byte("WARM_POOL_EXHAUSTED"),
		[]byte("RUNTIME_UNAVAILABLE"),
	} {
		if bytes.Contains(body, code) {
			return true
		}
	}
	return false
}

// Start issues POST /v1/sessions/{id}/start, transitioning a ready
// session to running. The minimal gateway's CreateSession already
// returns the session in running, so this method is for tests that
// explicitly stage the create → start two-step lifecycle (a derive +
// start sequence, for example).
func (d *Driver) Start(ctx context.Context, tenantID, sessionID string) (*Session, error) {
	path := "/v1/sessions/" + sessionID + "/start"
	res, err := d.doRequest(ctx, http.MethodPost, path,
		tenantAdmin(tenantID), nil)
	if err != nil {
		return nil, fmt.Errorf("start session %q: %w", sessionID, err)
	}
	defer res.Body.Close()
	rb, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("start session %q: status %d, body %s",
			sessionID, res.StatusCode, string(rb))
	}
	var s Session
	if err := json.Unmarshal(rb, &s); err != nil {
		return nil, fmt.Errorf("decode session response: %w; body %s", err, string(rb))
	}
	return &s, nil
}

// SendMessage issues POST /v1/sessions/{id}/messages with one user
// message and returns the executor's delivery receipt. The content is
// the message body the runtime sees; the echo runtime returns it
// verbatim as an assistant turn.
func (d *Driver) SendMessage(ctx context.Context, tenantID, sessionID, content string) (*MessageResponse, error) {
	envelope := struct {
		Messages []struct {
			ID      string `json:"id,omitempty"`
			Role    string `json:"role,omitempty"`
			Content string `json:"content"`
		} `json:"messages"`
	}{}
	envelope.Messages = append(envelope.Messages, struct {
		ID      string `json:"id,omitempty"`
		Role    string `json:"role,omitempty"`
		Content string `json:"content"`
	}{
		Role:    "user",
		Content: content,
	})
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode message: %w", err)
	}
	path := "/v1/sessions/" + sessionID + "/messages"
	res, err := d.doRequest(ctx, http.MethodPost, path,
		tenantAdmin(tenantID), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("send message: %w", err)
	}
	defer res.Body.Close()
	rb, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("send message to session %q: status %d, body %s",
			sessionID, res.StatusCode, string(rb))
	}
	var m MessageResponse
	if err := json.Unmarshal(rb, &m); err != nil {
		return nil, fmt.Errorf("decode message response: %w; body %s", err, string(rb))
	}
	return &m, nil
}

// Terminate issues POST /v1/sessions/{id}/terminate. The handler
// transitions the session to terminated and releases its pod claim
// back to the warm pool. Idempotent: a session already terminated
// returns the row in its terminal state.
func (d *Driver) Terminate(ctx context.Context, tenantID, sessionID string) error {
	path := "/v1/sessions/" + sessionID + "/terminate"
	res, err := d.doRequest(ctx, http.MethodPost, path,
		tenantAdmin(tenantID), nil)
	if err != nil {
		return fmt.Errorf("terminate session %q: %w", sessionID, err)
	}
	defer res.Body.Close()
	rb, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("terminate session %q: status %d, body %s",
			sessionID, res.StatusCode, string(rb))
	}
	return nil
}

// DeleteSession issues DELETE /v1/sessions/{id}. The handler removes
// the session row. A 204 / 200 is success; a 404 is not an error
// (the row was already absent).
func (d *Driver) DeleteSession(ctx context.Context, tenantID, sessionID string) error {
	path := "/v1/sessions/" + sessionID
	res, err := d.doRequest(ctx, http.MethodDelete, path,
		tenantAdmin(tenantID), nil)
	if err != nil {
		return fmt.Errorf("delete session %q: %w", sessionID, err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode == http.StatusNotFound {
		return nil
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("delete session %q: status %d", sessionID, res.StatusCode)
	}
	return nil
}

// GetSession issues GET /v1/sessions/{id} and returns the current row.
// Tests poll it to observe a state transition (a chaos test asserts the
// session moves to failed after the agent pod is deleted, for
// example).
func (d *Driver) GetSession(ctx context.Context, tenantID, sessionID string) (*Session, error) {
	path := "/v1/sessions/" + sessionID
	res, err := d.doRequest(ctx, http.MethodGet, path,
		tenantAdmin(tenantID), nil)
	if err != nil {
		return nil, fmt.Errorf("get session %q: %w", sessionID, err)
	}
	defer res.Body.Close()
	rb, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get session %q: status %d, body %s",
			sessionID, res.StatusCode, string(rb))
	}
	var s Session
	if err := json.Unmarshal(rb, &s); err != nil {
		return nil, fmt.Errorf("decode session response: %w; body %s", err, string(rb))
	}
	return &s, nil
}

// StreamEvents opens GET /v1/sessions/{id}/events as a Server-Sent
// Events stream and returns a channel of decoded events. The channel
// closes when the context is cancelled or the server closes the
// stream. afterSeq is the Last-Event-ID; pass 0 to start from the
// beginning of the retained backlog.
//
// The returned cancel function tears down the stream early. Callers
// MUST drain or cancel the channel; an undrained channel leaks the
// reader goroutine.
func (d *Driver) StreamEvents(ctx context.Context, tenantID, sessionID string, afterSeq uint64) (<-chan Event, func(), error) {
	path := "/v1/sessions/" + sessionID + "/events"
	streamCtx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet,
		d.baseURL+path, nil)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("build events request: %w", err)
	}
	addDevHeaders(req, tenantAdmin(tenantID))
	req.Header.Set("Accept", "text/event-stream")
	if afterSeq > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatUint(afterSeq, 10))
	}
	// SSE has no fixed deadline; use a no-timeout client for the
	// stream so a quiet session does not look like a server error.
	streamClient := &http.Client{Timeout: 0}
	res, err := streamClient.Do(req)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("open events stream: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		cancel()
		return nil, nil, fmt.Errorf("events stream status %d: %s", res.StatusCode, string(body))
	}

	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		defer res.Body.Close()
		readSSE(res.Body, ch, streamCtx)
	}()
	return ch, cancel, nil
}

// --- internal helpers ---

// devRole carries the gateway dev-mode auth headers a probe request
// presents. The e2e install runs gateway in dev mode (global.devMode:
// true), so X-Lenny-Tenant-ID / X-Lenny-Roles / X-Lenny-User-ID are
// honoured.
type devRole struct {
	tenant string
	roles  string
	user   string
}

// platformAdmin is the built-in platform-admin identity in the
// platform tenant. Used for the bootstrap + tenant-cleanup paths.
func platformAdmin() devRole {
	return devRole{tenant: "platform", roles: "platform-admin", user: "alice"}
}

// tenantAdmin is a platform-admin identity scoped to the named
// tenant. The session endpoints all gate on PermManageOwnSessions or
// PermReadOwnSessions; a tenant-scoped platform-admin satisfies both.
func tenantAdmin(tenantID string) devRole {
	return devRole{tenant: tenantID, roles: "platform-admin", user: "alice"}
}

// addDevHeaders attaches the dev-mode auth headers to the request.
func addDevHeaders(req *http.Request, r devRole) {
	req.Header.Set("X-Lenny-Tenant-ID", r.tenant)
	req.Header.Set("X-Lenny-Roles", r.roles)
	req.Header.Set("X-Lenny-User-ID", r.user)
}

// doRequest builds and executes one HTTP request through the
// port-forward, returning the raw *http.Response. The caller closes
// res.Body.
func (d *Driver) doRequest(ctx context.Context, method, path string, role devRole, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, d.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	addDevHeaders(req, role)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return d.hc.Do(req)
}

// delete is the closing-cleanup helper used by Close to remove the
// synthetic tenants the driver bootstrapped.
func (d *Driver) delete(ctx context.Context, path string, role devRole) error {
	res, err := d.doRequest(ctx, http.MethodDelete, path, role, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return nil
}

// waitGatewayReady polls GET /v1/admin/health until it responds with a
// 200 or the deadline expires. The endpoint is unauthenticated in dev
// mode, so it is the right readiness signal once the port-forward has
// bound.
func (d *Driver) waitGatewayReady(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			d.baseURL+"/v1/admin/health", nil)
		if err != nil {
			return err
		}
		res, err := d.hc.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK || res.StatusCode == http.StatusServiceUnavailable {
				// 503 from health means a sub-component is unhealthy but
				// the gateway is responsive; that is sufficient for the
				// session-driving harness, which does not exercise the
				// failed sub-component.
				return nil
			}
			lastErr = fmt.Errorf("status %d", res.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr == nil {
				lastErr = ctx.Err()
			}
			return fmt.Errorf("gateway not ready within %s: %w", timeout, lastErr)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// readSSE decodes a Server-Sent Events stream from r and publishes
// each event onto ch until the context is cancelled or the reader
// returns an error / EOF. The §15.1 SSE format is:
//
//	id: <seq>
//	event: <type>
//	data: <json>
//	<blank line>
func readSSE(r io.Reader, ch chan<- Event, ctx context.Context) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var cur Event
	for sc.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := sc.Text()
		if line == "" {
			if cur.Type != "" || len(cur.Data) > 0 {
				select {
				case ch <- cur:
				case <-ctx.Done():
					return
				}
			}
			cur = Event{}
			continue
		}
		if v, ok := strings.CutPrefix(line, "id:"); ok {
			s := strings.TrimSpace(v)
			if n, err := strconv.ParseUint(s, 10, 64); err == nil {
				cur.Seq = n
			}
			continue
		}
		if v, ok := strings.CutPrefix(line, "event:"); ok {
			cur.Type = strings.TrimSpace(v)
			continue
		}
		if v, ok := strings.CutPrefix(line, "data:"); ok {
			cur.Data = append(cur.Data, []byte(strings.TrimSpace(v))...)
			continue
		}
		if strings.HasPrefix(line, ":") {
			// SSE comment line; skip.
			continue
		}
	}
	// scanner ended; surface any unfinished frame the server emitted
	// without a terminating blank line.
	if cur.Type != "" || len(cur.Data) > 0 {
		select {
		case ch <- cur:
		case <-ctx.Done():
		}
	}
}

// WaitForState polls GET /v1/sessions/{id} until the session state is
// one of the targets, returning the final session and a flag for
// whether the target was reached within timeout. It is the right
// helper for tests that drive a session through a lifecycle
// transition and then assert the resulting state.
func (d *Driver) WaitForState(ctx context.Context, tenantID, sessionID string, timeout time.Duration, targets ...string) (*Session, bool) {
	deadline := time.Now().Add(timeout)
	var last *Session
	for {
		s, err := d.GetSession(ctx, tenantID, sessionID)
		if err == nil {
			last = s
			for _, want := range targets {
				if s.State == want {
					return s, true
				}
			}
		}
		if time.Now().After(deadline) {
			return last, false
		}
		select {
		case <-ctx.Done():
			return last, false
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// ErrEventsClosed is returned by ReceiveEvent when the events channel
// has been closed before an event arrived (the server ended the stream
// or the parent context was cancelled).
var ErrEventsClosed = errors.New("sessiondriver: events channel closed")
