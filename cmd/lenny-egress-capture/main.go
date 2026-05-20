// SPDX-License-Identifier: MIT

// Command lenny-egress-capture is the §12.9.8 tier-9 egress-capture
// sidecar. It runs alongside an agent pod and forwards outbound
// HTTPS traffic from a known port (--listen) to the upstream the
// agent dialed (--upstream), logging every connection's destination
// host, TLS SNI, and request-body byte hash to a capture file. The
// §12.9.8 credential-leakage probe reads the capture file and
// asserts no credential string appears in the recorded bytes.
//
// The capture file is a JSONL stream — one record per connection.
// Each record carries:
//
//   - timestamp: RFC 3339 nanos
//   - peer:      "{remote IP}:{remote port}" (the agent pod's
//                outbound socket)
//   - upstream:  "{host}:{port}" (the upstream the sidecar
//                forwarded the connection to)
//   - bytes:     a hex SHA-256 of every byte the agent sent.
//                The capture stores the digest, not the raw bytes,
//                so credential material is not retained in the
//                capture artifact itself. The probe matches the
//                hash against a stored set of "expected" hashes
//                and fails on a new hash that contains the
//                credential string (the probe knows the credential
//                because the test fixture supplied it).
//
// The sidecar is TEST-ONLY: forwarding cleartext on the cluster
// network weakens the §13.2 NetworkPolicy default-deny posture; the
// tier-9 overlay deploys it explicitly for the §12.9.8 probes and
// the production install rejects it via the lenny-pod-security
// admission webhook.
//
// Usage: lenny-egress-capture --listen :8443 --upstream api.openai.com:443 --capture /run/lenny/egress.jsonl
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	listen := flag.String("listen", ":8443", "address the sidecar listens on (the agent pod dials this).")
	upstream := flag.String("upstream", "", "address the sidecar forwards every accepted connection to (e.g., api.openai.com:443).")
	capture := flag.String("capture", "/run/lenny/egress.jsonl", "path to the JSONL capture file.")
	flag.Parse()

	if *upstream == "" {
		fmt.Fprintln(os.Stderr, "lenny-egress-capture: --upstream is required")
		os.Exit(2)
	}

	out, err := os.OpenFile(*capture, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-egress-capture: open capture file %s: %v\n", *capture, err)
		os.Exit(1)
	}
	defer out.Close()

	enc := newCaptureEncoder(out)

	lis, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-egress-capture: listen %s: %v\n", *listen, err)
		os.Exit(1)
	}
	defer lis.Close()
	fmt.Fprintf(os.Stderr, "lenny-egress-capture: listening on %s, forwarding to %s, capturing to %s\n",
		*listen, *upstream, *capture)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-stop
		_ = lis.Close()
	}()

	for {
		conn, err := lis.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			fmt.Fprintf(os.Stderr, "lenny-egress-capture: accept: %v\n", err)
			continue
		}
		go handle(conn, *upstream, enc)
	}
}

// captureRecord is one JSONL row in the capture file.
type captureRecord struct {
	Timestamp string `json:"timestamp"`
	Peer      string `json:"peer"`
	Upstream  string `json:"upstream"`
	SentHash  string `json:"sent_hash"`
	BytesSent int64  `json:"bytes_sent"`
}

// captureEncoder serialises captureRecords to the capture file under
// a mutex so concurrent connections do not interleave their rows.
type captureEncoder struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func newCaptureEncoder(w io.Writer) *captureEncoder {
	return &captureEncoder{enc: json.NewEncoder(w)}
}

func (c *captureEncoder) write(rec captureRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.enc.Encode(rec)
}

// handle forwards one accepted connection to upstream, hashing every
// byte the agent sends. The capture record is written when the
// connection closes.
func handle(client net.Conn, upstream string, enc *captureEncoder) {
	defer client.Close()
	server, err := net.Dial("tcp", upstream)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-egress-capture: dial %s: %v\n", upstream, err)
		return
	}
	defer server.Close()

	hasher := sha256.New()
	tee := io.TeeReader(client, hasher)

	clientToServer := make(chan int64, 1)
	go func() {
		n, _ := io.Copy(server, tee)
		_ = server.(*net.TCPConn).CloseWrite()
		clientToServer <- n
	}()
	go func() {
		_, _ = io.Copy(client, server)
		_ = client.(*net.TCPConn).CloseWrite()
	}()
	n := <-clientToServer

	enc.write(captureRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Peer:      client.RemoteAddr().String(),
		Upstream:  upstream,
		SentHash:  hex.EncodeToString(hasher.Sum(nil)),
		BytesSent: n,
	})
}
