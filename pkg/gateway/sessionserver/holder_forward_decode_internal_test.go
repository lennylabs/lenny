// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// failingReader returns its data, then err.
type failingReader struct {
	data string
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.data != "" {
		n := copy(p, r.data)
		r.data = r.data[n:]
		return n, nil
	}
	return 0, r.err
}

// failingWriter is a ResponseWriter whose Write fails.
type failingWriter struct{ httptest.ResponseRecorder }

func (*failingWriter) Write([]byte) (int, error) { return 0, errors.New("client gone") }

// spec: §29.3 (Interactive message send) Forwarding rules 3 to 5 and 7
//
// TestDecodeToolForwardResponseRejectsUndecodableResponses_spec_29_3 asserts
// that a forwarded tool call's response decodes only when it is a 2xx
// JSON-RPC tool result or a non-2xx error envelope naming a code. A 2xx body
// that is not a tool result, a non-2xx body without a code, and a body whose
// read fails mid-stream all report ok false, which the caller answers as the
// outcome-unknown refusal rather than a fabricated result.
func TestDecodeToolForwardResponseRejectsUndecodableResponses_spec_29_3(t *testing.T) {
	resp := func(status int, body io.Reader) *http.Response {
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(body)}
	}
	for _, tc := range []struct {
		name   string
		resp   *http.Response
		ok     bool
		toolEr string
	}{
		{"a 2xx body that is not a tool result", resp(200, strings.NewReader(`not json`)), false, ""},
		{"a non-2xx body that is not an envelope", resp(502, strings.NewReader(`<html>`)), false, ""},
		{"a non-2xx envelope without a code", resp(409, strings.NewReader(`{"error":{"message":"x"}}`)), false, ""},
		{"a body whose read fails", resp(200, &failingReader{data: `{"jsonrpc"`, err: errors.New("reset")}), false, ""},
		{"a non-2xx envelope naming a code", resp(409, strings.NewReader(`{"error":{"code":"TARGET_NOT_READY","message":"m"}}`)), true, "TARGET_NOT_READY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, te, ok := decodeToolForwardResponse(context.Background(), tc.resp)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if tc.toolEr != "" && (te == nil || te.Code != tc.toolEr) {
				t.Errorf("tool error = %+v, want code %s", te, tc.toolEr)
			}
		})
	}
}

// spec: §29.3 (Interactive message send) Forwarding rule 7
//
// TestStreamBodyReportsWriteAndReadFailures_spec_29_3 asserts that relaying
// the coordinator's streamed response stops with the error when the caller's
// connection refuses a write or the coordinator's body fails mid-stream, and
// returns nil at the end of a complete body.
func TestStreamBodyReportsWriteAndReadFailures_spec_29_3(t *testing.T) {
	if err := streamBody(&failingWriter{}, strings.NewReader("data")); err == nil {
		t.Error("streamBody to a failing writer = nil, want the write error")
	}
	readErr := errors.New("coordinator reset")
	rec := httptest.NewRecorder()
	if err := streamBody(rec, &failingReader{data: "part", err: readErr}); !errors.Is(err, readErr) {
		t.Errorf("streamBody over a failing body = %v, want %v", err, readErr)
	}
	if rec.Body.String() != "part" {
		t.Errorf("relayed %q before the failure, want %q", rec.Body.String(), "part")
	}
	rec = httptest.NewRecorder()
	if err := streamBody(rec, strings.NewReader("whole")); err != nil || rec.Body.String() != "whole" {
		t.Errorf("streamBody = %v relaying %q, want nil relaying %q", err, rec.Body.String(), "whole")
	}
}

// spec: §29.3 (Interactive message send) Forwarding rule 6
//
// TestForwardRequestBuildFailuresAreReported_spec_29_3 asserts that a forward
// whose request cannot be built returns an error before any dial: a target
// address that does not form a URL, and tool-call arguments that are not
// JSON.
func TestForwardRequestBuildFailuresAreReported_spec_29_3(t *testing.T) {
	ctx := context.Background()
	if _, err := newForwardRequest(ctx, http.MethodPost, "https://bad host\x7f/", nil, 0, http.Header{}); err == nil {
		t.Error("newForwardRequest with an invalid URL = nil error")
	}
	if _, err := newToolForwardRequest(ctx, "127.0.0.1:1", "lenny/send_message", json.RawMessage(`{`)); err == nil {
		t.Error("newToolForwardRequest with invalid arguments = nil error")
	}
	req, err := newToolForwardRequest(ctx, "127.0.0.1:1", "lenny/send_message", nil)
	if err != nil {
		t.Fatalf("newToolForwardRequest with no arguments: %v", err)
	}
	body, _ := io.ReadAll(req.Body)
	if !strings.Contains(string(body), `"arguments":{}`) {
		t.Errorf("forwarded body = %s, want empty arguments as {}", body)
	}
}

// spec: §29.3 (Interactive message send) Forwarding, §10.1.1 (Stateless
// Replicas and Per-Session Coordination) Replica address
//
// TestHolderForwardClientDoesNotFollowRedirects_spec_29_3 asserts that the
// forward client returns a coordinator's redirect to the caller rather than
// following it, so a forward never reaches an address other than the
// session's coordinating replica.
func TestHolderForwardClientDoesNotFollowRedirects_spec_29_3(t *testing.T) {
	var followed bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			followed = true
		}
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	cfg := &tls.Config{RootCAs: srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs, ServerName: "example.com"}
	var d net.Dialer
	client := newHolderForwardClient(cfg, 5*time.Second, d.DialContext)
	resp, err := client.Get(srv.URL + "/v1/sessions/s/messages")
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect || followed {
		t.Errorf("status = %d followed = %v, want the 307 returned unfollowed", resp.StatusCode, followed)
	}
}
