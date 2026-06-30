// SPDX-License-Identifier: MIT

package sessioncallback

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/webhooksig"
)

// TestRetrySchedule pins the §14 line 150 backoff and attempt budget so a
// future edit cannot silently change the delivery policy. F-14.1.11.
func TestRetrySchedule_spec_14_150(t *testing.T) {
	if MaxAttempts != 5 {
		t.Errorf("MaxAttempts = %d, want 5", MaxAttempts)
	}
	want := []time.Duration{10 * time.Second, 30 * time.Second, 60 * time.Second, 300 * time.Second, 900 * time.Second}
	if len(RetrySchedule) != len(want) {
		t.Fatalf("RetrySchedule len = %d, want %d", len(RetrySchedule), len(want))
	}
	for i := range want {
		if RetrySchedule[i] != want[i] {
			t.Errorf("RetrySchedule[%d] = %s, want %s", i, RetrySchedule[i], want[i])
		}
	}
}

func waitRec(t *testing.T, ch chan *DeliveryRecord) *DeliveryRecord {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("finalizer was not called")
		return nil
	}
}

// dispatcherFor builds a dispatcher whose delivery client targets srv
// directly with the §14 SSRF re-check stubbed (httptest uses loopback).
func dispatcherFor(srv *httptest.Server, secret []byte, done chan *DeliveryRecord, now func() time.Time) *Dispatcher {
	cfg := Config{
		GatewayID:  "gw-abcde",
		Opener:     func(context.Context, string, []byte) ([]byte, error) { return secret, nil },
		Finalizer:  func(_ context.Context, _, _ string, u *DeliveryRecord) error { done <- u; return nil },
		Sleep:      func(context.Context, time.Duration) bool { return true },
		Revalidate: func(netip.Addr) bool { return true },
		NewClient:  func(netip.Addr) *http.Client { return srv.Client() },
		NowFn:      now,
	}
	return NewDispatcher(cfg)
}

func completedJob(srvURL string) Job {
	return Job{
		TenantID:     "t_acme",
		SessionID:    "sess_1",
		CallbackURL:  srvURL,
		PinnedIP:     netip.MustParseAddr("127.0.0.1"),
		SealedSecret: []byte("opaque-sealed"),
		ShortName:    EventSessionCompleted,
		Subject:      "session/sess_1",
		Data:         CompletedData(SessionInfo{SessionID: "sess_1"}),
	}
}

// TestDispatcherDeliversSuccess verifies the §14 CloudEvents envelope,
// the cloudevents Content-Type, the HMAC signature, and the secret-clear
// on a 2xx delivery. spec: §14 lines 114-139. F-14.1.11.
func TestDispatcherDeliversSuccess_spec_14_114(t *testing.T) {
	var gotBody []byte
	var gotSig, gotCT, gotType, gotID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Lenny-Signature")
		gotCT = r.Header.Get("Content-Type")
		gotType = r.Header.Get("X-Lenny-Event-Type")
		gotID = r.Header.Get("X-Lenny-Event-Id")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	secret := []byte("whsec_test")
	done := make(chan *DeliveryRecord, 1)
	d := dispatcherFor(srv, secret, done, time.Now)
	d.Enqueue(completedJob(srv.URL))

	if rec := waitRec(t, done); rec != nil {
		t.Fatalf("expected nil undelivered record on success, got %+v", rec)
	}
	if gotCT != ContentType {
		t.Errorf("Content-Type = %q, want %q", gotCT, ContentType)
	}
	if gotType != "dev.lenny.session_completed" {
		t.Errorf("X-Lenny-Event-Type = %q", gotType)
	}
	if err := webhooksig.Verify(gotBody, gotSig, time.Now(), secret); err != nil {
		t.Errorf("signature did not verify: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(gotBody, &env); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if env["type"] != "dev.lenny.session_completed" {
		t.Errorf("CloudEvents type = %v", env["type"])
	}
	if env["specversion"] != "1.0" {
		t.Errorf("specversion = %v", env["specversion"])
	}
	if env["subject"] != "session/sess_1" {
		t.Errorf("subject = %v", env["subject"])
	}
	if gotID == "" || env["id"] != gotID {
		t.Errorf("X-Lenny-Event-Id %q != body id %v", gotID, env["id"])
	}
}

// TestDispatcherRetriesThenSucceeds confirms a transient 500 is retried
// and a later 2xx settles the delivery. spec: §14 line 150. F-14.1.11.
func TestDispatcherRetriesThenSucceeds_spec_14_150(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	done := make(chan *DeliveryRecord, 1)
	d := dispatcherFor(srv, []byte("s"), done, time.Now)
	d.Enqueue(completedJob(srv.URL))

	if rec := waitRec(t, done); rec != nil {
		t.Fatalf("expected success after retries, got undelivered %+v", rec)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

// TestDispatcherExhaustion confirms a persistently failing receiver
// exhausts the §14 retry budget and records the undelivered event with
// the last status. spec: §14 line 150. F-14.1.11.
func TestDispatcherExhaustion_spec_14_150(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	done := make(chan *DeliveryRecord, 1)
	d := dispatcherFor(srv, []byte("s"), done, time.Now)
	d.Enqueue(completedJob(srv.URL))

	rec := waitRec(t, done)
	if rec == nil {
		t.Fatal("expected an undelivered record after exhaustion")
	}
	if got := atomic.LoadInt32(&attempts); got != int32(MaxAttempts) {
		t.Errorf("attempts = %d, want %d", got, MaxAttempts)
	}
	if rec.LastStatus != http.StatusInternalServerError {
		t.Errorf("LastStatus = %d, want 500", rec.LastStatus)
	}
	if rec.EventType != "dev.lenny.session_completed" {
		t.Errorf("EventType = %q", rec.EventType)
	}
	if len(rec.Body) == 0 {
		t.Error("undelivered record carries no body for re-delivery")
	}
}

// TestDispatcherPermanentFailureNoRetry confirms a non-retryable status
// (4xx other than 429) is not retried. spec: §14 line 150 (only non-2xx
// transient failures retry). F-14.1.11.
func TestDispatcherPermanentFailureNoRetry_spec_14_150(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	done := make(chan *DeliveryRecord, 1)
	d := dispatcherFor(srv, []byte("s"), done, time.Now)
	d.Enqueue(completedJob(srv.URL))

	rec := waitRec(t, done)
	if rec == nil {
		t.Fatal("expected an undelivered record on permanent failure")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 400)", got)
	}
}

// TestDispatcherFreshSignaturePerAttempt confirms each delivery attempt
// re-signs with a fresh timestamp while the CloudEvents time stays fixed.
// spec: §14 line 139. F-14.1.11.
func TestDispatcherFreshSignaturePerAttempt_spec_14_139(t *testing.T) {
	var sigs []string
	var bodyTimes []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env map[string]any
		_ = json.Unmarshal(body, &env)
		sigs = append(sigs, r.Header.Get("X-Lenny-Signature"))
		bodyTimes = append(bodyTimes, env["time"])
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// NowFn advances one second per call so retried signatures differ.
	var nowCalls int64
	now := func() time.Time {
		n := atomic.AddInt64(&nowCalls, 1)
		return time.Unix(1_700_000_000+n, 0).UTC()
	}
	done := make(chan *DeliveryRecord, 1)
	d := dispatcherFor(srv, []byte("s"), done, now)
	d.Enqueue(completedJob(srv.URL))
	waitRec(t, done)

	if len(sigs) < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", len(sigs))
	}
	if sigs[0] == sigs[1] {
		t.Errorf("attempt signatures did not differ: %q", sigs[0])
	}
	if bodyTimes[0] != bodyTimes[1] {
		t.Errorf("CloudEvents time changed across retries: %v vs %v", bodyTimes[0], bodyTimes[1])
	}
}

// TestPinnedClientDialsPin confirms the delivery transport dials the
// pinned IP regardless of the URL hostname (DNS-rebind defense).
// spec: §14 line 110. F-14.1.11.
func TestPinnedClientDialsPin_spec_14_110(t *testing.T) {
	var hit int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hit, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	pin := netip.MustParseAddr(u.Hostname())

	done := make(chan *DeliveryRecord, 1)
	d := NewDispatcher(Config{
		GatewayID:  "gw-abcde",
		Opener:     func(context.Context, string, []byte) ([]byte, error) { return nil, nil },
		Finalizer:  func(_ context.Context, _, _ string, u *DeliveryRecord) error { done <- u; return nil },
		Sleep:      func(context.Context, time.Duration) bool { return true },
		Revalidate: func(netip.Addr) bool { return true },
		// Default NewClient = pinnedHTTPClient, which must dial the pin.
		NowFn: time.Now,
	})
	job := completedJob("http://callback.invalid:" + u.Port() + "/hook")
	job.PinnedIP = pin
	d.Enqueue(job)

	if rec := waitRec(t, done); rec != nil {
		t.Fatalf("delivery to a non-resolvable host failed despite a valid pin: %+v", rec)
	}
	if atomic.LoadInt32(&hit) != 1 {
		t.Error("pinned dial did not reach the server")
	}
}

// TestPinnedClientRefusesRedirect confirms the §14 line 111 no-redirect
// rule. F-14.1.11.
func TestPinnedClientRefusesRedirect_spec_14_111(t *testing.T) {
	c := pinnedHTTPClient(netip.MustParseAddr("93.184.216.34"))
	if c.CheckRedirect == nil {
		t.Fatal("callback client has no CheckRedirect")
	}
	if err := c.CheckRedirect(&http.Request{}, nil); !errors.Is(err, errNoRedirect) {
		t.Errorf("CheckRedirect = %v, want errNoRedirect", err)
	}
}

// TestDispatcherRevalidatesPin confirms a pin that turned private since
// admission is not dialed. spec: §14 line 110. F-14.1.11.
func TestDispatcherRevalidatesPin_spec_14_110(t *testing.T) {
	var hit int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hit, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	done := make(chan *DeliveryRecord, 1)
	d := NewDispatcher(Config{
		GatewayID:  "gw-abcde",
		Opener:     func(context.Context, string, []byte) ([]byte, error) { return nil, nil },
		Finalizer:  func(_ context.Context, _, _ string, u *DeliveryRecord) error { done <- u; return nil },
		Sleep:      func(context.Context, time.Duration) bool { return true },
		Revalidate: func(netip.Addr) bool { return false }, // pin now private
		NewClient:  func(netip.Addr) *http.Client { return srv.Client() },
		NowFn:      time.Now,
	})
	d.Enqueue(completedJob(srv.URL))

	rec := waitRec(t, done)
	if rec == nil {
		t.Fatal("expected an undelivered record when the pin fails re-validation")
	}
	if atomic.LoadInt32(&hit) != 0 {
		t.Error("dispatcher dialed a pin that failed re-validation")
	}
}
