// SPDX-License-Identifier: MIT

package egress

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// These tests exercise contributor tooling and carry no spec annotation.

func TestNewRefusesAnEmptyOrMalformedAllowList(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Error("an empty allow list was accepted")
	}
	if _, err := New([]string{"api.anthropic.com"}); err == nil {
		t.Error("an address without a port was accepted")
	}
	p, err := New([]string{"API.Anthropic.com:443", "lenny-journeys-control-plane:6443"})
	if err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]bool{
		"api.anthropic.com:443":             true,
		"lenny-journeys-control-plane:6443": true,
		"api.anthropic.com:80":              false,
		"lenny-e2e-control-plane:6443":      false,
		"host.docker.internal:443":          false,
		"example.com:443":                   false,
		"garbage":                           false,
	} {
		if got := p.Allowed(target); got != want {
			t.Errorf("Allowed(%q) = %v, want %v", target, got, want)
		}
	}
}

// connect sends a CONNECT for target through the proxy at addr and
// returns the status line and the connection.
func connect(t *testing.T, addr, target string) (string, net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(line), c, br
}

func TestProxyTunnelsOnlyAllowedTargets(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	var log bytes.Buffer
	p, err := New([]string{echo.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	p.Log = &log
	srv := httptest.NewServer(p)
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	status, c, br := connect(t, addr, echo.Addr().String())
	if !strings.Contains(status, "200") {
		t.Fatalf("an allowed CONNECT answered %q", status)
	}
	if _, err := br.ReadString('\n'); err != nil { // the blank line after the status
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	if got, err := br.ReadString('\n'); err != nil || got != "ping\n" {
		t.Fatalf("the tunnel echoed %q, %v", got, err)
	}
	_ = c.Close()

	status, c2, _ := connect(t, addr, "example.com:443")
	_ = c2.Close()
	if !strings.Contains(status, "403") {
		t.Errorf("a CONNECT to example.com answered %q, want 403", status)
	}

	proxyURL, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get("http://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a plain HTTP request answered %d, want 403", resp.StatusCode)
	}
	for _, want := range []string{"allowed CONNECT", "refused CONNECT example.com:443", "only CONNECT is proxied"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("the log lacks %q:\n%s", want, log.String())
		}
	}
}

func TestProxyReportsAnUnreachableUpstream(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	target := l.Addr().String()
	_ = l.Close() // nothing listens there now
	p, err := New([]string{target})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(p)
	defer srv.Close()
	status, c, _ := connect(t, strings.TrimPrefix(srv.URL, "http://"), target)
	_ = c.Close()
	if !strings.Contains(status, "502") {
		t.Errorf("an unreachable upstream answered %q, want 502", status)
	}
}
