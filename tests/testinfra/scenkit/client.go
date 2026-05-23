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

var pooledClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 200,
		IdleConnTimeout:     30 * time.Second,
	},
	Timeout: 5 * time.Second,
}

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
