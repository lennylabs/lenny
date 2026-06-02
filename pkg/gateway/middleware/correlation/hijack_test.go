// SPDX-License-Identifier: MIT

package correlation

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §27.5 / §27.3.1 line 142 — the correlation wrapper is the
// outermost writer-wrapping middleware on the MCP WebSocket upgrade path.
// nhooyr.io/websocket performs a direct http.Hijacker assertion, so the
// captureRW wrapper must re-expose Hijack or the upgrade fails.
func TestCaptureRWIsHijackable(t *testing.T) {
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	}), Options{})

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
