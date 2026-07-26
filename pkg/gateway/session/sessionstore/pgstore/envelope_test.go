// SPDX-License-Identifier: MIT

package pgstore

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
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
		Origin:                   "playground",
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
	// spec: §27.6 line 203 — the origin=playground label survives the bundle
	// round trip so a replica that reloads the row keeps the audit label.
	// F-27.6.8.
	if out.Origin != in.Origin {
		t.Errorf("Origin: got %q, want %q", out.Origin, in.Origin)
	}
}

// spec: §27.6 line 203 — a session carrying only the origin=playground label
// (no §14 envelope fields) still serializes the bundle so the label is durable.
// F-27.6.8.
func TestRequestEnvelopeOriginOnly_spec_27_6(t *testing.T) {
	arg, ok := requestEnvelopeArg(sessionstore.Session{Origin: "playground"}).(string)
	if !ok {
		t.Fatalf("requestEnvelopeArg(origin-only) = %T, want non-nil string", requestEnvelopeArg(sessionstore.Session{Origin: "playground"}))
	}
	var out sessionstore.Session
	applyStoredEnvelope(&out, []byte(arg))
	if out.Origin != "playground" {
		t.Errorf("Origin: got %q, want playground", out.Origin)
	}
}

// spec: §14 line 311 / §15.1 line 598 — the client labels ride the
// request-envelope bundle so the §15.1 list label filter has a durable
// JSONB target without a dedicated column; they round-trip back onto the
// Session's Labels field. F-15.1.15.
func TestRequestEnvelopeLabelsRoundTrip_spec_15_1_598(t *testing.T) {
	in := sessionstore.Session{Labels: map[string]string{"team": "payments", "env": "staging"}}
	arg, ok := requestEnvelopeArg(in).(string)
	if !ok {
		t.Fatalf("requestEnvelopeArg(labels-only) = %T, want non-nil string", requestEnvelopeArg(in))
	}
	var out sessionstore.Session
	applyStoredEnvelope(&out, []byte(arg))
	if !reflect.DeepEqual(out.Labels, in.Labels) {
		t.Errorf("Labels: got %v, want %v", out.Labels, in.Labels)
	}
}

// spec: §14 lines 108-150 — the §14 callback fields (callbackUrl, the
// DNS-pinned IP, the KMS-sealed secret, and undelivered events) ride the
// request-envelope bundle and round-trip back onto the Session. A session
// carrying only a callbackUrl still serializes the bundle. F-14.1.11.
func TestRequestEnvelopeCallbackRoundTrip_spec_14_108(t *testing.T) {
	in := sessionstore.Session{
		CallbackURL:      "https://hooks.example.com/lenny",
		CallbackPinnedIP: "93.184.216.34",
		CallbackSecret:   []byte("sealed-ciphertext-bytes"),
		WebhookEvents: []sessionstore.WebhookEventRecord{{
			EventID: "evt_1", EventType: "dev.lenny.session_completed",
			CallbackURL: "https://hooks.example.com/lenny", Attempts: 5, LastStatus: 500,
		}},
	}
	arg, ok := requestEnvelopeArg(in).(string)
	if !ok {
		t.Fatalf("requestEnvelopeArg(callback) = %T, want non-nil string", requestEnvelopeArg(in))
	}
	var out sessionstore.Session
	applyStoredEnvelope(&out, []byte(arg))
	if out.CallbackURL != in.CallbackURL || out.CallbackPinnedIP != in.CallbackPinnedIP {
		t.Errorf("callback url/pin: got %q/%q", out.CallbackURL, out.CallbackPinnedIP)
	}
	if string(out.CallbackSecret) != string(in.CallbackSecret) {
		t.Errorf("CallbackSecret: got %q, want %q", out.CallbackSecret, in.CallbackSecret)
	}
	if !reflect.DeepEqual(out.WebhookEvents, in.WebhookEvents) {
		t.Errorf("WebhookEvents: got %+v, want %+v", out.WebhookEvents, in.WebhookEvents)
	}
}

// spec: §15 built-in adapter single-shot compute model / §14.1 line 311 — a
// session carrying only a ContinuationParentID (the OpenResponsesAdapter
// previous_response_id lineage, no other §14.1 bundled field) still serializes
// the bundle rather than being dropped by the all-empty nil guard, and the
// pointer round-trips back onto the Session so GET /v1/responses/{id} echoes it.
func TestRequestEnvelopeContinuationParentOnly_spec_15(t *testing.T) {
	in := sessionstore.Session{ContinuationParentID: "resp_prior_0001"}
	arg, ok := requestEnvelopeArg(in).(string)
	if !ok {
		t.Fatalf("requestEnvelopeArg(continuation-only) = %T, want non-nil string; the all-empty guard must not drop a continuation-pointer-only session", requestEnvelopeArg(in))
	}
	var out sessionstore.Session
	applyStoredEnvelope(&out, []byte(arg))
	if out.ContinuationParentID != in.ContinuationParentID {
		t.Errorf("ContinuationParentID: got %q, want %q", out.ContinuationParentID, in.ContinuationParentID)
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
