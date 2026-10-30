//go:build contract

// SPDX-License-Identifier: MIT

// Package interceptor_proto_test is the Tier 3 contract suite for the
// §4.8 RequestInterceptor gRPC extension point. External interceptors are
// deployer-supplied gRPC services in any language, so the on-the-wire
// protobuf contract (field numbers, the Action enum integers, the service
// method name) is the interoperability surface a polyglot deployer must
// match. A field-number or enum-value drift on the gateway side would
// silently break every external interceptor that was generated against the
// published protobuf definition. These tests read the compiled protoreflect
// descriptors (the contract the gateway client and the interceptor server
// exchange) and exercise the binary wire form, so any drift that survives a
// coordinated proto edit plus regeneration is still caught.
package interceptor_proto_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	interceptorv1 "github.com/lennylabs/lenny/pkg/proto/interceptor/v1"
)

// wantField is one expected field on an interceptor wire message: its proto
// field name, its field number, and its scalar kind.
type wantField struct {
	name   string
	number protoreflect.FieldNumber
	kind   protoreflect.Kind
}

// assertFields pins the exact field set and numbering of a message
// descriptor against want, failing on a missing field, an extra field, a
// renumber, or a kind change.
func assertFields(t *testing.T, md protoreflect.MessageDescriptor, want []wantField) {
	t.Helper()
	fields := md.Fields()
	if got := fields.Len(); got != len(want) {
		t.Fatalf("%s has %d fields, want %d (§4.8 fixes the field set)", md.Name(), got, len(want))
	}
	for _, w := range want {
		f := fields.ByName(protoreflect.Name(w.name))
		if f == nil {
			t.Errorf("%s: field %q missing from the wire contract", md.Name(), w.name)
			continue
		}
		if f.Number() != w.number {
			t.Errorf("%s: field %q has number %d, want %d", md.Name(), w.name, f.Number(), w.number)
		}
		if f.Kind() != w.kind {
			t.Errorf("%s: field %q has kind %v, want %v", md.Name(), w.name, f.Kind(), w.kind)
		}
	}
}

// TestInterceptRequestWireContract pins the InterceptRequest field set and
// numbering against the §4.8 protobuf definition: phase (1), session_id (2),
// tenant_id (3), content (4), and the metadata map (5). A polyglot
// interceptor generated from the published proto encodes these numbers into
// its wire tags, so a renumber on the gateway side would make the gateway
// misread every field an external interceptor sent, and vice versa.
//
// spec: 4.8 (RequestInterceptor extension point, InterceptRequest message)
//
// diagnosis: the InterceptRequest wire contract diverged from the §4.8
// protobuf definition. A field was added, removed, renamed, or renumbered,
// so an external interceptor generated against the published proto no longer
// exchanges bytes the gateway can decode. Re-align schemas/lenny-interceptor.proto
// with §4.8 and run `make generate-proto`.
func TestInterceptRequestWireContract(t *testing.T) {
	md := (&interceptorv1.InterceptRequest{}).ProtoReflect().Descriptor()
	assertFields(t, md, []wantField{
		{name: "phase", number: 1, kind: protoreflect.StringKind},
		{name: "session_id", number: 2, kind: protoreflect.StringKind},
		{name: "tenant_id", number: 3, kind: protoreflect.StringKind},
		{name: "content", number: 4, kind: protoreflect.BytesKind},
		// A proto3 map<string,string> is a repeated message of map entries;
		// its Kind is MessageKind and IsMap() reports the map property.
		{name: "metadata", number: 5, kind: protoreflect.MessageKind},
	})

	// The metadata field carries the identity keys a §4.8 external
	// interceptor reads (user_id, tenant_id, roles/claims populated by
	// AuthEvaluator). Pin that it is a string-keyed, string-valued map so
	// those keys round-trip as the deployer contract requires.
	meta := md.Fields().ByName("metadata")
	if !meta.IsMap() {
		t.Fatalf("InterceptRequest.metadata must be a map, got IsMap()=false")
	}
	if k := meta.MapKey().Kind(); k != protoreflect.StringKind {
		t.Errorf("InterceptRequest.metadata key kind = %v, want string", k)
	}
	if v := meta.MapValue().Kind(); v != protoreflect.StringKind {
		t.Errorf("InterceptRequest.metadata value kind = %v, want string", v)
	}
}

// TestInterceptResponseWireContract pins the InterceptResponse field set and
// numbering against the §4.8 protobuf definition: action (1), reason (2), and
// modified_content (3).
//
// spec: 4.8 (RequestInterceptor extension point, InterceptResponse message)
//
// diagnosis: the InterceptResponse wire contract diverged from the §4.8
// protobuf definition. The gateway reads an interceptor's decision from these
// fields; a renumber or a dropped field means the gateway misreads the action
// or the modified content an external interceptor returns. Re-align
// schemas/lenny-interceptor.proto with §4.8 and run `make generate-proto`.
func TestInterceptResponseWireContract(t *testing.T) {
	md := (&interceptorv1.InterceptResponse{}).ProtoReflect().Descriptor()
	assertFields(t, md, []wantField{
		{name: "action", number: 1, kind: protoreflect.EnumKind},
		{name: "reason", number: 2, kind: protoreflect.StringKind},
		{name: "modified_content", number: 3, kind: protoreflect.BytesKind},
	})
}

// TestInterceptResponseActionEnumValues pins the Action enum integers the
// §4.8 protobuf definition fixes: ALLOW=0, REJECT=1, MODIFY=2. proto3
// transmits an enum as its integer, so a value-name-to-number remap on the
// gateway side would silently invert an external interceptor's decision (an
// interceptor sending REJECT=1 would be read as MODIFY, for example). This
// reads both the compiled constants and the enum descriptor so a reorder that
// survives regeneration is still caught.
//
// spec: 4.8 (InterceptResponse.Action: ALLOW = 0, REJECT = 1, MODIFY = 2)
//
// diagnosis: the Action enum integers diverged from the §4.8 protobuf
// definition. Because the decision travels as a bare integer on the wire, a
// remapped value silently changes what the gateway does with a request an
// external interceptor allowed, rejected, or modified. Re-align the enum in
// schemas/lenny-interceptor.proto with §4.8 and run `make generate-proto`.
func TestInterceptResponseActionEnumValues(t *testing.T) {
	// The generated constants must carry the §4.8 integers.
	if got := int32(interceptorv1.InterceptResponse_ALLOW); got != 0 {
		t.Errorf("ALLOW = %d, want 0", got)
	}
	if got := int32(interceptorv1.InterceptResponse_REJECT); got != 1 {
		t.Errorf("REJECT = %d, want 1", got)
	}
	if got := int32(interceptorv1.InterceptResponse_MODIFY); got != 2 {
		t.Errorf("MODIFY = %d, want 2", got)
	}

	// The enum descriptor (the wire contract itself) must map the same
	// names to the same numbers.
	ed := interceptorv1.InterceptResponse_ALLOW.Descriptor()
	for name, num := range map[string]protoreflect.EnumNumber{
		"ALLOW": 0, "REJECT": 1, "MODIFY": 2,
	} {
		v := ed.Values().ByName(protoreflect.Name(name))
		if v == nil {
			t.Errorf("Action enum: value %q missing from the wire contract", name)
			continue
		}
		if v.Number() != num {
			t.Errorf("Action enum: %q = %d, want %d", name, v.Number(), num)
		}
	}
}

// TestInterceptServiceMethodName pins the RequestInterceptor service and its
// single Intercept(InterceptRequest) returns (InterceptResponse) method. The
// fully qualified service name and method name form the gRPC path a polyglot
// interceptor server registers and the gateway dials; a rename would make
// every external interceptor unreachable with an UNIMPLEMENTED error.
//
// spec: 4.8 (service RequestInterceptor { rpc Intercept(InterceptRequest)
// returns (InterceptResponse); })
//
// diagnosis: the RequestInterceptor gRPC service or its Intercept method was
// renamed, so the gateway dials a method path no external interceptor serves.
// Re-align schemas/lenny-interceptor.proto with §4.8 and run
// `make generate-proto`.
func TestInterceptServiceMethodName(t *testing.T) {
	const wantService = "lenny.interceptor.v1.RequestInterceptor"
	if got := interceptorv1.RequestInterceptor_ServiceDesc.ServiceName; got != wantService {
		t.Errorf("service name = %q, want %q", got, wantService)
	}
	methods := interceptorv1.RequestInterceptor_ServiceDesc.Methods
	if len(methods) != 1 {
		t.Fatalf("RequestInterceptor has %d methods, want 1 (Intercept)", len(methods))
	}
	if got := methods[0].MethodName; got != "Intercept" {
		t.Errorf("method name = %q, want %q", got, "Intercept")
	}
}

// TestInterceptRequestRoundTrip pins that a representative InterceptRequest
// carrying every field, including the identity metadata keys an external
// interceptor reads, survives a binary marshal/unmarshal intact. This is the
// exact exchange the gateway performs against a deployer-supplied gRPC
// interceptor, so a wire-form regression that a descriptor check alone might
// miss (a value that fails to encode or decode) surfaces here.
//
// spec: 4.8 (InterceptRequest fields; metadata carries authenticated
// identity user_id/tenant_id for priority > 100 external interceptors)
//
// diagnosis: an InterceptRequest no longer round-trips through the proto
// binary wire form, so the gateway and an external interceptor disagree on
// the bytes for a phase, session, tenant, content payload, or the identity
// metadata map. Check schemas/lenny-interceptor.proto and the generated code.
func TestInterceptRequestRoundTrip(t *testing.T) {
	in := &interceptorv1.InterceptRequest{
		Phase:     "PreDelegation",
		SessionId: "sess-01HX9F0YWXKK0V7QZ7G6P3R5JN",
		TenantId:  "acme",
		Content:   []byte("delegate this task"),
		Metadata: map[string]string{
			"user_id":   "alice@acme.com",
			"tenant_id": "acme",
		},
	}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out interceptorv1.InterceptRequest
	if err := proto.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !proto.Equal(in, &out) {
		t.Fatalf("round-trip mismatch:\n got %v\nwant %v", &out, in)
	}
	if out.GetMetadata()["user_id"] != "alice@acme.com" {
		t.Errorf("metadata user_id lost in round-trip: got %q", out.GetMetadata()["user_id"])
	}
}

// TestInterceptResponseMODIFYRoundTrip pins that a MODIFY response carrying
// modified_content survives the binary wire form, since §4.8 documents that
// the gateway applies modified_content only when action = MODIFY. The
// gateway reads action as the integer 2 and the replacement payload from
// field 3.
//
// spec: 4.8 (InterceptResponse: action = MODIFY returns modified content in
// modified_content)
//
// diagnosis: a MODIFY InterceptResponse no longer round-trips, so the gateway
// cannot recover the replacement payload an external interceptor returned for
// a MODIFY decision. Check schemas/lenny-interceptor.proto and the generated
// code.
func TestInterceptResponseMODIFYRoundTrip(t *testing.T) {
	in := &interceptorv1.InterceptResponse{
		Action:          interceptorv1.InterceptResponse_MODIFY,
		Reason:          "redacted secret",
		ModifiedContent: []byte("delegate this [REDACTED]"),
	}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out interceptorv1.InterceptResponse
	if err := proto.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !proto.Equal(in, &out) {
		t.Fatalf("round-trip mismatch:\n got %v\nwant %v", &out, in)
	}
	if out.GetAction() != interceptorv1.InterceptResponse_MODIFY {
		t.Errorf("action lost in round-trip: got %v, want MODIFY", out.GetAction())
	}
	if string(out.GetModifiedContent()) != "delegate this [REDACTED]" {
		t.Errorf("modified_content lost in round-trip: got %q", out.GetModifiedContent())
	}
}
