// SPDX-License-Identifier: MIT

package scenkit

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

// HTTPClient is the shared connection-pooled client every HTTP-driven
// tier-7a scenario uses. Centralising it keeps the loopback ephemeral
// port range under the macOS limit even when many scenarios run back
// to back: each scenario's targets pool separately by host:port, so a
// shared client across scenarios is safe.
func HTTPClient() *http.Client { return pooledClient }

// maxConnsPerHost bounds the live connections the client opens to any
// single in-process gateway. Without a cap, a high-rate scenario (the
// SLO battery drives 1600+ requests/s over loopback) opens a fresh TCP
// connection whenever every pooled one is momentarily busy; each such
// connection enters TIME_WAIT for one MSL after the gateway listener
// closes at scenario teardown, and across the whole back-to-back
// battery the accumulation exhausts the macOS 49152-65535 ephemeral
// port range ("connect: can't assign requested address"). Capping forces
// the client to reuse a bounded pool and block rather than dial past it,
// so the live socket count per gateway stays bounded and TIME_WAIT churn
// is proportional to the cap rather than to the request rate. The value
// comfortably exceeds the scenarios' default VU counts so it does not
// throttle legitimate concurrency.
const maxConnsPerHost = 64

var pooledClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: maxConnsPerHost,
		MaxConnsPerHost:     maxConnsPerHost,
		IdleConnTimeout:     30 * time.Second,
	},
	Timeout: 5 * time.Second,
}

// CloseIdleConnections evicts the shared client's idle keep-alive
// connections. A tier-7a scenario calls this at teardown: each scenario
// boots its own in-process gateway on a fresh loopback port, and once
// that listener closes the pooled connections to it are dead. Evicting
// them promptly (rather than waiting out IdleConnTimeout) keeps idle
// sockets to retired gateways from accumulating across the back-to-back
// battery and competing for the ephemeral port range.
func CloseIdleConnections() { pooledClient.CloseIdleConnections() }

// Header is a key/value pair for DoJSON headers.
type Header struct{ K, V string }

// H is a shorthand for constructing a Header.
func H(k, v string) Header { return Header{K: k, V: v} }

// DoJSON builds an HTTP request with the supplied body and headers,
// dispatches it through HTTPClient, drains and closes the response
// body, and returns (status, body, err).
//
// The returned err is nil for any non-cancel error (so scenarios
// observe HTTP-level failures normally), and is the underlying
// transport error for cancel paths so the caller can use ctx.Err()
// to distinguish. The body is always drained even on error, so the
// pooled connection returns to the idle pool.
//
// On a successful HTTP call (status code received) the response body
// is read fully and returned as the second value. The Content-Type
// header is set to application/json automatically when body is
// non-nil.
func DoJSON(ctx context.Context, method, url string, body []byte, headers ...Header) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, h := range headers {
		req.Header.Set(h.K, h.V)
	}
	resp, err := pooledClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody, nil
}

// IsBenignCancel reports whether err is a transport-level cancel
// triggered by ctx expiring. Scenarios use it to suppress the
// run-end tail of requests that hit the loadgen duration boundary.
func IsBenignCancel(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	return ctx.Err() != nil
}
