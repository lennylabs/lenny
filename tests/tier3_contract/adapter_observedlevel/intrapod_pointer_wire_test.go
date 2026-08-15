//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_observedlevel_test is the Tier 3 contract suite for the
// intra-pod documentation the gateway ↔ pod adapter gRPC contract ships to
// its consumers. The proto source under schemas/ is the authored artifact,
// and the generated bindings under pkg/proto/adapter/v1 are what a Go
// consumer of the contract actually reads: protoc copies each comment into
// the stub verbatim. Three comments describe intra-pod behavior the
// §28.5.3 contract cards state (CH-RUNTIMEOPS for the runtime-operations
// channel, CH-MCP-PLATFORM for the intra-pod platform MCP server), so the
// generated bindings must carry the card citation rather than §4.7, which
// states none of that material.
//
// This suite pins both halves of the pair: the comment text the consumer
// reads, and the wire value the interrupt-timeout comment annotates.
package adapter_observedlevel_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// generatedStub is one generated binding and the comment fragments it must
// and must not carry.
type generatedStub struct {
	rel      string
	required []string
	retired  []string
}

// spec: 28.5.3 (intra-pod contract cards), 28.7 (wire-contract artifact
//
//	register), 15.4 (runtime adapter specification)
//
// diagnosis: the generated gRPC bindings under pkg/proto/adapter/v1 still
//
//	attribute the intra-pod lifecycle handshake, the platform MCP
//	server, or the INTERRUPT_TIMEOUT status to §4.7. Either the
//	proto comment was not corrected or `make generate-proto` was
//	not run after it was, so the contract a Go consumer reads and
//	the authored proto disagree.
func TestGeneratedStubsCiteIntraPodChannelCards(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	for _, stub := range []generatedStub{
		{
			rel: "pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go",
			required: []string{
				"completed the §28.5.3 CH-RUNTIMEOPS",
				"connected to the §28.5.3 CH-MCP-PLATFORM",
			},
			retired: []string{
				"completed the §4.7",
				"intra-pod platform MCP server (standard)",
			},
		},
		{
			rel: "pkg/proto/adapter/v1/lenny-adapter.pb.go",
			required: []string{
				"first §28.5.3 CH-RUNTIMEOPS lifecycle handshake",
				"no acknowledgement (§28.5.3 CH-RUNTIMEOPS)",
			},
			retired: []string{
				"first §4.7 lifecycle handshake",
				"no acknowledgement (§4.7)",
			},
		},
	} {
		t.Run(filepath.Base(stub.rel), func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(filepath.Join(root, stub.rel))
			if err != nil {
				t.Fatalf("read %s: %v", stub.rel, err)
			}
			src := string(data)
			for _, want := range stub.required {
				if !strings.Contains(src, want) {
					t.Errorf("%s must carry %q", stub.rel, want)
				}
			}
			for _, unwanted := range stub.retired {
				if strings.Contains(src, unwanted) {
					t.Errorf("%s still carries the retired pointer %q", stub.rel, unwanted)
				}
			}
		})
	}
}

// spec: 28.5.3 (intra-pod contract cards, INTERRUPT_TIMEOUT), 4.7 (runtime
//
//	adapter)
//
// diagnosis: the interrupt status the corrected comment annotates changed
//
//	name or wire number, so the comment now documents a different
//	value than the one §28.5.3 states the adapter returns when the
//	interrupt deadline elapses with no acknowledgement.
func TestInterruptTimeoutStatusWireValueUnchanged(t *testing.T) {
	t.Parallel()

	const wantNumber = 2
	if got := int32(adapterv1.InterruptResponse_STATUS_INTERRUPT_TIMEOUT); got != wantNumber {
		t.Errorf("STATUS_INTERRUPT_TIMEOUT = %d, want %d", got, wantNumber)
	}
	if got := adapterv1.InterruptResponse_STATUS_INTERRUPT_TIMEOUT.String(); got != "STATUS_INTERRUPT_TIMEOUT" {
		t.Errorf("status name = %q, want STATUS_INTERRUPT_TIMEOUT", got)
	}
	// The neighbouring BUSY status is stated by §4.7 rather than a
	// §28.5.3 card, and it keeps its own number.
	if got := int32(adapterv1.InterruptResponse_STATUS_BUSY); got != 3 {
		t.Errorf("STATUS_BUSY = %d, want 3", got)
	}
}
