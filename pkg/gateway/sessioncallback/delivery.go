// SPDX-License-Identifier: MIT

package sessioncallback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/gateway/eventbus"
	"github.com/lennylabs/lenny/pkg/webhookdelivery"
	"github.com/lennylabs/lenny/pkg/webhooksig"
)

// ContentType is the §14 line 114 callback request Content-Type: the
// body is a CloudEvents v1.0.2 JSON record.
const ContentType = "application/cloudevents+json"

// MaxAttempts is the §14 line 150 callback delivery budget: five total
// HTTP delivery attempts before the event is recorded undelivered.
const MaxAttempts = 5

// RetrySchedule is the §14 line 150 exponential backoff applied before
// each retry. With MaxAttempts == 5 the worker consumes the first four
// entries as the inter-attempt waits (before attempts 2-5); the fifth
// entry is the documented backoff that would precede a sixth attempt the
// budget does not take. spec: §14 line 150.
var RetrySchedule = []time.Duration{
	10 * time.Second,
	30 * time.Second,
	60 * time.Second,
	300 * time.Second,
	900 * time.Second,
}

// SecretOpener recovers the plaintext callbackSecret from its
// KMS-envelope-sealed form for the (tenant) KEK alias. spec: §14 line
// 139 (callbackSecret KMS-envelope storage).
type SecretOpener func(ctx context.Context, tenantID string, sealed []byte) ([]byte, error)

// Finalizer persists the post-delivery §14 callback state: it clears the
// sealed secret (the §14 line 139 NULL-on-terminal rule) and, when
// undelivered is non-nil, records the exhausted event for the §15.1
// GET /v1/sessions/{id}/webhook-events query. spec: §14 lines 139, 150.
type Finalizer func(ctx context.Context, tenantID, sessionID string, undelivered *DeliveryRecord) error

// DeliveryRecord is one undelivered §14 webhook event surfaced by
// GET /v1/sessions/{id}/webhook-events after the retry budget is spent.
type DeliveryRecord struct {
	EventID     string    `json:"eventId"`
	EventType   string    `json:"eventType"`
	CallbackURL string    `json:"callbackUrl"`
	Body        []byte    `json:"body"`
	Attempts    int       `json:"attempts"`
	LastError   string    `json:"lastError,omitempty"`
	LastStatus  int       `json:"lastStatus,omitempty"`
	FailedAt    time.Time `json:"failedAt"`
}

// Job is one §14 callback delivery the dispatcher carries from a session
// terminal-state transition through the retry budget.
type Job struct {
	TenantID      string
	SessionID     string
	RootSessionID string
	CallbackURL   string
	PinnedIP      netip.Addr
	SealedSecret  []byte
	ShortName     string // §16.6 short name, e.g. "session_completed"
	Subject       string // CloudEvents subject, e.g. "session/sess_abc123"
	Data          json.RawMessage
}

// Config constructs a Dispatcher. The test seams (NowFn, Sleep,
// NewClient) default to wall-clock behaviour when unset.
type Config struct {
	GatewayID   string
	Opener      SecretOpener
	Finalizer   Finalizer
	Concurrency int

	// Test seams.
	NowFn      func() time.Time
	Sleep      func(ctx context.Context, d time.Duration) bool
	NewClient  func(pinned netip.Addr) *http.Client
	Revalidate func(pinned netip.Addr) bool
}

// Dispatcher delivers §14 session-completion webhooks from an isolated
// goroutine pool (§14 line 111). Each Job runs the bounded-retry budget
// on its own goroutine, signs every attempt with a fresh delivery
// timestamp, and finalizes the callback state when the budget resolves.
type Dispatcher struct {
	gatewayID string
	opener    SecretOpener
	finalize  Finalizer
	nowFn      func() time.Time
	sleep      func(ctx context.Context, d time.Duration) bool
	newClient  func(pinned netip.Addr) *http.Client
	revalidate func(pinned netip.Addr) bool

	sem    chan struct{}
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// NewDispatcher builds a Dispatcher. Concurrency < 1 defaults to 8.
func NewDispatcher(cfg Config) *Dispatcher {
	conc := cfg.Concurrency
	if conc < 1 {
		conc = 8
	}
	now := cfg.NowFn
	if now == nil {
		now = time.Now
	}
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	newClient := cfg.NewClient
	if newClient == nil {
		newClient = pinnedHTTPClient
	}
	revalidate := cfg.Revalidate
	if revalidate == nil {
		revalidate = isPublicAddr
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Dispatcher{
		gatewayID:  cfg.GatewayID,
		opener:     cfg.Opener,
		finalize:   cfg.Finalizer,
		nowFn:      now,
		sleep:      sleep,
		newClient:  newClient,
		revalidate: revalidate,
		sem:        make(chan struct{}, conc),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Enqueue schedules a callback delivery. It returns immediately; the
// delivery runs on a pooled goroutine. A Job enqueued after Close is
// dropped. spec: §14 line 111 (isolated callback worker).
func (d *Dispatcher) Enqueue(j Job) {
	if d == nil || j.CallbackURL == "" {
		return
	}
	select {
	case <-d.ctx.Done():
		return
	default:
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		select {
		case d.sem <- struct{}{}:
			defer func() { <-d.sem }()
		case <-d.ctx.Done():
			return
		}
		d.run(j)
	}()
}

// Close stops accepting new jobs and waits for in-flight deliveries to
// settle so the §14 line 139 secret-clear runs before shutdown.
func (d *Dispatcher) Close() {
	if d == nil {
		return
	}
	d.cancel()
	d.wg.Wait()
}

// run executes one Job's full retry budget and finalizes its state.
func (d *Dispatcher) run(j Job) {
	body, err := d.buildBody(j)
	if err != nil {
		d.finalizeUndelivered(j, nil, 0, 0, "build event: "+err.Error())
		return
	}
	eventID := extractEventID(body)

	var secret []byte
	if len(j.SealedSecret) > 0 && d.opener != nil {
		secret, err = d.opener(d.ctx, j.TenantID, j.SealedSecret)
		if err != nil {
			d.finalizeUndelivered(j, body, 0, 0, "open callbackSecret: "+err.Error())
			return
		}
	}

	client := d.newClient(j.PinnedIP)
	var lastStatus int
	var lastErr string
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		if attempt > 1 {
			delay := RetrySchedule[attempt-2]
			if !d.sleep(d.ctx, delay) {
				lastErr = "dispatcher shutdown before retry"
				d.finalizeUndelivered(j, body, attempt-1, lastStatus, lastErr)
				return
			}
		}
		// spec: §14 line 110 — re-check the pin is still public; a pin
		// that resolved private since registration must not be dialed.
		if !d.revalidate(j.PinnedIP) {
			d.finalizeUndelivered(j, body, attempt-1, 0, "pinned IP is not public")
			return
		}
		status, derr := d.attempt(client, j, body, secret, eventID, attempt)
		if derr == nil && status >= 200 && status <= 299 {
			d.finalizeSuccess(j)
			return
		}
		lastStatus = status
		if derr != nil {
			lastErr = derr.Error()
		} else {
			lastErr = fmt.Sprintf("receiver returned HTTP %d", status)
		}
		retryable := derr != nil || webhookdelivery.RetryableStatus(status)
		if !retryable {
			break
		}
	}
	d.finalizeUndelivered(j, body, MaxAttempts, lastStatus, lastErr)
}

// attempt performs one signed delivery and reports the receiver status
// or a transport error.
func (d *Dispatcher) attempt(client *http.Client, j Job, body, secret []byte, eventID string, attempt int) (int, error) {
	req, err := http.NewRequestWithContext(d.ctx, http.MethodPost, j.CallbackURL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", ContentType)
	// spec: §14 line 139 — sign with a fresh delivery timestamp each
	// attempt; the CloudEvents time inside body stays fixed.
	if len(secret) > 0 {
		req.Header.Set("X-Lenny-Signature", webhooksig.Sign(secret, body, d.nowFn()))
	}
	req.Header.Set("X-Lenny-Event-Id", eventID)
	req.Header.Set("X-Lenny-Event-Type", eventbusType(j.ShortName))
	req.Header.Set("X-Lenny-Delivery-Attempt", fmt.Sprintf("%d", attempt))

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// buildBody renders the §14 CloudEvents v1.0.2 envelope for a Job. The
// envelope reuses the shared eventbus builder so the callback id/source
// format matches the EventBus, SSE, and §25 webhook transports. spec:
// §14 lines 114-137.
func (d *Dispatcher) buildBody(j Job) ([]byte, error) {
	ev, err := eventbus.NewEvent(eventbus.NewEventInput{
		PublisherID:   d.gatewayID,
		Component:     "gateway",
		TenantID:      j.TenantID,
		ShortName:     j.ShortName,
		Subject:       j.Subject,
		Data:          j.Data,
		RootSessionID: j.RootSessionID,
		Now:           d.nowFn,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(ev)
}

func (d *Dispatcher) finalizeSuccess(j Job) {
	if d.finalize == nil {
		return
	}
	_ = d.finalize(context.Background(), j.TenantID, j.SessionID, nil)
}

func (d *Dispatcher) finalizeUndelivered(j Job, body []byte, attempts, status int, lastErr string) {
	if d.finalize == nil {
		return
	}
	rec := &DeliveryRecord{
		EventID:     extractEventID(body),
		EventType:   eventbusType(j.ShortName),
		CallbackURL: j.CallbackURL,
		Body:        body,
		Attempts:    attempts,
		LastError:   lastErr,
		LastStatus:  status,
		FailedAt:    d.nowFn().UTC(),
	}
	_ = d.finalize(context.Background(), j.TenantID, j.SessionID, rec)
}

// eventbusType returns the dev.lenny.<short_name> CloudEvents type.
func eventbusType(shortName string) string {
	if shortName == "" {
		return ""
	}
	return "dev.lenny." + shortName
}

// extractEventID reads the CloudEvents id out of a rendered envelope so
// the undelivered record and the X-Lenny-Event-Id header agree with the
// body. A malformed body yields a fresh UUID so the record stays keyed.
func extractEventID(body []byte) string {
	var probe struct {
		ID string `json:"id"`
	}
	if len(body) > 0 && json.Unmarshal(body, &probe) == nil && probe.ID != "" {
		return probe.ID
	}
	return uuid.NewString()
}

// sleepCtx waits d or returns false early when ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
