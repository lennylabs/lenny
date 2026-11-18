// SPDX-License-Identifier: MIT

package requestmetrics

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Recorder receives the request metrics the middleware measures. The
// gateway's metric registry implements it.
type Recorder interface {
	// ObserveRequest records one finished request: its method, its route
	// template, its status class (2xx, 3xx, 4xx, or 5xx), and its duration
	// in seconds.
	ObserveRequest(method, route, statusClass string, seconds float64)
	// AddInflightRequests moves the count of requests in flight by delta.
	AddInflightRequests(delta int64)
}

// Wrap returns an http.Handler that records the §16.1 request metrics for
// every request to inner through rec. The route label comes from routeOf,
// so high-cardinality path segments (session ids, blob refs) collapse to a
// stable route template. The in-flight count rises before inner runs and
// falls after it returns, so the §16.1 lenny_gateway_request_queue_depth
// gauge, the primary scale-out trigger of §4.1 (SCL-026), reflects the
// instantaneous concurrent-request count on the replica.
func Wrap(inner http.Handler, rec Recorder, routeOf func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := routeOf(r)
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		rec.AddInflightRequests(1)
		defer rec.AddInflightRequests(-1)
		inner.ServeHTTP(sr, r)
		rec.ObserveRequest(r.Method, route, statusClass(sr.status), time.Since(start).Seconds())
	})
}

// statusRecorder captures the response status code so the middleware can
// label by status class.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

// Flush forwards to the underlying http.Flusher when the embedded
// ResponseWriter supports streaming. The §15.1 SSE event stream at
// GET /v1/sessions/{id}/events and the §4.9 LLM-proxy streaming
// translators rely on http.Flusher to push partial bytes to the client;
// without this forwarder the middleware hides the Flusher and the SSE
// handler returns 500 ("response writer does not support streaming"). The
// request-metrics accounting is unaffected because Write already passes
// through.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		if !s.wroteHeader {
			s.wroteHeader = true
		}
		f.Flush()
	}
}

// Hijack forwards to the underlying http.Hijacker so a WebSocket upgrade
// survives the request-metrics wrapper. The MCP WebSocket transport at
// /mcp/v1/ws (§15.2, §27.5) upgrades through this middleware;
// nhooyr.io/websocket performs a direct http.Hijacker type assertion on the
// handed ResponseWriter, so without this forwarder the wrapper hides the
// Hijacker and the upgrade fails.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := s.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("requestmetrics: underlying ResponseWriter does not support hijacking")
}

// statusClass collapses an HTTP status to its §16.1.1 low-cardinality class
// label (2xx, 3xx, 4xx, 5xx) so the metric does not explode into one series
// per status code.
func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return strconv.Itoa(code/100) + "xx"
	}
}
