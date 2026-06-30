// SPDX-License-Identifier: MIT

// Package billingsink implements the §11.2.1 billing-event delivery
// sinks. Billing events are always written to Postgres synchronously
// through the EventStore; the sinks are the downstream delivery surface
// the spec lists under "Delivery sinks":
//
//   - Webhook URL — events POSTed as JSON with an HMAC-SHA256 signature
//     header, retried with exponential backoff and dead-lettered after
//     exhaustion.
//   - Message queue — published to an SQS, Google Pub/Sub, or Kafka
//     topic through an injected QueuePublisher.
//   - Both — a webhook and a queue sink registered together for
//     redundancy.
//
// Per §11.2.1 line 137 the gateway publishes to the sinks asynchronously
// and only after the synchronous Postgres write confirms. The Publishing
// store decorator enforces that ordering: it delivers a sealed event to
// the configured sinks only after the wrapped Store.Append returns
// success.
//
// spec: §11.2.1 — Delivery sinks (lines 132-138). F-11.2.14.
package billingsink

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
)

// SignatureHeader is the §11.2.1 line 136 HMAC-SHA256 signature header
// the webhook sink stamps on every delivery. The value is
// `sha256=<hex>` over the exact request body, matching the de-facto
// webhook-signature convention so subscribers verify by recomputing the
// HMAC of the raw bytes they received.
const SignatureHeader = "X-Lenny-Signature"

// EventTypeHeader names the §11.2.1 event_type on the webhook request so
// a subscriber can route without parsing the body.
const EventTypeHeader = "X-Lenny-Event-Type"

const (
	// DefaultMaxAttempts bounds the exponential-backoff retry loop before
	// a delivery is dead-lettered. spec: §11.2.1 line 136.
	DefaultMaxAttempts = 5

	// DefaultBaseBackoff is the first inter-attempt delay; each
	// subsequent attempt doubles it (1s, 2s, 4s, ...).
	DefaultBaseBackoff = time.Second

	// DefaultTimeout bounds a single webhook POST.
	DefaultTimeout = 10 * time.Second
)

// EventMeta carries the routing/correlation fields a sink needs without
// re-parsing the payload: the per-tenant ledger coordinates and the
// event type. Dead-letter handlers and queue-topic keys use it.
type EventMeta struct {
	TenantID       string
	SequenceNumber uint64
	EventType      string
}

// metaOf extracts the EventMeta from a sealed billing event.
func metaOf(e billingstore.Event) EventMeta {
	return EventMeta{TenantID: e.TenantID, SequenceNumber: e.SequenceNumber, EventType: string(e.EventType)}
}

// Sink delivers one billing-event payload to a single downstream
// consumer. A Sink owns its own retry, backoff, and dead-letter policy;
// Deliver returns a non-nil error only after the sink has exhausted its
// own recovery and dead-lettered the event, so the fan-out Publisher
// treats a returned error as terminal for that sink and moves on.
type Sink interface {
	// Name identifies the sink in logs and metrics (e.g. "webhook",
	// "queue").
	Name() string
	// Deliver sends the already-marshaled JSON payload to the sink. meta
	// carries the §11.2.1 routing fields.
	Deliver(ctx context.Context, payload []byte, meta EventMeta) error
}

// DeadLetterFunc receives an event whose delivery a sink could not
// complete after exhausting its retries. The gateway wires it to a
// structured CRITICAL log and the dead-letter metric.
type DeadLetterFunc func(sink string, meta EventMeta, payload []byte, err error)

// Marshal renders a sealed billing event as the §11.2.1 delivery
// payload. The field names match the §15.1 metering wire so a webhook
// subscriber and a metering-API consumer see the same event shape.
func Marshal(e billingstore.Event) ([]byte, error) {
	p := payload{
		SchemaVersion:        e.SchemaVersion,
		SequenceNumber:       e.SequenceNumber,
		TenantID:             e.TenantID,
		UserID:               e.UserID,
		SessionID:            e.SessionID,
		ExperimentID:         e.ExperimentID,
		VariantID:            e.VariantID,
		EventType:            string(e.EventType),
		TokensInput:          e.TokensInput,
		TokensOutput:         e.TokensOutput,
		PodMinutes:           e.PodMinutes,
		CorrectsSequence:     e.CorrectsSequence,
		CorrectionReasonCode: string(e.CorrectionReasonCode),
		CorrectionDetail:     e.CorrectionDetail,
		Timestamp:            e.CreatedAt.UTC().Format(time.RFC3339Nano),
		Conditional:          e.Conditional,
	}
	return json.Marshal(p)
}

// payload is the §11.2.1 billing-event JSON delivered to a sink. It
// mirrors the §15.1 metering wire so the two surfaces never diverge.
type payload struct {
	SchemaVersion        uint32  `json:"schemaVersion"`
	SequenceNumber       uint64  `json:"sequenceNumber"`
	TenantID             string  `json:"tenantId"`
	UserID               string  `json:"userId,omitempty"`
	SessionID            string  `json:"sessionId,omitempty"`
	ExperimentID         string  `json:"experimentId,omitempty"`
	VariantID            string  `json:"variantId,omitempty"`
	EventType            string  `json:"eventType"`
	TokensInput          uint64  `json:"tokensInput,omitempty"`
	TokensOutput         uint64  `json:"tokensOutput,omitempty"`
	PodMinutes           float64 `json:"podMinutes,omitempty"`
	CorrectsSequence     uint64  `json:"correctsSequence,omitempty"`
	CorrectionReasonCode string  `json:"correctionReasonCode,omitempty"`
	CorrectionDetail     string  `json:"correctionDetail,omitempty"`
	Timestamp            string  `json:"timestamp"`
	*billingstore.Conditional
}

// Sign returns the §11.2.1 HMAC-SHA256 signature header value for body
// under secret, formatted as `sha256=<lowercase-hex>`. A subscriber
// verifies a delivery by recomputing this over the raw bytes received.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// httpDoer is the subset of *http.Client the webhook sink needs; tests
// inject a fake.
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// WebhookSink is the §11.2.1 webhook delivery sink. It POSTs each event
// as JSON with an HMAC-SHA256 signature header, retries failed
// deliveries with exponential backoff, and dead-letters after the
// attempt budget is exhausted.
type WebhookSink struct {
	url         string
	secret      []byte
	client      httpDoer
	maxAttempts int
	baseBackoff time.Duration
	sleep       func(context.Context, time.Duration)
	deadLetter  DeadLetterFunc
}

// WebhookOptions configures a WebhookSink. URL is required.
type WebhookOptions struct {
	// URL is the webhook endpoint. Required.
	URL string
	// Secret keys the HMAC-SHA256 signature. An empty secret omits the
	// signature header (the spec recommends a secret; the sink does not
	// force one so an internal collector behind mTLS can opt out).
	Secret []byte
	// Client overrides the default HTTP client (timeout DefaultTimeout).
	Client httpDoer
	// MaxAttempts overrides DefaultMaxAttempts.
	MaxAttempts int
	// BaseBackoff overrides DefaultBaseBackoff.
	BaseBackoff time.Duration
	// Sleep overrides the inter-attempt wait; tests inject a no-op so
	// the backoff loop does not block. It must return promptly when ctx
	// is cancelled.
	Sleep func(context.Context, time.Duration)
	// DeadLetter receives an event the sink could not deliver.
	DeadLetter DeadLetterFunc
}

// NewWebhookSink builds a WebhookSink from opts.
func NewWebhookSink(opts WebhookOptions) (*WebhookSink, error) {
	if opts.URL == "" {
		return nil, errors.New("billingsink: webhook URL is required")
	}
	s := &WebhookSink{
		url:         opts.URL,
		secret:      opts.Secret,
		client:      opts.Client,
		maxAttempts: opts.MaxAttempts,
		baseBackoff: opts.BaseBackoff,
		sleep:       opts.Sleep,
		deadLetter:  opts.DeadLetter,
	}
	if s.client == nil {
		s.client = &http.Client{Timeout: DefaultTimeout}
	}
	if s.maxAttempts <= 0 {
		s.maxAttempts = DefaultMaxAttempts
	}
	if s.baseBackoff <= 0 {
		s.baseBackoff = DefaultBaseBackoff
	}
	if s.sleep == nil {
		s.sleep = sleepContext
	}
	return s, nil
}

// Name implements Sink.
func (s *WebhookSink) Name() string { return "webhook" }

// Deliver implements Sink. It POSTs payload up to maxAttempts times,
// backing off exponentially between attempts, and dead-letters on
// exhaustion. A 2xx response is success; any other status or a
// transport error is a retryable failure.
func (s *WebhookSink) Deliver(ctx context.Context, payload []byte, meta EventMeta) error {
	var lastErr error
	for attempt := 1; attempt <= s.maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			lastErr = err
			break
		}
		lastErr = s.post(ctx, payload, meta)
		if lastErr == nil {
			return nil
		}
		if attempt < s.maxAttempts {
			// Exponential backoff: base * 2^(attempt-1).
			s.sleep(ctx, s.baseBackoff<<(attempt-1))
		}
	}
	if s.deadLetter != nil {
		s.deadLetter(s.Name(), meta, payload, lastErr)
	}
	return fmt.Errorf("billingsink: webhook delivery exhausted after %d attempts: %w", s.maxAttempts, lastErr)
}

// post performs one webhook POST and classifies the outcome.
func (s *WebhookSink) post(ctx context.Context, body []byte, meta EventMeta) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(EventTypeHeader, meta.EventType)
	if len(s.secret) > 0 {
		req.Header.Set(SignatureHeader, Sign(s.secret, body))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
}

// QueuePublisher publishes a billing-event payload to a message-queue
// topic. The concrete SQS / Google Pub/Sub / Kafka drivers implement it
// over their respective SDKs; the gateway injects the configured one.
type QueuePublisher interface {
	Publish(ctx context.Context, topic string, payload []byte, meta EventMeta) error
}

// QueueSink is the §11.2.1 message-queue delivery sink. It publishes
// each event to the configured topic through an injected QueuePublisher,
// retrying with exponential backoff and dead-lettering on exhaustion.
// The broker-specific QueuePublisher (SQS, Pub/Sub, Kafka) is supplied
// by the deployment.
type QueueSink struct {
	topic       string
	publisher   QueuePublisher
	maxAttempts int
	baseBackoff time.Duration
	sleep       func(context.Context, time.Duration)
	deadLetter  DeadLetterFunc
}

// QueueOptions configures a QueueSink. Topic and Publisher are required.
type QueueOptions struct {
	Topic       string
	Publisher   QueuePublisher
	MaxAttempts int
	BaseBackoff time.Duration
	Sleep       func(context.Context, time.Duration)
	DeadLetter  DeadLetterFunc
}

// NewQueueSink builds a QueueSink from opts.
func NewQueueSink(opts QueueOptions) (*QueueSink, error) {
	if opts.Topic == "" {
		return nil, errors.New("billingsink: queue topic is required")
	}
	if opts.Publisher == nil {
		return nil, errors.New("billingsink: queue publisher is required")
	}
	s := &QueueSink{
		topic:       opts.Topic,
		publisher:   opts.Publisher,
		maxAttempts: opts.MaxAttempts,
		baseBackoff: opts.BaseBackoff,
		sleep:       opts.Sleep,
		deadLetter:  opts.DeadLetter,
	}
	if s.maxAttempts <= 0 {
		s.maxAttempts = DefaultMaxAttempts
	}
	if s.baseBackoff <= 0 {
		s.baseBackoff = DefaultBaseBackoff
	}
	if s.sleep == nil {
		s.sleep = sleepContext
	}
	return s, nil
}

// Name implements Sink.
func (s *QueueSink) Name() string { return "queue" }

// Deliver implements Sink.
func (s *QueueSink) Deliver(ctx context.Context, payload []byte, meta EventMeta) error {
	var lastErr error
	for attempt := 1; attempt <= s.maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			lastErr = err
			break
		}
		lastErr = s.publisher.Publish(ctx, s.topic, payload, meta)
		if lastErr == nil {
			return nil
		}
		if attempt < s.maxAttempts {
			s.sleep(ctx, s.baseBackoff<<(attempt-1))
		}
	}
	if s.deadLetter != nil {
		s.deadLetter(s.Name(), meta, payload, lastErr)
	}
	return fmt.Errorf("billingsink: queue delivery exhausted after %d attempts: %w", s.maxAttempts, lastErr)
}

// Publisher fans a billing event out to every configured Sink for
// §11.2.1 "Both" redundancy: a webhook and a queue sink registered
// together each receive the event independently, and one sink's failure
// does not suppress the other. Delivery is best-effort per sink; each
// sink owns its retry and dead-letter policy.
type Publisher struct {
	sinks   []Sink
	onError func(sink string, meta EventMeta, err error)
}

// NewPublisher builds a fan-out over sinks. onError, when non-nil,
// receives the terminal error from a sink that dead-lettered an event.
func NewPublisher(sinks []Sink, onError func(string, EventMeta, error)) *Publisher {
	return &Publisher{sinks: sinks, onError: onError}
}

// Sinks returns the registered sink names, for startup logging.
func (p *Publisher) Sinks() []string {
	names := make([]string, 0, len(p.sinks))
	for _, s := range p.sinks {
		names = append(names, s.Name())
	}
	return names
}

// Empty reports whether the publisher has no sinks; the gateway skips
// wrapping the billing store when so.
func (p *Publisher) Empty() bool { return p == nil || len(p.sinks) == 0 }

// Publish delivers payload to every sink. It returns after all sinks
// have been attempted; a sink's terminal (post-dead-letter) error is
// reported via onError and does not stop the remaining sinks.
func (p *Publisher) Publish(ctx context.Context, payload []byte, meta EventMeta) {
	for _, s := range p.sinks {
		if err := s.Deliver(ctx, payload, meta); err != nil && p.onError != nil {
			p.onError(s.Name(), meta, err)
		}
	}
}

// sleepContext sleeps for d or until ctx is cancelled, whichever comes
// first.
func sleepContext(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
