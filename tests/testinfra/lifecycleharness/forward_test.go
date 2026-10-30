// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/mtls/certreload"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
)

// forwardProbe sends one POST to the listener and reports the status, or -1
// on a transport error, and whether the client obtained a connection.
func forwardProbe(t *testing.T, client *http.Client, addr string) (int, bool) {
	t.Helper()
	var connected atomic.Bool
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected.Store(true) }}
	req, err := http.NewRequest(http.MethodPost, "https://"+addr+"/v1/sessions/s/messages", strings.NewReader(`{"m":1}`))
	if err != nil {
		t.Fatal(err)
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := client.Do(req)
	if err != nil {
		return -1, connected.Load()
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, connected.Load()
}

// TestHarnessForwardListenerFaults_spec_29_3 pins the harness forward
// lane's faults against the forward client: the listener serves a gateway
// client over mTLS; the refusal fails a forward before any connection is
// obtained, which the forward classifies as not delivered; the fault reads
// the request and closes the connection with no response after the
// connection was obtained, which the forward classifies as outcome unknown;
// disarming each restores service; and a closed listener refuses the dial.
//
// spec: 29.3 (Interactive message send) Forwarding rules 6 and 7, 10.3 (mTLS
// PKI) NET-072
func TestHarnessForwardListenerFaults_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	reloader, err := certreload.New(m.CertFile, m.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	f := newForwardListener(t)
	var seen atomic.Int64
	f.serve(sessionserver.HolderForwardServerTLS(reloader, m.CAPool), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) == `{"m":1}` {
			seen.Add(1)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(f.close)
	client := sessionserver.NewHolderForwardClient(reloader, m.CAPool)

	if code, _ := forwardProbe(t, client, f.Addr()); code != http.StatusAccepted || seen.Load() != 1 {
		t.Fatalf("healthy forward = %d (handler saw %d), want 202 and the body", code, seen.Load())
	}

	var faulted atomic.Int64
	f.setFault(func(r *http.Request) bool {
		body, _ := io.ReadAll(r.Body)
		faulted.Add(int64(len(body)))
		return true
	})
	if code, connected := forwardProbe(t, client, f.Addr()); code != -1 || !connected {
		t.Errorf("faulted forward = %d (connected %v), want a transport error after the connection", code, connected)
	}
	if faulted.Load() == 0 || seen.Load() != 1 {
		t.Errorf("fault read %d body bytes and the handler ran %d times, want the body read and no handler", faulted.Load(), seen.Load())
	}
	f.setFault(nil)

	f.setRefuse(true)
	client.CloseIdleConnections()
	if code, connected := forwardProbe(t, client, f.Addr()); code != -1 || connected {
		t.Errorf("refused forward = %d (connected %v), want a transport error before any connection", code, connected)
	}
	f.setRefuse(false)
	if code, _ := forwardProbe(t, client, f.Addr()); code != http.StatusAccepted {
		t.Errorf("forward after disarming = %d, want 202", code)
	}

	f.close()
	client.CloseIdleConnections()
	if code, connected := forwardProbe(t, client, f.Addr()); code != -1 || connected {
		t.Errorf("forward to a closed listener = %d (connected %v), want a dial failure", code, connected)
	}
}
