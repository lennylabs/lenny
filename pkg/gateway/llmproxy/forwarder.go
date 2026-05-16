// SPDX-License-Identifier: MIT

package llmproxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

// defaultUpstreamTimeout bounds a single non-streaming upstream call
// when the Forwarder is constructed without an explicit HTTP client.
const defaultUpstreamTimeout = 120 * time.Second

// ErrCircuitOpen reports that the LLM Proxy circuit breaker is open and
// rejected the request before any upstream call was made (§4.9). The
// proxy surfaces it to the agent pod as PROVIDER_UNAVAILABLE.
var ErrCircuitOpen = errors.New("llmproxy: upstream circuit breaker is open")

// Forwarder sends a translated UpstreamRequest to the upstream LLM
// provider, gating each call through a CircuitBreaker (§4.9). A
// transport failure or a 5xx response records a breaker failure; any
// completed HTTP exchange below 500 records a breaker success, because
// a reachable upstream that returns a 4xx is healthy from the breaker's
// point of view — only 5xx and pre-first-byte transport failures feed
// the breaker.
type Forwarder struct {
	// Client is the HTTP client used for upstream calls. A nil client
	// selects a client bounded by defaultUpstreamTimeout.
	Client *http.Client
	// Breaker gates upstream calls. Required.
	Breaker *CircuitBreaker
}

// Forward dials the upstream provider for the translated request. When
// the circuit breaker is open it returns ErrCircuitOpen without
// dialing. A transport failure returns a TranslationError tagged
// timeout or upstream_5xx; a completed HTTP exchange returns the
// UpstreamResponse regardless of status so the translator can classify
// the status for the agent pod.
func (f *Forwarder) Forward(ctx context.Context, req *UpstreamRequest) (*UpstreamResponse, error) {
	if !f.Breaker.Allow() {
		return nil, ErrCircuitOpen
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		f.Breaker.RecordFailure()
		return nil, translationErrorf(ErrUpstream5xx, "build upstream request: %v", err)
	}
	for k, v := range req.Header {
		httpReq.Header.Set(k, v)
	}

	resp, err := f.client().Do(httpReq)
	if err != nil {
		f.Breaker.RecordFailure()
		if isTimeout(err) {
			return nil, translationErrorf(ErrTimeout, "upstream request timed out: %v", err)
		}
		return nil, translationErrorf(ErrUpstream5xx, "upstream request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		f.Breaker.RecordFailure()
		return nil, translationErrorf(ErrUpstream5xx, "read upstream response: %v", err)
	}

	if resp.StatusCode >= 500 {
		f.Breaker.RecordFailure()
	} else {
		f.Breaker.RecordSuccess()
	}
	return &UpstreamResponse{StatusCode: resp.StatusCode, Body: body}, nil
}

// client returns the configured HTTP client or a default bounded one.
func (f *Forwarder) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return &http.Client{Timeout: defaultUpstreamTimeout}
}

// isTimeout reports whether err is a context deadline or a network
// timeout, the failures the §4.9 taxonomy classifies as `timeout`.
func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
