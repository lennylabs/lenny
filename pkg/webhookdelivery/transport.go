// SPDX-License-Identifier: MIT

package webhookdelivery

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ContentType is the §25.5 webhook request Content-Type: the body is a
// CloudEvents v1.0.2 JSON record.
const ContentType = "application/cloudevents+json"

// HTTP headers §25.5 sets on every webhook delivery.
const (
	// HeaderSignature carries the HMAC-SHA256 of the request body.
	HeaderSignature = "X-Lenny-Signature"
	// HeaderEventType carries the CloudEvents type attribute.
	HeaderEventType = "X-Lenny-Event-Type"
	// HeaderEventID carries the CloudEvents id attribute (the eventKey).
	HeaderEventID = "X-Lenny-Event-Id"
	// HeaderDeliveryID carries a UUID unique to this delivery attempt.
	HeaderDeliveryID = "X-Lenny-Delivery-Id"
	// HeaderDeliveryAttempt carries the 1-based attempt number.
	HeaderDeliveryAttempt = "X-Lenny-Delivery-Attempt"
)

// Delivery is one webhook delivery request: the CloudEvents JSON body
// and the metadata §25.5 puts in the HTTP headers.
type Delivery struct {
	// CallbackURL is the subscription's webhook endpoint.
	CallbackURL string
	// Body is the serialized CloudEvents JSON record.
	Body []byte
	// Secret is the subscription's HMAC signing secret.
	Secret []byte
	// EventType is the CloudEvents type attribute (e.g. dev.lenny.alert_fired).
	EventType string
	// EventID is the CloudEvents id attribute (the eventKey).
	EventID string
	// DeliveryID is a UUID unique to this delivery attempt.
	DeliveryID string
	// Attempt is the 1-based attempt number.
	Attempt int
}

// Outcome is the result of one webhook delivery attempt.
type Outcome struct {
	// StatusCode is the receiver's HTTP status; zero when the request did
	// not reach a receiver (DNS failure, connection refused, timeout).
	StatusCode int
	// Err describes a transport-level failure; nil when an HTTP response
	// was received regardless of its status.
	Err error
}

// Delivered reports whether the attempt reached the receiver and the
// receiver returned a 2xx status.
func (o Outcome) Delivered() bool {
	return o.Err == nil && o.StatusCode >= 200 && o.StatusCode <= 299
}

// Retryable reports whether a failed attempt should be retried under
// the §25.5 policy: a transport error is transient, and an HTTP
// response is retryable when RetryableStatus classifies it transient.
func (o Outcome) Retryable() bool {
	if o.Delivered() {
		return false
	}
	if o.Err != nil {
		return true
	}
	return RetryableStatus(o.StatusCode)
}

// Sign returns the §25.5 X-Lenny-Signature value: the lowercase hex
// HMAC-SHA256 of body keyed by secret.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Transport performs a single §25.5 webhook delivery attempt over HTTP.
// It does not retry; the delivery worker sequences attempts using the
// RetryDelay backoff. The retry budget and outcome classification stay
// in the pure policy functions so the worker and its tests share them.
type Transport struct {
	client *http.Client
	// guard re-validates the callback URL before each attempt dials, so
	// the §25.5 SSRF/DNS-rebinding check runs on every delivery and not
	// only at subscription create time. A nil guard skips the check (the
	// create-time validation still applies). spec: §25.5 lines 2735-2745.
	guard func(ctx context.Context, callbackURL string) error
}

// NewTransport returns a Transport whose HTTP client bounds each
// delivery attempt by timeout and never follows redirects, per §25.5
// (a redirect from a whitelisted callback domain is a delivery
// failure, not a hop to follow).
func NewTransport(timeout time.Duration) *Transport {
	return &Transport{client: &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// WithSSRFGuard installs a per-delivery callback-URL validator. The guard
// runs before every attempt dials; a non-nil error from the guard fails
// the attempt without a network call, closing the §25.5 line 2741 DNS
// rebinding gap where a host resolves to a public IP at subscription time
// and to a private IP later. spec: §25.5 lines 2735-2745.
func (t *Transport) WithSSRFGuard(guard func(ctx context.Context, callbackURL string) error) *Transport {
	t.guard = guard
	return t
}

// Deliver POSTs one webhook delivery to its callback URL with the
// §25.5 Content-Type, HMAC signature, and X-Lenny-* headers, and
// reports the outcome. A transport-level failure is returned as an
// Outcome with Err set rather than as a Go error so the worker can
// classify every attempt uniformly.
func (t *Transport) Deliver(ctx context.Context, d Delivery) Outcome {
	if t.guard != nil {
		if err := t.guard(ctx, d.CallbackURL); err != nil {
			return Outcome{Err: fmt.Errorf("webhook SSRF guard rejected %s: %w", d.CallbackURL, err)}
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.CallbackURL, bytes.NewReader(d.Body))
	if err != nil {
		return Outcome{Err: fmt.Errorf("build webhook request: %w", err)}
	}
	req.Header.Set("Content-Type", ContentType)
	req.Header.Set(HeaderSignature, Sign(d.Secret, d.Body))
	req.Header.Set(HeaderEventType, d.EventType)
	req.Header.Set(HeaderEventID, d.EventID)
	req.Header.Set(HeaderDeliveryID, d.DeliveryID)
	req.Header.Set(HeaderDeliveryAttempt, fmt.Sprintf("%d", d.Attempt))

	resp, err := t.client.Do(req)
	if err != nil {
		return Outcome{Err: fmt.Errorf("webhook POST to %s: %w", d.CallbackURL, err)}
	}
	defer resp.Body.Close()
	// Drain the body so the connection can be reused; the receiver's
	// response content is not part of the §25.5 delivery contract.
	_, _ = io.Copy(io.Discard, resp.Body)
	return Outcome{StatusCode: resp.StatusCode}
}
