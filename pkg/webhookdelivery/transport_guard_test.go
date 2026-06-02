// SPDX-License-Identifier: MIT

package webhookdelivery_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/webhookdelivery"
)

// spec: §25.5 lines 2735-2745 — the per-delivery SSRF guard runs before
// the attempt dials. A guard rejection fails the attempt without a
// network call; a passing guard lets the delivery through.
func TestTransportSSRFGuard_spec_25_5(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Rejecting guard: the receiver is never contacted.
	rejecting := webhookdelivery.NewTransport(2 * time.Second).WithSSRFGuard(
		func(context.Context, string) error { return errors.New("resolved to private range") })
	out := rejecting.Deliver(context.Background(), webhookdelivery.Delivery{
		CallbackURL: srv.URL, Body: []byte("{}"), Secret: []byte("s"),
	})
	if out.Err == nil {
		t.Fatalf("guarded delivery succeeded, want SSRF rejection")
	}
	if hit {
		t.Errorf("receiver was contacted despite guard rejection")
	}

	// Passing guard: the delivery reaches the receiver.
	allowing := webhookdelivery.NewTransport(2 * time.Second).WithSSRFGuard(
		func(context.Context, string) error { return nil })
	out = allowing.Deliver(context.Background(), webhookdelivery.Delivery{
		CallbackURL: srv.URL, Body: []byte("{}"), Secret: []byte("s"),
	})
	if !out.Delivered() {
		t.Fatalf("allowed delivery did not reach receiver: %+v", out)
	}
	if !hit {
		t.Errorf("receiver was not contacted by an allowed delivery")
	}
}
