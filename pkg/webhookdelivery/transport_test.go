// SPDX-License-Identifier: MIT

package webhookdelivery_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/webhookdelivery"
)

func TestSignIsStableHMAC(t *testing.T) {
	secret := []byte("whsec_test")
	body := []byte(`{"id":"evt-1"}`)
	got := webhookdelivery.Sign(secret, body)
	if got != webhookdelivery.Sign(secret, body) {
		t.Error("Sign is not deterministic for the same secret and body")
	}
	if got == webhookdelivery.Sign([]byte("whsec_other"), body) {
		t.Error("Sign returned the same value for different secrets")
	}
	if len(got) != 64 {
		t.Errorf("Sign returned %d hex chars, want 64 for SHA-256", len(got))
	}
}

func TestDeliverSendsCloudEventsRequest(t *testing.T) {
	var (
		gotSig, gotType, gotID, gotDelivery, gotAttempt, gotContentType string
		gotBody                                                         []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get(webhookdelivery.HeaderSignature)
		gotType = r.Header.Get(webhookdelivery.HeaderEventType)
		gotID = r.Header.Get(webhookdelivery.HeaderEventID)
		gotDelivery = r.Header.Get(webhookdelivery.HeaderDeliveryID)
		gotAttempt = r.Header.Get(webhookdelivery.HeaderDeliveryAttempt)
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	body := []byte(`{"id":"evt-7","type":"dev.lenny.alert_fired"}`)
	secret := []byte("whsec_abc")
	out := webhookdelivery.NewTransport(2*time.Second).Deliver(context.Background(), webhookdelivery.Delivery{
		CallbackURL: srv.URL,
		Body:        body,
		Secret:      secret,
		EventType:   "dev.lenny.alert_fired",
		EventID:     "evt-7",
		DeliveryID:  "del-1",
		Attempt:     1,
	})
	if !out.Delivered() {
		t.Fatalf("Delivered() = false, outcome = %+v", out)
	}
	if gotContentType != webhookdelivery.ContentType {
		t.Errorf("Content-Type = %q, want %q", gotContentType, webhookdelivery.ContentType)
	}
	if gotSig != webhookdelivery.Sign(secret, body) {
		t.Errorf("signature header = %q, want the HMAC of the body", gotSig)
	}
	if gotType != "dev.lenny.alert_fired" || gotID != "evt-7" || gotDelivery != "del-1" || gotAttempt != "1" {
		t.Errorf("X-Lenny headers = type %q id %q delivery %q attempt %q", gotType, gotID, gotDelivery, gotAttempt)
	}
	if string(gotBody) != string(body) {
		t.Errorf("body = %q, want %q", gotBody, body)
	}
}

func TestDeliverClassifiesRetryable5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	out := webhookdelivery.NewTransport(2*time.Second).Deliver(context.Background(), webhookdelivery.Delivery{
		CallbackURL: srv.URL, Body: []byte("{}"), Secret: []byte("s"), Attempt: 1,
	})
	if out.Delivered() {
		t.Error("Delivered() = true for a 502 response")
	}
	if !out.Retryable() {
		t.Error("Retryable() = false for a 502 response, want true (transient)")
	}
}

func TestDeliverClassifiesPermanent4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	out := webhookdelivery.NewTransport(2*time.Second).Deliver(context.Background(), webhookdelivery.Delivery{
		CallbackURL: srv.URL, Body: []byte("{}"), Secret: []byte("s"), Attempt: 1,
	})
	if out.Delivered() || out.Retryable() {
		t.Errorf("a 400 response must be a permanent failure, got %+v", out)
	}
}

func TestDeliverTransportErrorIsRetryable(t *testing.T) {
	// A closed listener address produces a connection error: no HTTP
	// response, so the outcome carries Err and is retryable.
	out := webhookdelivery.NewTransport(500*time.Millisecond).Deliver(context.Background(), webhookdelivery.Delivery{
		CallbackURL: "http://127.0.0.1:1", Body: []byte("{}"), Secret: []byte("s"), Attempt: 1,
	})
	if out.Err == nil {
		t.Fatal("expected a transport error dialing a closed port")
	}
	if out.Delivered() || !out.Retryable() {
		t.Errorf("a transport error must be a retryable non-delivery, got %+v", out)
	}
}

func TestDeliverDoesNotFollowRedirects(t *testing.T) {
	var hops atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops.Add(1)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	out := webhookdelivery.NewTransport(2*time.Second).Deliver(context.Background(), webhookdelivery.Delivery{
		CallbackURL: srv.URL + "/redirect", Body: []byte("{}"), Secret: []byte("s"), Attempt: 1,
	})
	// §25.5: the client does not follow redirects; a 302 is a delivery
	// failure (not a 2xx). The receiver must be hit exactly once.
	if out.Delivered() {
		t.Error("a 302 redirect must not count as delivered")
	}
	if got := hops.Load(); got != 1 {
		t.Errorf("receiver hit %d times, want 1 (redirect not followed)", got)
	}
}
