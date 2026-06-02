// SPDX-License-Identifier: MIT

package recover_test

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	recovermw "github.com/lennylabs/lenny/pkg/gateway/middleware/recover"
)

// spec: §27.5 / §27.3.1 line 142 — the recovery wrapper sits on the MCP
// WebSocket upgrade path at /mcp/v1/ws. nhooyr.io/websocket performs a
// direct http.Hijacker assertion, so the wrapper must re-expose Hijack or
// the upgrade fails. This test confirms a hijack survives the wrapper.
func TestRecoverWriterIsHijackable(t *testing.T) {
	assertHijackable(t, recovermw.Middleware(hijackEcho()))
}

// hijackEcho returns a handler that hijacks the connection and writes a
// sentinel response so a raw TCP client can confirm the hijack worked.
func hijackEcho() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	})
}

func assertHijackable(t *testing.T, h http.Handler) {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: x\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(line, "101") {
		t.Errorf("hijack response = %q, want a 101 switch (Hijack did not pass through)", line)
	}
}
