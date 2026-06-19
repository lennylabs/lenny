// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessioncallback"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// spec: §14 lines 108-150 (session-completion webhook); §15.1 lines 678,
// 690 (webhook-events list, start callbackUrl). F-14.1.11 / F-15.1.11.

// fakeResolver maps a hostname to fixed addresses so the §14 SSRF
// validator runs without real DNS in the sessionserver tests.
type fakeResolver struct{ m map[string][]netip.Addr }

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	a, ok := f.m[host]
	if !ok {
		return nil, errors.New("nxdomain")
	}
	return a, nil
}

func callbackValidator() *sessioncallback.Validator {
	return sessioncallback.NewValidator(nil, fakeResolver{m: map[string][]netip.Addr{
		"hooks.example.com":    {netip.MustParseAddr("93.184.216.34")},
		"internal.example.com": {netip.MustParseAddr("10.0.0.7")},
	}})
}

// fakeSeal stands in for the KMS-envelope seal so the create path can run
// without a KMS backend. It is a recognizable, non-plaintext transform.
func fakeSeal(_ context.Context, _ string, pt []byte) ([]byte, error) {
	return append([]byte("sealed:"), pt...), nil
}

func callbackServer(t *testing.T, store sessionstore.Store, opts sessionserver.Options) *sessionserver.Server {
	t.Helper()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	if opts.Clock == nil {
		opts.Clock = clock
	}
	if opts.IDFunc == nil {
		opts.IDFunc = func() string { return "sess_cb" }
	}
	if opts.UploadTokenIssuer == nil {
		opts.UploadTokenIssuer = uploadtoken.NewIssuer(ring, opts.Clock)
	}
	if opts.CallbackValidator == nil {
		opts.CallbackValidator = callbackValidator()
	}
	if opts.CallbackSeal == nil {
		opts.CallbackSeal = fakeSeal
	}
	return sessionserver.New(store, opts)
}

func TestCreateAcceptsCallback_spec_14_108(t *testing.T) {
	store := memstore.New()
	srv := callbackServer(t, store, sessionserver.Options{})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:     "claude-code",
		CallbackURL:    "https://hooks.example.com/lenny",
		CallbackSecret: "whsec_supersecret",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "whsec_supersecret") {
		t.Error("create response leaked the plaintext callbackSecret")
	}
	row, err := store.Get(context.Background(), "acme", "sess_cb")
	if err != nil {
		t.Fatalf("get row: %v", err)
	}
	if row.CallbackURL != "https://hooks.example.com/lenny" {
		t.Errorf("row.CallbackURL = %q", row.CallbackURL)
	}
	if row.CallbackPinnedIP != "93.184.216.34" {
		t.Errorf("row.CallbackPinnedIP = %q, want 93.184.216.34", row.CallbackPinnedIP)
	}
	if string(row.CallbackSecret) != "sealed:whsec_supersecret" {
		t.Errorf("row.CallbackSecret = %q; want the KMS-sealed form, not plaintext", row.CallbackSecret)
	}
}

func TestCreateRejectsNonHTTPSCallback_spec_14_109(t *testing.T) {
	srv := callbackServer(t, memstore.New(), sessionserver.Options{})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:  "claude-code",
		CallbackURL: "http://hooks.example.com/lenny",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	code, _, details := decodeError(t, rr)
	if code != "INVALID_CALLBACK_URL" {
		t.Errorf("code = %q, want INVALID_CALLBACK_URL", code)
	}
	if details["reason"] != sessioncallback.ReasonSchemeNotHTTPS {
		t.Errorf("details.reason = %v, want %q", details["reason"], sessioncallback.ReasonSchemeNotHTTPS)
	}
}

func TestCreateRejectsPrivateCallback_spec_14_110(t *testing.T) {
	srv := callbackServer(t, memstore.New(), sessionserver.Options{})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:  "claude-code",
		CallbackURL: "https://internal.example.com/lenny",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	code, _, details := decodeError(t, rr)
	if code != "INVALID_CALLBACK_URL" || details["reason"] != sessioncallback.ReasonPrivateIP {
		t.Errorf("code/reason = %q/%v, want INVALID_CALLBACK_URL/private_ip", code, details["reason"])
	}
}

func TestCreateRejectsSecretWithoutURL_spec_14_139(t *testing.T) {
	srv := callbackServer(t, memstore.New(), sessionserver.Options{})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:     "claude-code",
		CallbackSecret: "whsec_x",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if code, _, _ := decodeError(t, rr); code != "VALIDATION_ERROR" {
		t.Errorf("code = %q, want VALIDATION_ERROR", code)
	}
}

// TestCreateAndStartAcceptsCallback covers the §15.1 line 690 start-path
// callbackUrl. F-15.1.11.
func TestCreateAndStartAcceptsCallback_spec_15_690(t *testing.T) {
	store := memstore.New()
	srv := callbackServer(t, store, sessionserver.Options{IDFunc: func() string { return "sess_start_cb" }})
	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{
		RuntimeRef:  "claude-code",
		CallbackURL: "https://hooks.example.com/lenny",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "acme", "sess_start_cb")
	if err != nil {
		t.Fatalf("get row: %v", err)
	}
	if row.CallbackURL != "https://hooks.example.com/lenny" || row.CallbackPinnedIP == "" {
		t.Errorf("start did not persist the callback: url=%q pin=%q", row.CallbackURL, row.CallbackPinnedIP)
	}
}

func TestCreateAndStartRejectsBadCallback_spec_15_690(t *testing.T) {
	srv := callbackServer(t, memstore.New(), sessionserver.Options{IDFunc: func() string { return "sess_start_bad" }})
	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{
		RuntimeRef:  "claude-code",
		CallbackURL: "http://hooks.example.com/lenny",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if code, _, _ := decodeError(t, rr); code != "INVALID_CALLBACK_URL" {
		t.Errorf("code = %q, want INVALID_CALLBACK_URL", code)
	}
}

// TestWebhookEventsEndpoint covers the §15.1 line 678 undelivered-events
// list. F-14.1.11.
func TestWebhookEventsEndpoint_spec_15_678(t *testing.T) {
	store := memstore.New()
	srv := callbackServer(t, store, sessionserver.Options{})
	ctx := context.Background()
	if err := store.Create(ctx, sessionstore.Session{
		ID: "sess_wh", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Empty before any exhaustion.
	got := getWebhookEvents(t, srv.Handler(), "sess_wh")
	if len(got.Items) != 0 {
		t.Fatalf("expected no events, got %d", len(got.Items))
	}

	// Record an undelivered event and confirm it surfaces.
	if _, err := store.Update(ctx, "acme", "sess_wh", func(row *sessionstore.Session) error {
		row.WebhookEvents = append(row.WebhookEvents, sessionstore.WebhookEventRecord{
			EventID: "evt_1", EventType: "dev.lenny.session_completed",
			CallbackURL: "https://hooks.example.com/lenny", Attempts: 5,
			LastStatus: 500, Body: []byte(`{"id":"evt_1"}`),
		})
		return nil
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got = getWebhookEvents(t, srv.Handler(), "sess_wh")
	if len(got.Items) != 1 || got.Items[0].EventID != "evt_1" {
		t.Fatalf("expected the recorded event, got %+v", got.Items)
	}
	if string(got.Items[0].Event) != `{"id":"evt_1"}` {
		t.Errorf("event body not inlined: %s", got.Items[0].Event)
	}

	// 404 for a missing session.
	rr := httptest.NewRequest(http.MethodGet, "/v1/sessions/nope/webhook-events", nil)
	rr.Header.Set("X-Lenny-Tenant-ID", "acme")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, rr)
	if w.Code != http.StatusNotFound {
		t.Errorf("missing session status: got %d, want 404", w.Code)
	}
}

type webhookEventsResp struct {
	SessionID string `json:"sessionId"`
	Items     []struct {
		EventID string          `json:"eventId"`
		Event   json.RawMessage `json:"event"`
	} `json:"items"`
	HasMore bool `json:"hasMore"`
}

func getWebhookEvents(t *testing.T, h http.Handler, id string) webhookEventsResp {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+id+"/webhook-events", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("webhook-events status: got %d, body=%s", w.Code, w.Body.String())
	}
	var resp webhookEventsResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode webhook-events: %v", err)
	}
	return resp
}

// TestTerminalCallbackDelivered drives a completed session through the
// terminal hook and asserts the §14 webhook is delivered and the sealed
// secret is cleared. spec: §14 lines 108-150. F-14.1.11.
func TestTerminalCallbackDelivered_spec_14_108(t *testing.T) {
	var gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env map[string]any
		_ = json.Unmarshal(body, &env)
		if s, ok := env["type"].(string); ok {
			gotType = s
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	store := memstore.New()
	done := make(chan *sessioncallback.DeliveryRecord, 1)
	dispatcher := sessioncallback.NewDispatcher(sessioncallback.Config{
		GatewayID: "gw-test1",
		Opener:    func(context.Context, string, []byte) ([]byte, error) { return []byte("secret"), nil },
		Finalizer: func(ctx context.Context, tenantID, sessionID string, undelivered *sessioncallback.DeliveryRecord) error {
			_, err := store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
				row.CallbackSecret = nil
				return nil
			})
			done <- undelivered
			return err
		},
		Sleep:      func(context.Context, time.Duration) bool { return true },
		Revalidate: func(netip.Addr) bool { return true },
		NewClient:  func(netip.Addr) *http.Client { return srv.Client() },
	})
	gw := callbackServer(t, store, sessionserver.Options{CallbackDispatcher: dispatcher})

	ctx := context.Background()
	row := sessionstore.Session{
		ID: "sess_term", TenantID: "acme", State: session.StateCompleted,
		CallbackURL: srv.URL, CallbackPinnedIP: u.Hostname(),
		CallbackSecret: []byte("opaque-sealed"),
		CreatedAt:      time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.Create(ctx, row); err != nil {
		t.Fatalf("create: %v", err)
	}
	gw.OnSessionTerminal(ctx, session.StateRunning, row)

	select {
	case rec := <-done:
		if rec != nil {
			t.Fatalf("expected successful delivery, got undelivered %+v", rec)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("callback was not delivered")
	}
	if gotType != "dev.lenny.session_completed" {
		t.Errorf("delivered CloudEvents type = %q", gotType)
	}
	after, _ := store.Get(ctx, "acme", "sess_term")
	if len(after.CallbackSecret) != 0 {
		t.Errorf("callbackSecret was not cleared after delivery: %q", after.CallbackSecret)
	}
}
