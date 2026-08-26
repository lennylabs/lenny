//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_session_address_test is the Tier 3 contract suite for the
// address the gateway → pod adapter gRPC requests carry
// (schemas/lenny-adapter.proto).
//
// Every session is bound to a slot on every pod, whatever the pool's
// concurrency, and a session-mode slot's identifier is its session's
// identifier. A request therefore names one address, `session_id`, and the
// duplicate `slot_id` that stood beside it is gone: the field numbers and
// names it held are reserved so no later change recycles them onto a
// schema runtime authors and the generated compliance suite compile
// against.
//
// This suite reads the generated protoreflect descriptor to pin that no
// session-scoped request declares a second address, that each one declares
// `session_id`, that the removed numbers and names stay reserved, and that
// the `SlotId` wrapper message the fields carried is gone from the file.
package adapter_session_address_test

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// retiredFieldName is the proto field name of the duplicate address.
const retiredFieldName = "slot_id"

// retiredWrapperName is the message type every duplicate address carried.
const retiredWrapperName = protoreflect.FullName("lenny.adapter.v1.SlotId")

// sessionScopedMessages is every request message §4.1 addresses to one
// session, in both directions, together with the number the duplicate
// address held on it. A message is in this set when the specification's
// message-scope table gives it session scope; a pod-scoped message
// (`CoordinatorFenceRequest`) and a message that never carried the
// duplicate (`ExportPathsRequest`, `ConfigureWorkspaceRequest`) are out of
// it and are covered by the session-address arm below alone.
var sessionScopedMessages = map[string]protoreflect.FieldNumber{
	"PrepareWorkspaceRequest":      4,
	"FinalizeWorkspaceRequest":     5,
	"RunSetupRequest":              4,
	"StartSessionRequest":          11,
	"SendMessageRequest":           2,
	"AttachRequest":                2,
	"AssignCredentialsRequest":     3,
	"RotateCredentialsRequest":     4,
	"ExtendCredentialLeaseRequest": 5,
	"RevokeCredentialsRequest":     4,
	"InterruptRequest":             5,
	"SignalDeadlineRequest":        5,
	"ResumeRequest":                15,
	"CheckpointBarrierRequest":     4,
	"ReportUsageRequest":           4,
	"ShutdownRequest":              4,
	"CheckpointStart":              6,
	"ReportSessionScrubRequest":    3,
}

// messageDescriptors resolves each named message in the adapter proto's
// file descriptor.
func messageDescriptors(t *testing.T) protoreflect.MessageDescriptors {
	t.Helper()
	return (&adapterv1.StartSessionRequest{}).ProtoReflect().Descriptor().ParentFile().Messages()
}

// spec: 4.1 (the gRPC leg addresses a session by its session identifier),
// 5.2 (a session-mode slot's identifier is its session's identifier)
//
// diagnosis: a session-scoped request declares a second address again, so
// the wire carries the session twice and a producer and a consumer can
// disagree about which of the two is authoritative.
func TestSessionScopedRequestsDeclareNoSecondAddress_spec_4_1(t *testing.T) {
	t.Parallel()
	msgs := messageDescriptors(t)
	for name := range sessionScopedMessages {
		md := msgs.ByName(protoreflect.Name(name))
		if md == nil {
			t.Errorf("%s is not declared in the adapter proto", name)
			continue
		}
		if f := md.Fields().ByName(protoreflect.Name(retiredFieldName)); f != nil {
			t.Errorf("%s declares %s at number %d; the session is addressed by session_id alone",
				name, retiredFieldName, f.Number())
		}
	}
}

// spec: 5.2 (the address is required and non-empty), 4.1
//
// diagnosis: a session-scoped request lost its session address, so the
// adapter has nothing to resolve the session's slot from and the request
// cannot be attributed at all.
func TestSessionScopedRequestsDeclareTheSessionAddress_spec_4_1(t *testing.T) {
	t.Parallel()
	msgs := messageDescriptors(t)
	for name := range sessionScopedMessages {
		md := msgs.ByName(protoreflect.Name(name))
		if md == nil {
			continue
		}
		f := md.Fields().ByName("session_id")
		if f == nil {
			t.Errorf("%s declares no session_id; the session address is the request's only address", name)
			continue
		}
		if got := f.Message().FullName(); got != "lenny.adapter.v1.SessionId" {
			t.Errorf("%s.session_id carries %s, want lenny.adapter.v1.SessionId", name, got)
		}
	}
}

// spec: 15.4 (breaking changes to the published adapter proto follow
// buf-style breaking-change rules)
//
// diagnosis: a removed field number or name was recycled onto a message a
// runtime author's generated code already compiles against, which changes
// the bytes on the wire without changing the schema's field names. §4.1
// states that a session-scoped request carries one address; the rule that a
// removed number and name stay reserved is §15.4's stability rule on the
// published artifact, so the case is credited there.
func TestRemovedAddressNumbersAndNamesStayReserved_spec_15_4(t *testing.T) {
	t.Parallel()
	msgs := messageDescriptors(t)
	for name, num := range sessionScopedMessages {
		md := msgs.ByName(protoreflect.Name(name))
		if md == nil {
			continue
		}
		if !reservesNumber(md, num) {
			t.Errorf("%s does not reserve field number %d, which %s held", name, num, retiredFieldName)
		}
		if !reservesName(md, retiredFieldName) {
			t.Errorf("%s does not reserve the name %q", name, retiredFieldName)
		}
	}
}

// spec: 15.4 (the published adapter proto is the contract a runtime author
// implements against)
//
// diagnosis: the wrapper type survives with no field carrying it, so the
// published schema still advertises a second address to a runtime author
// reading it.
func TestTheRetiredAddressWrapperIsGone_spec_15_4(t *testing.T) {
	t.Parallel()
	msgs := messageDescriptors(t)
	for i := 0; i < msgs.Len(); i++ {
		if msgs.Get(i).FullName() == retiredWrapperName {
			t.Fatalf("%s is still declared; the last field carrying it was removed", retiredWrapperName)
		}
	}
}

// reservesNumber reports whether md reserves the given field number.
func reservesNumber(md protoreflect.MessageDescriptor, num protoreflect.FieldNumber) bool {
	ranges := md.ReservedRanges()
	for i := 0; i < ranges.Len(); i++ {
		r := ranges.Get(i)
		if num >= r[0] && num < r[1] {
			return true
		}
	}
	return false
}

// reservesName reports whether md reserves the given field name.
func reservesName(md protoreflect.MessageDescriptor, name string) bool {
	names := md.ReservedNames()
	for i := 0; i < names.Len(); i++ {
		if string(names.Get(i)) == name {
			return true
		}
	}
	return false
}
