// SPDX-License-Identifier: MIT

package pgstore

import (
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// conversationResponses returns n distinct Open Responses records.
func conversationResponses(n int) []sessionstore.ConversationResponse {
	if n == 0 {
		return nil
	}
	base := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	out := make([]sessionstore.ConversationResponse, n)
	for i := range out {
		out[i] = sessionstore.ConversationResponse{
			CreatedAt:          base.Add(time.Duration(i) * time.Minute),
			EndSeq:             uint64(10 * (i + 1)),
			RecoveryGeneration: int64(i / 2),
			Completed:          i < n-1,
		}
	}
	return out
}

// spec: §15 (Open Responses conversation sessions) — the per-response
// records and the requested runtime ref round-trip through the
// request-envelope bundle with nil, one, and many entries.
func TestRequestEnvelope_conversationRoundTrip_spec_15(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		ref  string
	}{
		{name: "nil", n: 0, ref: "claude-worker"},
		{name: "one", n: 1, ref: "claude-worker"},
		{name: "many", n: 7, ref: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := sessionstore.Session{
				ContinuationParentID: "11111111-1111-1111-1111-111111111111.2",
				Responses:            conversationResponses(tc.n),
				RequestedRuntimeRef:  tc.ref,
			}
			arg, ok := requestEnvelopeArg(in).(string)
			if !ok {
				t.Fatalf("requestEnvelopeArg returned %T, want string", requestEnvelopeArg(in))
			}
			var out sessionstore.Session
			applyStoredEnvelope(&out, []byte(arg))
			if !reflect.DeepEqual(out.Responses, in.Responses) {
				t.Errorf("Responses: got %+v, want %+v", out.Responses, in.Responses)
			}
			if out.RequestedRuntimeRef != in.RequestedRuntimeRef {
				t.Errorf("RequestedRuntimeRef: got %q, want %q", out.RequestedRuntimeRef, in.RequestedRuntimeRef)
			}
			if out.ContinuationParentID != in.ContinuationParentID {
				t.Errorf("ContinuationParentID: got %q, want %q", out.ContinuationParentID, in.ContinuationParentID)
			}
		})
	}
}

// spec: §15 (Open Responses conversation sessions) — a session carrying only
// response records, or only a requested runtime ref, stores a non-NULL
// bundle, so the all-empty NULL check does not drop conversation state.
func TestRequestEnvelope_conversationOnlyIsNotNull_spec_15(t *testing.T) {
	for name, in := range map[string]sessionstore.Session{
		"responses only":             {Responses: conversationResponses(1)},
		"requested runtime ref only": {RequestedRuntimeRef: "claude-worker"},
	} {
		t.Run(name, func(t *testing.T) {
			if requestEnvelopeArg(in) == nil {
				t.Fatalf("requestEnvelopeArg(%+v) = nil, want a stored bundle", in)
			}
		})
	}
}

// spec: §15 (Open Responses conversation sessions) — a stored bundle without
// conversation fields reads back with nil Responses and an empty requested
// runtime ref, and an empty Responses slice is omitted from the bundle.
func TestRequestEnvelope_conversationAbsent_spec_15(t *testing.T) {
	out := sessionstore.Session{Responses: conversationResponses(2), RequestedRuntimeRef: "stale"}
	applyStoredEnvelope(&out, []byte(`{"pool":"p"}`))
	if out.Responses != nil || out.RequestedRuntimeRef != "" {
		t.Fatalf("absent conversation fields read back as %+v / %q, want nil / empty", out.Responses, out.RequestedRuntimeRef)
	}
	if got := requestEnvelopeArg(sessionstore.Session{Responses: []sessionstore.ConversationResponse{}}); got != nil {
		t.Fatalf("requestEnvelopeArg(empty Responses) = %v, want nil", got)
	}
}
