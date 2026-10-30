// SPDX-License-Identifier: MIT

package tcpfault

import (
	"bufio"
	"net"
	"testing"
	"time"
)

// echoServer accepts connections and echoes each line back.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if _, err := c.Write([]byte(line)); err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// roundTrip dials addr, sends one line, and returns the echo.
func roundTrip(addr string) (string, error) {
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte("ping\n")); err != nil {
		return "", err
	}
	return bufio.NewReader(c).ReadString('\n')
}

// spec: 25.15 (Failure Mode Analysis)
// diagnosis: the proxy does not model a store outage and its end: a Cut
// still lets a dial or an open connection through, or a Restore does not
// serve the same address again, so a process could not reconnect to the
// store with its data after the injected outage.
func TestProxyCutRefusesAndRestoreServesAgain(t *testing.T) {
	p := Start(t, echoServer(t))
	if got, err := roundTrip(p.Addr()); err != nil || got != "ping\n" {
		t.Fatalf("before Cut: %q %v", got, err)
	}
	open, err := net.Dial("tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	time.Sleep(100 * time.Millisecond)

	p.Cut()
	if _, err := roundTrip(p.Addr()); err == nil {
		t.Fatal("a dial succeeded after Cut")
	}
	_ = open.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := open.Read(make([]byte, 1)); err == nil {
		t.Fatal("a connection open before Cut stayed usable")
	}

	p.Restore(t)
	if got, err := roundTrip(p.Addr()); err != nil || got != "ping\n" {
		t.Fatalf("after Restore: %q %v", got, err)
	}
}

// spec: 25.15 (Failure Mode Analysis)
// diagnosis: the DSN rewrite loses the credentials, database, or query of
// the store URL, so the process given the proxied DSN cannot log in.
func TestRewriteURLKeepsEverythingButTheHost(t *testing.T) {
	got, err := RewriteURL("postgres://u:p@localhost:5432/lenny_test?sslmode=disable", "127.0.0.1:9999")
	if err != nil {
		t.Fatal(err)
	}
	if want := "postgres://u:p@127.0.0.1:9999/lenny_test?sslmode=disable"; got != want {
		t.Errorf("RewriteURL = %s, want %s", got, want)
	}
	if hp, err := HostPort("redis://localhost:6379/0"); err != nil || hp != "localhost:6379" {
		t.Errorf("HostPort = %q %v", hp, err)
	}
	if _, err := RewriteURL("not a url", "x:1"); err == nil {
		t.Error("RewriteURL accepted a URL with no host")
	}
}
