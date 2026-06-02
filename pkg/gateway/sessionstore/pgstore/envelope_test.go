// SPDX-License-Identifier: MIT

package pgstore

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// spec: §14 lines 47-50 — envArg renders the client env map as a JSONB
// argument; nil/empty maps store SQL NULL. F-14.1.12.
func TestEnvArg_spec_14(t *testing.T) {
	if got := envArg(nil); got != nil {
		t.Errorf("envArg(nil) = %v, want nil", got)
	}
	if got := envArg(map[string]string{}); got != nil {
		t.Errorf("envArg({}) = %v, want nil", got)
	}
	got, ok := envArg(map[string]string{"NODE_ENV": "production"}).(string)
	if !ok || got == "" || got[0] != '{' {
		t.Fatalf("envArg(map) = %v, want JSONB object literal", got)
	}
}

// spec: §14.1 line 311 — requestEnvelopeArg bundles the §14.1 envelope
// fields; a session carrying none of them stores SQL NULL. F-14.1.14.
func TestRequestEnvelopeArg_emptyIsNull_spec_14_1(t *testing.T) {
	if got := requestEnvelopeArg(sessionstore.Session{}); got != nil {
		t.Errorf("requestEnvelopeArg(empty) = %v, want nil", got)
	}
}

// spec: §14.1 — the request envelope bundle round-trips through the JSONB
// argument and applyStoredEnvelope back onto the distinct Session fields.
// F-14.1.14 / F-14.1.15.
func TestRequestEnvelopeRoundTrip_spec_14_1(t *testing.T) {
	depth, kids := 2, 5
	in := sessionstore.Session{
		Pool:                     "claude-worker-sandboxed-medium",
		Timeouts:                 &sessionstore.SessionTimeouts{MaxSessionAgeSeconds: 1800, MaxIdleSeconds: 300},
		CredentialPolicyOverride: &sessionstore.CredentialPolicyOverride{PreferredSource: "pool"},
		DelegationLeaseRequest:   &sessionstore.DelegationLeaseRequest{MaxDepth: &depth, MaxChildrenTotal: &kids, DelegationPolicyRef: "default-policy"},
		RuntimeOptions:           json.RawMessage(`{"streamingMode":true}`),
	}
	arg, ok := requestEnvelopeArg(in).(string)
	if !ok {
		t.Fatalf("requestEnvelopeArg returned %T, want string", requestEnvelopeArg(in))
	}

	var out sessionstore.Session
	applyStoredEnvelope(&out, []byte(arg))

	if out.Pool != in.Pool {
		t.Errorf("Pool: got %q, want %q", out.Pool, in.Pool)
	}
	if !reflect.DeepEqual(out.Timeouts, in.Timeouts) {
		t.Errorf("Timeouts: got %+v, want %+v", out.Timeouts, in.Timeouts)
	}
	if !reflect.DeepEqual(out.CredentialPolicyOverride, in.CredentialPolicyOverride) {
		t.Errorf("CredentialPolicyOverride: got %+v, want %+v", out.CredentialPolicyOverride, in.CredentialPolicyOverride)
	}
	if !reflect.DeepEqual(out.DelegationLeaseRequest, in.DelegationLeaseRequest) {
		t.Errorf("DelegationLeaseRequest: got %+v, want %+v", out.DelegationLeaseRequest, in.DelegationLeaseRequest)
	}
	if string(out.RuntimeOptions) != string(in.RuntimeOptions) {
		t.Errorf("RuntimeOptions: got %s, want %s", out.RuntimeOptions, in.RuntimeOptions)
	}
}

// applyStoredEnvelope must tolerate a malformed payload (the gateway
// validates the bundle at admission, so a stored value is by-construction
// well-formed; a corrupt one must not panic). F-14.1.14.
func TestApplyStoredEnvelope_malformedIsNoop_spec_14_1(t *testing.T) {
	var s sessionstore.Session
	applyStoredEnvelope(&s, []byte("not json"))
	if s.Pool != "" || s.Timeouts != nil || s.RuntimeOptions != nil {
		t.Errorf("malformed payload mutated the session: %+v", s)
	}
}
