//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_events_test is the Tier 3 contract suite for the event
// taxonomy the gateway ↔ pod adapter gRPC contract ships to its consumers.
// The proto source under schemas/ is the authored artifact, and the
// generated bindings under pkg/proto/adapter/v1 are what a Go consumer of
// the contract actually reads: protoc copies each comment into the stub
// verbatim. The two comments on the adapter→gateway event stream state the
// event set §4.7.3 tables, so the generated bindings must carry that event
// set and the §4.7.3 pointer rather than the intra-pod CH-RUNTIMEOPS frames
// or schemas/lenny-adapter-jsonl.schema.json, which schematizes the binary
// stdin/stdout frames and no adapter→gateway event.
//
// This suite pins both halves of the pair: the comment text the consumer
// reads, and the envelope the comment annotates.
package adapter_events_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// adapterEventSet is the event set the adapter emits on the stream, as
// §4.7.3 tables it.
var adapterEventSet = []string{
	"RATE_LIMITED",
	"AUTH_EXPIRED",
	"PROVIDER_UNAVAILABLE",
	"LEASE_REJECTED",
	"AdapterTerminating",
	"FINAL_USAGE_REPORT",
	"CheckpointBarrierAck",
}

// retiredTaxonomyTokens are the intra-pod frames and the wrong taxonomy
// owner neither comment on this stream may carry. Both are legitimate
// elsewhere in the bindings, so the check is scoped to the two comments.
var retiredTaxonomyTokens = []string{
	"checkpoint_ready",
	"interrupt_acknowledged",
	"credentials_acknowledged",
	"deadline_approaching",
	"lenny-adapter-jsonl.schema.json",
	"§15.4",
}

// generatedComment returns the contiguous `//` comment block immediately
// above the first line containing marker. The second result is false when
// no line carries the marker or no comment precedes it.
func generatedComment(src, marker string) (string, bool) {
	lines := strings.Split(src, "\n")
	idx := -1
	for i, line := range lines {
		if strings.Contains(line, marker) {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return "", false
	}
	start := idx
	for start > 0 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "//") {
		start--
	}
	if start == idx {
		return "", false
	}
	return strings.Join(lines[start:idx], "\n"), true
}

// spec: 4.7.3 (adapter events on CH-ADAPTEREVENTS), 28.7 (wire-contract
//
//	artifact register)
//
// diagnosis: the generated gRPC bindings under pkg/proto/adapter/v1 state a
//
//	different event taxonomy for the adapter→gateway stream than the
//	authored proto does. Either the proto comments were not
//	corrected or `make generate-proto` was not run after they were,
//	so the contract a Go consumer reads and the authored proto
//	disagree.
func TestGeneratedStubsStateTheAdapterEventTaxonomy(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	rpcRequired := append(append([]string{}, adapterEventSet...), "spec/04_system-components.md", "§4.7.3")
	for _, site := range []struct {
		name     string
		rel      string
		marker   string
		required []string
	}{
		{
			name:     "client interface",
			rel:      "pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go",
			marker:   "AdapterEvents(ctx context.Context",
			required: rpcRequired,
		},
		{
			name:     "server interface",
			rel:      "pkg/proto/adapter/v1/lenny-adapter_grpc.pb.go",
			marker:   "AdapterEvents(grpc.BidiStreamingServer",
			required: rpcRequired,
		},
		{
			name:     "envelope message",
			rel:      "pkg/proto/adapter/v1/lenny-adapter.pb.go",
			marker:   "type AdapterEventsRequest struct",
			required: []string{"Opaque adapter event envelope", "spec/04_system-components.md", "§4.7.3"},
		},
	} {
		t.Run(site.name, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(filepath.Join(root, site.rel))
			if err != nil {
				t.Fatalf("read %s: %v", site.rel, err)
			}
			block, ok := generatedComment(string(data), site.marker)
			if !ok {
				t.Fatalf("%s: no comment above %q", site.rel, site.marker)
			}
			for _, want := range site.required {
				if !strings.Contains(block, want) {
					t.Errorf("%s %s comment must carry %q, got:\n%s", site.rel, site.name, want, block)
				}
			}
			for _, unwanted := range retiredTaxonomyTokens {
				if strings.Contains(block, unwanted) {
					t.Errorf("%s %s comment still credits the adapter→gateway stream with %q", site.rel, site.name, unwanted)
				}
			}
		})
	}
}

// spec: 4.7.3 (adapter events on CH-ADAPTEREVENTS)
// diagnosis: the envelope the corrected comment annotates stopped carrying
//
//	an opaque JSON payload, so the taxonomy the comment points at is
//	no longer the taxonomy the wire carries. A consumer decoding an
//	event by its `type` discriminator breaks.
func TestAdapterEventEnvelopeStaysOpaqueJSON(t *testing.T) {
	t.Parallel()

	for _, name := range adapterEventSet {
		payload, err := json.Marshal(map[string]string{"type": name})
		if err != nil {
			t.Fatalf("marshal %s envelope: %v", name, err)
		}
		req := &adapterv1.AdapterEventsRequest{EnvelopeJson: payload}
		resp := &adapterv1.AdapterEventsResponse{EnvelopeJson: payload}
		for label, got := range map[string][]byte{
			"AdapterEventsRequest":  req.GetEnvelopeJson(),
			"AdapterEventsResponse": resp.GetEnvelopeJson(),
		} {
			var decoded map[string]string
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Fatalf("%s: decode %s envelope: %v", label, name, err)
			}
			if decoded["type"] != name {
				t.Errorf("%s: envelope type = %q, want %q", label, decoded["type"], name)
			}
		}
	}

	// The empty envelope is the boundary case: the field is opaque
	// bytes, so the message carries it without a proto-level error and
	// the decode failure surfaces at the consumer.
	empty := &adapterv1.AdapterEventsRequest{}
	if len(empty.GetEnvelopeJson()) != 0 {
		t.Errorf("unset envelope_json = %q, want empty", empty.GetEnvelopeJson())
	}
	var decoded map[string]string
	if err := json.Unmarshal(empty.GetEnvelopeJson(), &decoded); err == nil {
		t.Error("decoding an empty envelope must fail at the consumer")
	}
}
