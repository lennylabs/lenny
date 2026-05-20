// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// upstreamServer accepts one connection, reads everything the
// sidecar forwards, and writes a fixed reply. It exits when the
// connection closes.
func upstreamServer(t *testing.T) (string, chan []byte) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	got := make(chan []byte, 1)
	go func() {
		defer lis.Close()
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, conn)
		got <- buf.Bytes()
		// Reply with a fixed payload so the client side sees a clean
		// half-close.
		_, _ = conn.Write([]byte("OK"))
	}()
	return lis.Addr().String(), got
}

// startSidecar runs handle for one accepted connection and exits.
func startSidecar(t *testing.T, upstream string, enc *captureEncoder) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sidecar listen: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		handle(conn, upstream, enc)
	}()
	return lis.Addr().String()
}

// spec: §12.9.8 (the sidecar forwards bytes to the upstream and
// records the SHA-256 hash of the agent-sent payload)
func TestSidecarForwardsAndHashes(t *testing.T) {
	upstreamAddr, upstreamRx := upstreamServer(t)
	var capBuf bytes.Buffer
	var capMu sync.Mutex
	// syncWriter mediates the capBuf so the sidecar's writer goroutine
	// and the test's Len/Bytes reads do not race under -race.
	enc := newCaptureEncoder(&syncWriter{w: &capBuf, mu: &capMu})

	sidecarAddr := startSidecar(t, upstreamAddr, enc)

	client, err := net.Dial("tcp", sidecarAddr)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	payload := []byte("POST /v1/chat/completions HTTP/1.1\r\nAuthorization: Bearer sk-leak\r\n\r\n")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("client write: %v", err)
	}
	// Half-close so the sidecar's CopyFrom returns and the capture
	// record is written.
	_ = client.(*net.TCPConn).CloseWrite()
	resp, _ := io.ReadAll(client)
	_ = client.Close()

	if string(resp) != "OK" {
		t.Errorf("upstream reply = %q, want OK", resp)
	}

	select {
	case got := <-upstreamRx:
		if !bytes.Equal(got, payload) {
			t.Errorf("upstream received %q, want %q", got, payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive the forwarded payload within 2s")
	}

	// Drain the capture file and verify the recorded hash matches the
	// hash of the payload the client sent. The mutex pair-wise serialises
	// each Len() poll with the sidecar's write goroutine.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		capMu.Lock()
		ready := capBuf.Len() > 0
		capMu.Unlock()
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	capMu.Lock()
	capBytes := append([]byte(nil), capBuf.Bytes()...)
	capMu.Unlock()
	if !strings.Contains(string(capBytes), "sent_hash") {
		t.Errorf("capture JSONL missing sent_hash field:\n%s", string(capBytes))
	}
	scanner := bufio.NewScanner(bytes.NewReader(capBytes))
	if !scanner.Scan() {
		t.Fatalf("capture file empty: %v", scanner.Err())
	}
	var rec captureRecord
	if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
		t.Fatalf("decode capture record: %v\n%s", err, scanner.Text())
	}
	if rec.Upstream != upstreamAddr {
		t.Errorf("Upstream = %q, want %q", rec.Upstream, upstreamAddr)
	}
	if rec.BytesSent != int64(len(payload)) {
		t.Errorf("BytesSent = %d, want %d", rec.BytesSent, len(payload))
	}
	want := sha256.Sum256(payload)
	if rec.SentHash != hex.EncodeToString(want[:]) {
		t.Errorf("SentHash = %q, want %q", rec.SentHash, hex.EncodeToString(want[:]))
	}
}

// spec: §12.9.8 (concurrent connections each produce one record)
func TestSidecarConcurrentConnections(t *testing.T) {
	upstreamLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	defer upstreamLis.Close()
	// Echo back so each client sees a half-close.
	go func() {
		for {
			c, err := upstreamLis.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(io.Discard, c)
			}(c)
		}
	}()

	var capBuf bytes.Buffer
	var capMu sync.Mutex
	enc := newCaptureEncoder(&syncWriter{w: &capBuf, mu: &capMu})

	sidecarLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sidecar listen: %v", err)
	}
	defer sidecarLis.Close()
	go func() {
		for {
			c, err := sidecarLis.Accept()
			if err != nil {
				return
			}
			go handle(c, upstreamLis.Addr().String(), enc)
		}
	}()

	const conns = 5
	var wg sync.WaitGroup
	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := net.Dial("tcp", sidecarLis.Addr().String())
			if err != nil {
				t.Errorf("dial %d: %v", i, err)
				return
			}
			defer c.Close()
			_, _ = c.Write([]byte("hello"))
			_ = c.(*net.TCPConn).CloseWrite()
		}(i)
	}
	wg.Wait()

	// Wait briefly for all capture rows to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		capMu.Lock()
		got := bytes.Count(capBuf.Bytes(), []byte("\n"))
		capMu.Unlock()
		if got >= conns {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	capMu.Lock()
	defer capMu.Unlock()
	got := bytes.Count(capBuf.Bytes(), []byte("\n"))
	if got != conns {
		t.Errorf("capture rows = %d, want %d", got, conns)
	}
}

// syncWriter is a thin mutex wrapper so the test asserts byte counts
// without racing on the test buffer.
type syncWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
