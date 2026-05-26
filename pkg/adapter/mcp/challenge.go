// SPDX-License-Identifier: MIT

package mcp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ChallengeBytes is the §4.7 nonce-only-mode adapterChallenge length: a
// 128-bit random value (spec/04_system-components.md line 881).
const ChallengeBytes = 16

// ChallengeTimeout bounds the §4.7 line 882 challenge-response: the agent
// must answer with its HMAC within 500 ms or the adapter closes the
// socket.
const ChallengeTimeout = 500 * time.Millisecond

// ChallengeParamKey and ChallengeResponseParamKey are the JSON object
// keys for the adapter→agent challenge frame and the agent→adapter HMAC
// response frame on an intra-pod MCP connection. They sit alongside the
// §15.4.3 _lennyNonce convention so a runtime SDK recognizes the
// supplement by its key.
const (
	ChallengeParamKey         = "_lennyChallenge"
	ChallengeResponseParamKey = "_lennyChallengeResponse"
)

// Sentinel errors for the §4.7 nonce-only-mode challenge-response. A
// connection that produces any of these is closed without a protocol
// response (spec line 883).
var (
	// ErrChallengeResponseMissing reports a challenge-response frame that
	// carries no _lennyChallengeResponse field.
	ErrChallengeResponseMissing = errors.New("mcp: challenge response carries no _lennyChallengeResponse")

	// ErrChallengeResponseInvalid reports a challenge-response frame whose
	// HMAC does not match HMAC-SHA256(key=manifestNonce, data=adapterChallenge).
	ErrChallengeResponseInvalid = errors.New("mcp: challenge response HMAC does not match")
)

// newChallenge returns a fresh §4.7 adapterChallenge: a 128-bit random
// value, lowercase hex-encoded. A new challenge is generated per
// connection so an observed nonce cannot be replayed (spec lines 879-883).
func newChallenge() (string, error) {
	b := make([]byte, ChallengeBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("mcp: generate adapterChallenge: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ExpectedChallengeResponse computes the §4.7 line 882
// HMAC-SHA256(key=manifestNonce, data=adapterChallenge), lowercase
// hex-encoded. It is the value the agent must return and is exported so a
// runtime SDK and its tests can compute the same response.
func ExpectedChallengeResponse(nonce, challenge string) string {
	mac := hmac.New(sha256.New, []byte(nonce))
	mac.Write([]byte(challenge))
	return hex.EncodeToString(mac.Sum(nil))
}

// ValidateChallengeResponse checks an agent's §4.7 challenge-response
// frame against HMAC-SHA256(key=nonce, data=challenge). The comparison is
// constant-time. A frame that carries no response field or a mismatched
// HMAC is rejected; the caller closes the connection with no protocol
// response (spec line 883).
func ValidateChallengeResponse(response []byte, nonce, challenge string) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response, &envelope); err != nil {
		return fmt.Errorf("mcp: decode challenge response: %w", err)
	}
	raw, ok := envelope[ChallengeResponseParamKey]
	if !ok {
		return ErrChallengeResponseMissing
	}
	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		return fmt.Errorf("mcp: decode %s: %w", ChallengeResponseParamKey, err)
	}
	want := ExpectedChallengeResponse(nonce, challenge)
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return ErrChallengeResponseInvalid
	}
	return nil
}
