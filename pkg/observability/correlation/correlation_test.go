// SPDX-License-Identifier: MIT

package correlation

import (
	"context"
	"net/http"
	"testing"
)

func TestFieldsIsEmpty(t *testing.T) {
	if !(Fields{}).IsEmpty() {
		t.Fatal("zero Fields should report IsEmpty == true")
	}
	if (Fields{TenantID: "acme"}).IsEmpty() {
		t.Fatal("Fields with TenantID should not be IsEmpty")
	}
	if (Fields{Component: "gateway"}).IsEmpty() {
		t.Fatal("Fields with Component should not be IsEmpty")
	}
}

func TestWithAndFromRoundTrip(t *testing.T) {
	want := Fields{
		TraceID:   "0123456789abcdef0123456789abcdef",
		SpanID:    "0123456789abcdef",
		TenantID:  "acme",
		SessionID: "sess_42",
		Component: "gateway",
	}
	ctx := With(context.Background(), want)
	got := From(ctx)
	if got != want {
		t.Fatalf("From(With(...)) mismatch:\n  want %+v\n  got  %+v", want, got)
	}
}

func TestFromNilContextReturnsZero(t *testing.T) {
	// The test exists exactly to verify the nil-context branch is
	// defensive; binding a typed-nil variable disables the
	// staticcheck SA1012 warning that fires on bare nil literals.
	var ctx context.Context
	got := From(ctx)
	if !got.IsEmpty() {
		t.Fatalf("From(nil) should return empty Fields, got %+v", got)
	}
}

func TestFromMissingValueReturnsZero(t *testing.T) {
	got := From(context.Background())
	if !got.IsEmpty() {
		t.Fatalf("From(no value) should return empty Fields, got %+v", got)
	}
}

func TestMergeOverridesOnlyNonEmpty(t *testing.T) {
	base := Fields{
		TenantID:  "acme",
		Component: "gateway",
		SessionID: "sess_initial",
	}
	override := Fields{
		SessionID:   "sess_overridden",
		OperationID: "op_42",
	}
	got := base.Merge(override)
	if got.TenantID != "acme" {
		t.Errorf("TenantID should survive merge, got %q", got.TenantID)
	}
	if got.Component != "gateway" {
		t.Errorf("Component should survive merge, got %q", got.Component)
	}
	if got.SessionID != "sess_overridden" {
		t.Errorf("SessionID should be overridden, got %q", got.SessionID)
	}
	if got.OperationID != "op_42" {
		t.Errorf("OperationID should be set from override, got %q", got.OperationID)
	}
}

func TestMergeEmptyOverrideIsNoOp(t *testing.T) {
	base := Fields{TenantID: "acme", Component: "gateway"}
	got := base.Merge(Fields{})
	if got != base {
		t.Fatalf("Merge with empty override should be a no-op:\n  want %+v\n  got  %+v", base, got)
	}
}

func TestWithComponentReplacesComponentOnly(t *testing.T) {
	base := Fields{TenantID: "acme", Component: "gateway"}
	ctx := With(context.Background(), base)
	ctx = WithComponent(ctx, "lenny-ops")
	got := From(ctx)
	if got.Component != "lenny-ops" {
		t.Errorf("Component should be replaced, got %q", got.Component)
	}
	if got.TenantID != "acme" {
		t.Errorf("TenantID should survive WithComponent, got %q", got.TenantID)
	}
}

func TestFromHTTPHeaderRoundTrip(t *testing.T) {
	h := http.Header{}
	want := Fields{
		TraceID:     "0123456789abcdef0123456789abcdef",
		SpanID:      "0123456789abcdef",
		SessionID:   "sess_42",
		TenantID:    "acme",
		OperationID: "op_42",
		AgentName:   "alice-agent",
	}
	want.InjectHTTPHeader(h)
	got := FromHTTPHeader(h)
	if got != want {
		t.Fatalf("HTTP header round trip mismatch:\n  want %+v\n  got  %+v", want, got)
	}
}

func TestInjectHTTPHeaderEmptyFieldsLeavesHeaderUntouched(t *testing.T) {
	h := http.Header{}
	h.Set(HeaderTenantID, "globex")
	(Fields{}).InjectHTTPHeader(h)
	if got := h.Get(HeaderTenantID); got != "globex" {
		t.Fatalf("InjectHTTPHeader from empty Fields should not overwrite, got %q", got)
	}
}

func TestInjectHTTPHeaderNilHeaderIsNoOp(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("InjectHTTPHeader(nil) should not panic, got %v", r)
		}
	}()
	(Fields{TenantID: "acme"}).InjectHTTPHeader(nil)
}

func TestFromHTTPHeaderMalformedTraceparentIgnored(t *testing.T) {
	h := http.Header{}
	h.Set(HeaderTraceParent, "not-a-real-traceparent")
	got := FromHTTPHeader(h)
	if got.TraceID != "" || got.SpanID != "" {
		t.Fatalf("malformed traceparent should drop to empty, got %+v", got)
	}
}

func TestFromGRPCMetadataLowerCaseKeys(t *testing.T) {
	md := map[string][]string{
		"x-lenny-tenant-id":    {"acme"},
		"x-lenny-operation-id": {"op_42"},
		"traceparent":          {"00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"},
	}
	got := FromGRPCMetadata(md)
	if got.TenantID != "acme" {
		t.Errorf("TenantID: want acme, got %q", got.TenantID)
	}
	if got.OperationID != "op_42" {
		t.Errorf("OperationID: want op_42, got %q", got.OperationID)
	}
	if got.TraceID != "0123456789abcdef0123456789abcdef" {
		t.Errorf("TraceID: want full hex, got %q", got.TraceID)
	}
	if got.SpanID != "0123456789abcdef" {
		t.Errorf("SpanID: want hex, got %q", got.SpanID)
	}
}

func TestFromGRPCMetadataNilReturnsZero(t *testing.T) {
	got := FromGRPCMetadata(nil)
	if !got.IsEmpty() {
		t.Fatalf("nil metadata should return empty Fields, got %+v", got)
	}
}
