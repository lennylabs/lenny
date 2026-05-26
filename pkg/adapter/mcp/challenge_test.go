// SPDX-License-Identifier: MIT

package mcp_test

import (
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// challengeServer is mcp.NewServer with the §4.7 nonce-only-mode
// challenge-response supplement enabled.
func challengeServer() *mcp.Server {
	s := mcp.NewServer()
	s.RequireChallenge = true
	return s
}

// readChallenge reads the adapter's challenge frame and returns the
// adapterChallenge value.
func readChallenge(t *testing.T, dec *json.Decoder) string {
	t.Helper()
	var frame map[string]string
	if err := dec.Decode(&frame); err != nil {
		t.Fatalf("read adapterChallenge frame: %v", err)
	}
	c, ok := frame[mcp.ChallengeParamKey]
	if !ok || c == "" {
		t.Fatalf("challenge frame = %v, want a %s value", frame, mcp.ChallengeParamKey)
	}
	return c
}

// spec: §4.7 lines 879-883 — a connection that answers the
// adapterChallenge with the correct HMAC-SHA256(key=nonce) proceeds to
// the initialize response and can dispatch tools.
func TestServerChallengeSuccess_spec_4_7(t *testing.T) {
	s := challengeServer()
	s.Register(mcp.Tool{
		Name: "lenny/output", Handler: func(json.RawMessage) (any, error) { return nil, nil },
	})
	enc, dec := serverPipe(t, s, testNonce)

	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	challenge := readChallenge(t, dec)
	if err := enc.Encode(map[string]string{
		mcp.ChallengeResponseParamKey: mcp.ExpectedChallengeResponse(testNonce, challenge),
	}); err != nil {
		t.Fatalf("send challenge response: %v", err)
	}

	resp := readResponse(t, dec)
	if _, isErr := resp["error"]; isErr {
		t.Fatalf("initialize after challenge returned an error: %s", resp["error"])
	}
	// The post-handshake tool path is live.
	sendRequest(t, enc, 2, "tools/list", nil)
	if resp := readResponse(t, dec); resp["result"] == nil {
		t.Errorf("tools/list after challenge returned no result: %v", resp)
	}
}

// spec: §4.7 line 883 — a mismatched HMAC closes the socket with no
// protocol response.
func TestServerChallengeWrongHMAC_spec_4_7(t *testing.T) {
	enc, dec := serverPipe(t, challengeServer(), testNonce)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	_ = readChallenge(t, dec)
	if err := enc.Encode(map[string]string{
		mcp.ChallengeResponseParamKey: "deadbeef",
	}); err != nil {
		t.Fatalf("send bad challenge response: %v", err)
	}
	var resp map[string]json.RawMessage
	if err := dec.Decode(&resp); err == nil {
		t.Errorf("server responded after a bad HMAC: %v; want an immediate close", resp)
	}
}

// spec: §4.7 line 883 — a challenge response missing the HMAC field is
// rejected and the socket is closed.
func TestServerChallengeMissingField_spec_4_7(t *testing.T) {
	enc, dec := serverPipe(t, challengeServer(), testNonce)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	_ = readChallenge(t, dec)
	if err := enc.Encode(map[string]string{"unrelated": "value"}); err != nil {
		t.Fatalf("send field-less challenge response: %v", err)
	}
	var resp map[string]json.RawMessage
	if err := dec.Decode(&resp); err == nil {
		t.Errorf("server responded to a field-less challenge response: %v; want a close", resp)
	}
}

// spec: §4.7 line 882 — a response that does not arrive within 500 ms
// times out and the socket is closed.
func TestServerChallengeTimeout_spec_4_7(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- challengeServer().ServeConn(server, testNonce) }()
	t.Cleanup(func() { _ = client.Close() })

	enc, dec := json.NewEncoder(client), json.NewDecoder(client)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	_ = readChallenge(t, dec)
	// Never answer the challenge. The 500 ms deadline must end the conn.
	select {
	case err := <-done:
		if err == nil {
			t.Error("ServeConn returned nil after a challenge timeout, want a read-deadline error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ServeConn did not return after the 500 ms challenge deadline elapsed")
	}
}

// spec: §4.7 lines 879-883 — the supplement is opt-in: a server with
// RequireChallenge unset sends the initialize response directly with no
// intervening challenge frame.
func TestServerNoChallengeWhenDisabled_spec_4_7(t *testing.T) {
	enc, dec := serverPipe(t, mcp.NewServer(), testNonce)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	resp := readResponse(t, dec)
	// The first frame back is the initialize result, not a challenge.
	if _, ok := resp["result"]; !ok {
		t.Errorf("first frame = %v, want the initialize result (no challenge when disabled)", resp)
	}
}

func TestExpectedChallengeResponseDeterministic_spec_4_7(t *testing.T) {
	const challenge = "00112233445566778899aabbccddeeff"
	a := mcp.ExpectedChallengeResponse(testNonce, challenge)
	b := mcp.ExpectedChallengeResponse(testNonce, challenge)
	if a != b {
		t.Errorf("HMAC not deterministic: %q vs %q", a, b)
	}
	if mcp.ExpectedChallengeResponse("a-different-nonce", challenge) == a {
		t.Error("HMAC did not change with the key (nonce)")
	}
	if len(a) != 64 {
		t.Errorf("HMAC-SHA256 hex length = %d, want 64", len(a))
	}
}

func TestValidateChallengeResponse_spec_4_7(t *testing.T) {
	const challenge = "ffeeddccbbaa99887766554433221100"
	good := mcp.ExpectedChallengeResponse(testNonce, challenge)

	frame, _ := json.Marshal(map[string]string{mcp.ChallengeResponseParamKey: good})
	if err := mcp.ValidateChallengeResponse(frame, testNonce, challenge); err != nil {
		t.Errorf("valid response rejected: %v", err)
	}

	missing, _ := json.Marshal(map[string]string{"other": good})
	if err := mcp.ValidateChallengeResponse(missing, testNonce, challenge); !errors.Is(err, mcp.ErrChallengeResponseMissing) {
		t.Errorf("missing-field error = %v, want ErrChallengeResponseMissing", err)
	}

	wrong, _ := json.Marshal(map[string]string{mcp.ChallengeResponseParamKey: "00"})
	if err := mcp.ValidateChallengeResponse(wrong, testNonce, challenge); !errors.Is(err, mcp.ErrChallengeResponseInvalid) {
		t.Errorf("wrong-HMAC error = %v, want ErrChallengeResponseInvalid", err)
	}
}
