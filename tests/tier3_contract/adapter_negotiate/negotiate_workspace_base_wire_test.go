//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_negotiate_test is the Tier 3 contract suite for the
// workspace path the adapter reports on the §15.5 version handshake.
//
// The per-slot tree is the only layout, so the adapter no longer reports a
// pod-global working directory. It reports the base it nests every
// session's tree under, and the gateway derives the session's cwd from
// that base and the session identifier. The retired field's number and
// name stay reserved, because recycling a field number for a new meaning
// changes the bytes a runtime author's generated code already produces
// without changing any name that author reads.
package adapter_negotiate_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// retiredRootField is the pod-global working directory the handshake used
// to report, and retiredRootNumber is the field number it held.
const (
	retiredRootField  = "workspace_root"
	retiredRootNumber = protoreflect.FieldNumber(5)
)

// negotiateDescriptor resolves the handshake response message.
func negotiateDescriptor() protoreflect.MessageDescriptor {
	return (&adapterv1.NegotiateVersionResponse{}).ProtoReflect().Descriptor()
}

// spec: 6.4 (the per-slot tree is the only layout), 7.3 step (d) (the
// gateway captures the session's cwd from the reported base)
//
// diagnosis: a failure means the handshake either still reports a
// pod-global working directory or has recycled the number that field held.
// Recycling number 5 is the silent case: both ends regenerate from the same
// proto and agree with each other, while an adapter built against the
// earlier schema decodes the new meaning under the old name and points
// every session at a directory that does not exist.
func TestHandshakeReservesTheRetiredWorkspaceRoot_spec_6_4(t *testing.T) {
	t.Parallel()
	md := negotiateDescriptor()
	if f := md.Fields().ByName(retiredRootField); f != nil {
		t.Errorf("NegotiateVersionResponse declares %s at number %d; the session root is derived from the base",
			retiredRootField, f.Number())
	}
	if !reservesNumber(md, retiredRootNumber) {
		t.Errorf("NegotiateVersionResponse does not reserve field number %d, which %s held",
			retiredRootNumber, retiredRootField)
	}
	if !reservesName(md, retiredRootField) {
		t.Errorf("NegotiateVersionResponse does not reserve the name %q", retiredRootField)
	}
}

// spec: 6.4 (the workspace base every session's tree nests under), 7.3
// step (d)
//
// diagnosis: a failure means the handshake declares no workspace base, or
// declares it with the wrong number or type, so the gateway has nothing to
// derive a session's cwd from and cannot persist a root for a later resume
// to assert against.
func TestHandshakeDeclaresTheWorkspaceBase_spec_6_4(t *testing.T) {
	t.Parallel()
	f := negotiateDescriptor().Fields().ByName("workspace_base")
	if f == nil {
		t.Fatal("NegotiateVersionResponse declares no workspace_base")
	}
	if f.Number() != 6 {
		t.Errorf("workspace_base is field %d, want 6", f.Number())
	}
	if f.Kind() != protoreflect.StringKind {
		t.Errorf("workspace_base is %v, want string", f.Kind())
	}
}

// spec: 6.4, 7.3 step (d)
//
// diagnosis: a failure means the reported base does not survive a wire
// round trip on the number the schema assigns it, so the gateway reads an
// empty base and derives every session cwd from nothing.
func TestHandshakeCarriesTheWorkspaceBaseOnTheWire_spec_6_4(t *testing.T) {
	t.Parallel()
	const base = "/workspace"
	b, err := proto.Marshal(&adapterv1.NegotiateVersionResponse{
		SelectedProtocolVersion: "1.0",
		WorkspaceBase:           base,
	})
	if err != nil {
		t.Fatalf("marshal NegotiateVersionResponse: %v", err)
	}
	var got adapterv1.NegotiateVersionResponse
	if err := proto.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal NegotiateVersionResponse: %v", err)
	}
	if got.GetWorkspaceBase() != base {
		t.Errorf("workspace_base round trip = %q, want %q", got.GetWorkspaceBase(), base)
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
