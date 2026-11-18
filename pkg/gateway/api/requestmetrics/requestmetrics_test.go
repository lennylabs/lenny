// SPDX-License-Identifier: MIT

package requestmetrics_test

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/api/requestmetrics"
)

// fakeRecorder records what the middleware reports.
type fakeRecorder struct {
	mu       sync.Mutex
	observed []string
	inflight int64
	peak     int64
}

func (f *fakeRecorder) ObserveRequest(method, route, statusClass string, _ float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observed = append(f.observed, method+" "+route+" "+statusClass)
}

func (f *fakeRecorder) AddInflightRequests(delta int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inflight += delta
	if f.inflight > f.peak {
		f.peak = f.inflight
	}
}

// spec: §16.1 (Gateway metrics)
func TestWrapRecordsEachRequestByRouteAndStatusClass(t *testing.T) {
	rec := &fakeRecorder{}
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusOK, "2xx"},
		{http.StatusFound, "3xx"},
		{http.StatusNotFound, "4xx"},
		{http.StatusServiceUnavailable, "5xx"},
		{http.StatusContinue, "1xx"},
	}
	for _, c := range cases {
		h := requestmetrics.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
		}), rec, func(*http.Request) string { return "/v1/sessions/{id}" })
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/sessions/abc", nil))
	}
	want := []string{
		"GET /v1/sessions/{id} 2xx",
		"GET /v1/sessions/{id} 3xx",
		"GET /v1/sessions/{id} 4xx",
		"GET /v1/sessions/{id} 5xx",
		"GET /v1/sessions/{id} 1xx",
	}
	if strings.Join(rec.observed, "|") != strings.Join(want, "|") {
		t.Fatalf("observed %q, want %q", rec.observed, want)
	}
}

// spec: §16.1 (Gateway metrics)
func TestWrapCountsAWriteWithoutAHeaderAsSuccess(t *testing.T) {
	rec := &fakeRecorder{}
	h := requestmetrics.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
		w.WriteHeader(http.StatusInternalServerError) // ignored after the body started
	}), rec, func(*http.Request) string { return "/healthz" })
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if len(rec.observed) != 1 || rec.observed[0] != "GET /healthz 2xx" {
		t.Fatalf("observed %q, want one 2xx request", rec.observed)
	}
}

// spec: §16.1 (Gateway metrics)
func TestWrapHoldsTheInflightCountWhileTheRequestRuns(t *testing.T) {
	rec := &fakeRecorder{}
	started, release := make(chan struct{}), make(chan struct{})
	h := requestmetrics.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	}), rec, func(*http.Request) string { return "/test" })
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/test", nil))
	}()
	<-started
	rec.mu.Lock()
	during := rec.inflight
	rec.mu.Unlock()
	close(release)
	<-done
	if during != 1 {
		t.Fatalf("in-flight count while the request ran = %d, want 1", during)
	}
	if rec.inflight != 0 {
		t.Fatalf("in-flight count after the request = %d, want 0", rec.inflight)
	}
}

// spec: §15.1 GET /v1/sessions/{id}/events (SSE event stream)
func TestWrapForwardsFlusher(t *testing.T) {
	flushed := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("wrapper did not implement http.Flusher; SSE handlers will 500")
		}
		w.WriteHeader(http.StatusOK)
		f.Flush()
		flushed = true
	})
	h := requestmetrics.Wrap(inner, &fakeRecorder{}, func(*http.Request) string { return "/v1/sessions/{id}/events" })
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/sessions/x/events", nil))
	if !flushed {
		t.Fatal("inner handler did not reach Flush")
	}
	if !rr.Flushed {
		t.Error("recorder reports the response was not flushed")
	}
}

// spec: §27.5 (Chat), §27.3.1 (Authentication)
func TestWrapIsHijackable(t *testing.T) {
	h := requestmetrics.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "not hijackable", http.StatusInternalServerError)
			return
		}
		conn, brw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: x\r\nConnection: Upgrade\r\n\r\n")
		_ = brw.Flush()
	}), &fakeRecorder{}, func(*http.Request) string { return "/mcp/v1/ws" })

	srv := httptest.NewServer(h)
	defer srv.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: x\r\n\r\n"))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(line, "101") {
		t.Errorf("hijack response = %q, want 101 (Hijack did not pass through)", line)
	}
}

func TestWrapRefusesHijackWhenTheWriterCannot(t *testing.T) {
	var hijackErr error
	h := requestmetrics.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _, hijackErr = w.(http.Hijacker).Hijack()
	}), &fakeRecorder{}, func(*http.Request) string { return "/mcp/v1/ws" })
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/mcp/v1/ws", nil))
	if hijackErr == nil {
		t.Fatal("Hijack on a writer that cannot hijack returned no error")
	}
}
