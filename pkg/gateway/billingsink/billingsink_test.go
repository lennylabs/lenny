// SPDX-License-Identifier: MIT

package billingsink

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
)

// fakeDoer is an injectable httpDoer that returns a scripted sequence of
// (status, error) outcomes and records every request it sees.
type fakeDoer struct {
	mu       sync.Mutex
	statuses []int
	errs     []error
	reqs     []*http.Request
	bodies   [][]byte
	i        int
}

func (f *fakeDoer) Do(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	f.reqs = append(f.reqs, r)
	f.bodies = append(f.bodies, body)
	idx := f.i
	f.i++
	if idx < len(f.errs) && f.errs[idx] != nil {
		return nil, f.errs[idx]
	}
	status := http.StatusOK
	if idx < len(f.statuses) {
		status = f.statuses[idx]
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

func (f *fakeDoer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.i
}

func sampleEvent() billingstore.Event {
	return billingstore.Event{
		TenantID:       "acme",
		SequenceNumber: 7,
		SchemaVersion:  1,
		SessionID:      "sess_1",
		EventType:      billingstore.EventSessionCompleted,
		TokensInput:    11,
		TokensOutput:   22,
	}
}

// spec: §11.2.1 line 136 — the webhook sink POSTs each event with an
// HMAC-SHA256 signature header over the exact body.
func TestWebhookSink_SignsAndDelivers_spec_11_2_1_136(t *testing.T) {
	doer := &fakeDoer{statuses: []int{http.StatusOK}}
	secret := []byte("topsecret")
	sink, err := NewWebhookSink(WebhookOptions{URL: "https://example/hook", Secret: secret, Client: doer,
		Sleep: func(context.Context, time.Duration) {}})
	if err != nil {
		t.Fatalf("NewWebhookSink: %v", err)
	}
	body, _ := Marshal(sampleEvent())
	if err := sink.Deliver(context.Background(), body, EventMeta{TenantID: "acme", SequenceNumber: 7, EventType: "session.completed"}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if doer.count() != 1 {
		t.Fatalf("attempts = %d, want 1", doer.count())
	}
	req := doer.reqs[0]
	if got := req.Header.Get(EventTypeHeader); got != "session.completed" {
		t.Errorf("%s = %q, want session.completed", EventTypeHeader, got)
	}
	// The signature header must be sha256=HMAC(secret, body).
	mac := hmac.New(sha256.New, secret)
	mac.Write(doer.bodies[0])
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got := req.Header.Get(SignatureHeader); got != want {
		t.Errorf("%s = %q, want %q", SignatureHeader, got, want)
	}
}

// spec: §11.2.1 line 136 — an empty secret omits the signature header
// (an internal collector behind mTLS may opt out).
func TestWebhookSink_NoSecretOmitsSignature_spec_11_2_1_136(t *testing.T) {
	doer := &fakeDoer{statuses: []int{http.StatusNoContent}}
	sink, _ := NewWebhookSink(WebhookOptions{URL: "https://example/hook", Client: doer,
		Sleep: func(context.Context, time.Duration) {}})
	if err := sink.Deliver(context.Background(), []byte("{}"), EventMeta{}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if got := doer.reqs[0].Header.Get(SignatureHeader); got != "" {
		t.Errorf("signature header = %q, want empty", got)
	}
}

// spec: §11.2.1 line 136 — failed deliveries are retried with
// exponential backoff; a transient failure followed by success counts as
// delivered without a dead-letter.
func TestWebhookSink_RetriesThenSucceeds_spec_11_2_1_136(t *testing.T) {
	doer := &fakeDoer{statuses: []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusOK}}
	var deadLettered bool
	var sleeps int
	sink, _ := NewWebhookSink(WebhookOptions{URL: "https://x/h", Client: doer, MaxAttempts: 5,
		Sleep:      func(context.Context, time.Duration) { sleeps++ },
		DeadLetter: func(string, EventMeta, []byte, error) { deadLettered = true }})
	if err := sink.Deliver(context.Background(), []byte("{}"), EventMeta{}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if doer.count() != 3 {
		t.Errorf("attempts = %d, want 3", doer.count())
	}
	if sleeps != 2 {
		t.Errorf("backoff sleeps = %d, want 2 (between the 3 attempts)", sleeps)
	}
	if deadLettered {
		t.Errorf("dead-letter fired on an eventually-successful delivery")
	}
}

// spec: §11.2.1 line 136 — deliveries are dead-lettered after the retry
// budget is exhausted; Deliver returns the terminal error.
func TestWebhookSink_DeadLettersOnExhaustion_spec_11_2_1_136(t *testing.T) {
	doer := &fakeDoer{errs: []error{errors.New("dial"), errors.New("dial"), errors.New("dial")}}
	var dl struct {
		sink string
		meta EventMeta
	}
	sink, _ := NewWebhookSink(WebhookOptions{URL: "https://x/h", Client: doer, MaxAttempts: 3,
		Sleep:      func(context.Context, time.Duration) {},
		DeadLetter: func(s string, m EventMeta, _ []byte, _ error) { dl.sink = s; dl.meta = m }})
	err := sink.Deliver(context.Background(), []byte("{}"), EventMeta{TenantID: "acme", SequenceNumber: 9})
	if err == nil {
		t.Fatalf("Deliver returned nil, want exhaustion error")
	}
	if doer.count() != 3 {
		t.Errorf("attempts = %d, want 3", doer.count())
	}
	if dl.sink != "webhook" || dl.meta.SequenceNumber != 9 {
		t.Errorf("dead-letter sink/meta = %q/%d, want webhook/9", dl.sink, dl.meta.SequenceNumber)
	}
}

// A cancelled context stops the retry loop and reports the cancellation
// rather than spinning through the attempt budget.
func TestWebhookSink_StopsOnContextCancel(t *testing.T) {
	doer := &fakeDoer{errs: []error{errors.New("x"), errors.New("x"), errors.New("x"), errors.New("x"), errors.New("x")}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sink, _ := NewWebhookSink(WebhookOptions{URL: "https://x/h", Client: doer, MaxAttempts: 5,
		Sleep:      func(context.Context, time.Duration) {},
		DeadLetter: func(string, EventMeta, []byte, error) {}})
	if err := sink.Deliver(ctx, []byte("{}"), EventMeta{}); err == nil {
		t.Fatalf("Deliver returned nil on a cancelled context")
	}
	if doer.count() != 0 {
		t.Errorf("attempts = %d, want 0 on pre-cancelled context", doer.count())
	}
}

func TestNewWebhookSink_RequiresURL(t *testing.T) {
	if _, err := NewWebhookSink(WebhookOptions{}); err == nil {
		t.Fatalf("NewWebhookSink with empty URL returned nil error")
	}
}

// spec: §11.2.1 line 137 — the message-queue sink publishes through the
// injected QueuePublisher, retrying and dead-lettering like the webhook.
func TestQueueSink_DeliversAndRetries_spec_11_2_1_137(t *testing.T) {
	var calls int
	pub := queueFunc(func(_ context.Context, topic string, _ []byte, _ EventMeta) error {
		calls++
		if calls < 2 {
			return errors.New("broker unavailable")
		}
		return nil
	})
	sink, err := NewQueueSink(QueueOptions{Topic: "billing", Publisher: pub, MaxAttempts: 3,
		Sleep: func(context.Context, time.Duration) {}})
	if err != nil {
		t.Fatalf("NewQueueSink: %v", err)
	}
	if sink.Name() != "queue" {
		t.Errorf("Name = %q, want queue", sink.Name())
	}
	if err := sink.Deliver(context.Background(), []byte("{}"), EventMeta{}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if calls != 2 {
		t.Errorf("publish calls = %d, want 2", calls)
	}
}

func TestNewQueueSink_RequiresTopicAndPublisher(t *testing.T) {
	if _, err := NewQueueSink(QueueOptions{Publisher: queueFunc(func(context.Context, string, []byte, EventMeta) error { return nil })}); err == nil {
		t.Fatalf("missing topic accepted")
	}
	if _, err := NewQueueSink(QueueOptions{Topic: "t"}); err == nil {
		t.Fatalf("missing publisher accepted")
	}
}

// spec: §11.2.1 line 138 — registering a webhook and a queue sink
// together delivers each event to both for redundancy; one sink's
// terminal failure does not suppress the other.
func TestPublisher_FansOutToAllSinks_spec_11_2_1_138(t *testing.T) {
	good := &recordSink{name: "queue"}
	bad := &recordSink{name: "webhook", fail: errors.New("dead-lettered")}
	var onErr struct {
		sink string
		seq  uint64
	}
	pub := NewPublisher([]Sink{bad, good}, func(s string, m EventMeta, _ error) { onErr.sink = s; onErr.seq = m.SequenceNumber })
	if pub.Empty() {
		t.Fatalf("Empty() true with two sinks")
	}
	pub.Publish(context.Background(), []byte("{}"), EventMeta{SequenceNumber: 3})
	if good.calls != 1 {
		t.Errorf("healthy sink calls = %d, want 1 (not suppressed by the failing sink)", good.calls)
	}
	if bad.calls != 1 {
		t.Errorf("failing sink calls = %d, want 1", bad.calls)
	}
	if onErr.sink != "webhook" || onErr.seq != 3 {
		t.Errorf("onError = %q/%d, want webhook/3", onErr.sink, onErr.seq)
	}
	if names := pub.Sinks(); len(names) != 2 {
		t.Errorf("Sinks() = %v, want 2 names", names)
	}
}

func TestPublisher_EmptyOnNilOrNoSinks(t *testing.T) {
	if !(*Publisher)(nil).Empty() {
		t.Errorf("nil publisher Empty() = false")
	}
	if !NewPublisher(nil, nil).Empty() {
		t.Errorf("no-sink publisher Empty() = false")
	}
}

// spec: §11.2.1 — the delivery payload mirrors the §15.1 metering wire
// field names so a webhook subscriber and a metering consumer see the
// same shape.
func TestMarshal_UsesMeteringFieldNames_spec_11_2_1(t *testing.T) {
	body, err := Marshal(sampleEvent())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	for _, k := range []string{"schemaVersion", "sequenceNumber", "tenantId", "eventType", "timestamp"} {
		if _, ok := m[k]; !ok {
			t.Errorf("payload missing required key %q (got %v)", k, m)
		}
	}
	if m["eventType"] != "session.completed" {
		t.Errorf("eventType = %v, want session.completed", m["eventType"])
	}
}

func TestSign_IsDeterministicHMAC(t *testing.T) {
	got := Sign([]byte("k"), []byte("body"))
	mac := hmac.New(sha256.New, []byte("k"))
	mac.Write([]byte("body"))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got != want {
		t.Errorf("Sign = %q, want %q", got, want)
	}
}

// queueFunc adapts a function to the QueuePublisher interface.
type queueFunc func(context.Context, string, []byte, EventMeta) error

func (f queueFunc) Publish(ctx context.Context, topic string, payload []byte, meta EventMeta) error {
	return f(ctx, topic, payload, meta)
}

// recordSink is a Sink that records its calls and optionally fails.
type recordSink struct {
	name  string
	fail  error
	calls int
}

func (s *recordSink) Name() string { return s.name }
func (s *recordSink) Deliver(_ context.Context, _ []byte, _ EventMeta) error {
	s.calls++
	return s.fail
}
