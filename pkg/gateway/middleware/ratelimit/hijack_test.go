// SPDX-License-Identifier: MIT

package ratelimit_test

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ratelimitmw "github.com/lennylabs/lenny/pkg/gateway/middleware/ratelimit"
	rlcounter "github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
)

// spec: §27.5 / §27.3.1 line 142 — the rate-limit wrapper (rateLimitRW)
// is the topmost writer the MCP WebSocket handler receives in production.
// nhooyr.io/websocket performs a direct http.Hijacker assertion, so the
// wrapper must re-expose Hijack or the /mcp/v1/ws upgrade fails. A
// non-nil Counter forces the writer wrapper onto the path.
func TestRateLimitRWIsHijackable(t *testing.T) {
	h := ratelimitmw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	}), ratelimitmw.Options{Counter: rlcounter.NewMemory(), GlobalPerMinute: 1000})

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
