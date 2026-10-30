// SPDX-License-Identifier: MIT

// Package siem is the §11.7 SIEM forwarder. For deployments that
// require compliance-grade audit integrity, audit events are streamed
// to an external immutable log (SIEM, cloud audit service, or
// append-only object storage) in addition to Postgres storage. The
// external SIEM is the independent copy a database superuser cannot
// alter — the only audit copy that detects a superuser who
// reconstructs the Postgres hash chain.
//
// The forwarder consumes OCSF v1.1.0 records (the §11.7 single
// canonical wire format) and streams them to the configured
// audit.siem.endpoint. The §11.7 SIEM delivery pointer advances past
// dead-lettered rows — the OCSF translator emits a translation-failure
// receipt in place of an untranslatable event, so a persistently
// failing event does not halt the per-tenant SIEM stream.
//
// SIEM wire protocol seam. §11.7 mandates OCSF as the record format
// but does not pin the SIEM transport for v1 (Splunk HEC, Sumo,
// append-only S3, and cloud audit services each differ). The forwarder
// is therefore built against the Sink interface; HTTPSink is the
// reference transport — a newline-delimited OCSF JSON POST with an
// optional HMAC-SHA256 batch signature, matching the documented
// tests/testinfra/stubs/siem stub contract. A deployer targeting a
// specific SIEM implements Sink for that product.
package siem

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/audit/ocsf"
)

// signatureHeader is the §11.7 / stub-contract HMAC header the HTTP
// sink sets when a shared secret is configured.
const signatureHeader = "X-Lenny-SIEM-Signature"

// Sink is the §11.7 SIEM transport seam. An implementation delivers a
// batch of OCSF records to an external immutable log. DeliverBatch
// must be all-or-nothing from the forwarder's perspective: a returned
// error means the batch was not durably accepted and the forwarder
// retries it.
type Sink interface {
	// DeliverBatch streams a batch of OCSF wire records to the SIEM.
	// recs are the already-marshalled OCSF JSON objects. A non-nil
	// error causes the forwarder to retry the batch.
	DeliverBatch(ctx context.Context, recs []json.RawMessage) error
}

// HTTPSink is the reference §11.7 SIEM transport: it POSTs a batch as
// newline-delimited OCSF JSON to the configured endpoint, optionally
// signing the body with HMAC-SHA256. It is the transport the SIEM stub
// in tests/testinfra/stubs/siem accepts.
type HTTPSink struct {
	endpoint string
	secret   string
	client   *http.Client
}

// HTTPSinkOptions configures NewHTTPSink.
type HTTPSinkOptions struct {
	// Endpoint is the audit.siem.endpoint URL.
	Endpoint string

	// Secret, when set, enables the HMAC-SHA256 batch signature in the
	// X-Lenny-SIEM-Signature header.
	Secret string

	// Client is the HTTP client; defaults to a client with a 10s
	// timeout.
	Client *http.Client
}

// NewHTTPSink returns an HTTPSink for the configured endpoint.
func NewHTTPSink(opts HTTPSinkOptions) *HTTPSink {
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &HTTPSink{endpoint: opts.Endpoint, secret: opts.Secret, client: client}
}

// DeliverBatch POSTs the OCSF records as a JSON array. A non-2xx
// response is a delivery failure the forwarder retries.
func (s *HTTPSink) DeliverBatch(ctx context.Context, recs []json.RawMessage) error {
	body, err := json.Marshal(recs)
	if err != nil {
		return fmt.Errorf("siem: marshal batch: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("siem: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.secret != "" {
		mac := hmac.New(sha256.New, []byte(s.secret))
		mac.Write(body)
		req.Header.Set(signatureHeader, hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("siem: deliver to %s: %w", s.endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("siem: endpoint %s returned %d", s.endpoint, resp.StatusCode)
	}
	return nil
}

// Metrics is the §11.7 SIEM forwarder metric surface. A nil Metrics is
// a valid no-op.
type Metrics interface {
	// Delivered counts records successfully streamed to the SIEM.
	Delivered(n int)

	// DeliveryFailed counts a batch delivery that failed (after
	// retries are exhausted). Drives the §11.7 SIEM failure-rate
	// health check.
	DeliveryFailed()

	// DeliveryRetried counts a batch delivery retry.
	DeliveryRetried()
}

// ForwarderConfig pins the §11.7 SIEM forwarder retry parameters.
type ForwarderConfig struct {
	// MaxRetries is how many times a failed batch is retried before
	// the forwarder gives up and reports the delivery failure.
	MaxRetries int

	// RetryBackoff is the base backoff between retries; it doubles per
	// attempt.
	RetryBackoff time.Duration

	// BatchSize bounds how many records one DeliverBatch call carries.
	BatchSize int
}

// DefaultForwarderConfig returns the §11.7 SIEM forwarder defaults.
func DefaultForwarderConfig() ForwarderConfig {
	return ForwarderConfig{
		MaxRetries:   3,
		RetryBackoff: 100 * time.Millisecond,
		BatchSize:    128,
	}
}

// Forwarder streams OCSF-translated audit records to a SIEM Sink with
// the §11.7 retry / state handling. It satisfies ocsf.Sink, so the
// OCSF translator's multicast fan-out can deliver straight into it: a
// translated record flows translator → Forwarder → Sink → SIEM.
type Forwarder struct {
	sink    Sink
	cfg     ForwarderConfig
	metrics Metrics
	sleep   func(time.Duration)

	mu      sync.Mutex
	healthy bool
}

// NewForwarder returns a Forwarder over sink. cfg's zero fields are
// filled from DefaultForwarderConfig; metrics may be nil.
func NewForwarder(sink Sink, cfg ForwarderConfig, metrics Metrics) *Forwarder {
	def := DefaultForwarderConfig()
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = def.MaxRetries
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = def.RetryBackoff
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = def.BatchSize
	}
	return &Forwarder{
		sink:    sink,
		cfg:     cfg,
		metrics: metrics,
		sleep:   time.Sleep,
		healthy: true,
	}
}

// Deliver implements ocsf.Sink. It forwards a single OCSF record to
// the SIEM as a one-record batch with the §11.7 retry policy. The
// translator's multicast fan-out calls this for every translated row.
func (f *Forwarder) Deliver(ctx context.Context, _ string, _ string, rec ocsf.Record) error {
	b, err := ocsf.MarshalRecord(rec)
	if err != nil {
		return fmt.Errorf("siem: marshal OCSF record: %w", err)
	}
	return f.ForwardBatch(ctx, []json.RawMessage{b})
}

// ForwardBatch streams a batch of OCSF records to the SIEM with the
// §11.7 retry policy: a failed DeliverBatch is retried up to MaxRetries
// times with exponential backoff. On final failure the forwarder marks
// itself unhealthy (the §11.7 SIEM delivery health check reads this)
// and returns the error so the caller can react.
func (f *Forwarder) ForwardBatch(ctx context.Context, recs []json.RawMessage) error {
	if len(recs) == 0 {
		return nil
	}
	var lastErr error
	for attempt := 0; attempt <= f.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			if f.metrics != nil {
				f.metrics.DeliveryRetried()
			}
			backoff := f.cfg.RetryBackoff << (attempt - 1)
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			f.sleep(backoff)
		}
		err := f.sink.DeliverBatch(ctx, recs)
		if err == nil {
			f.setHealthy(true)
			if f.metrics != nil {
				f.metrics.Delivered(len(recs))
			}
			return nil
		}
		lastErr = err
	}
	f.setHealthy(false)
	if f.metrics != nil {
		f.metrics.DeliveryFailed()
	}
	return fmt.Errorf("siem: batch delivery failed after %d retries: %w", f.cfg.MaxRetries, lastErr)
}

// Healthy reports whether the most recent SIEM delivery succeeded. The
// §11.7 /healthz degraded-status check reads this; the gateway reports
// degraded status when SIEM delivery is failing.
func (f *Forwarder) Healthy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.healthy
}

func (f *Forwarder) setHealthy(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.healthy = v
}

// ValidateConnectivity implements the §11.7 startup SIEM connectivity
// validation: a test event is sent and the gateway refuses to start
// until acknowledgement is received. The caller (gateway startup)
// treats a non-nil error as fatal.
func (f *Forwarder) ValidateConnectivity(ctx context.Context) error {
	probe := ocsf.Record{
		ClassUID:    ocsf.ClassAppSecurityFinding,
		CategoryUID: 2,
		ActivityID:  ocsf.ActivityCreate,
		TypeUID:     ocsf.ClassAppSecurityFinding*100 + ocsf.ActivityCreate,
		Time:        time.Now().UTC().UnixMilli(),
		SeverityID:  1,
		Metadata: ocsf.Metadata{
			UID:       "siem-connectivity-probe",
			Version:   ocsf.Version,
			TenantUID: "platform",
			Product:   ocsf.Product{Name: "Lenny", VendorName: "Lenny"},
		},
		Finding: &ocsf.Finding{Title: "SIEM connectivity probe"},
	}
	b, err := ocsf.MarshalRecord(probe)
	if err != nil {
		return fmt.Errorf("siem: marshal connectivity probe: %w", err)
	}
	if err := f.sink.DeliverBatch(ctx, []json.RawMessage{b}); err != nil {
		f.setHealthy(false)
		return fmt.Errorf("siem: startup connectivity validation failed: %w", err)
	}
	f.setHealthy(true)
	return nil
}

// CountingMetrics is an in-memory Metrics implementation for tests and
// the §11.7 SIEM delivery-success-rate health check. It is
// goroutine-safe.
type CountingMetrics struct {
	mu        sync.Mutex
	delivered int
	failed    int
	retried   int
}

// NewCountingMetrics returns an empty CountingMetrics.
func NewCountingMetrics() *CountingMetrics {
	return &CountingMetrics{}
}

// Delivered records n successfully streamed records.
func (m *CountingMetrics) Delivered(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delivered += n
}

// DeliveryFailed records a failed batch.
func (m *CountingMetrics) DeliveryFailed() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failed++
}

// DeliveryRetried records a retry.
func (m *CountingMetrics) DeliveryRetried() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retried++
}

// DeliveredCount returns the total streamed-record count.
func (m *CountingMetrics) DeliveredCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.delivered
}

// FailedCount returns the failed-batch count.
func (m *CountingMetrics) FailedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failed
}

// RetriedCount returns the retry count.
func (m *CountingMetrics) RetriedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.retried
}

// FailureRate returns the §11.7 SIEM delivery failure rate as a
// percentage. The gateway compares it against
// audit.siem.failureThresholdPercent (default 5%).
func (m *CountingMetrics) FailureRate() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := m.delivered + m.failed
	if total == 0 {
		return 0
	}
	return float64(m.failed) / float64(total) * 100
}
