// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The adapter→gateway event stream in the shipped gRPC contract carries two
// hand-authored comments that state its event taxonomy: the doc comment on
// the AdapterEvents RPC and the comment over the request and response
// envelope messages. The events that stream carries are the ones §4.7.3
// tables (RATE_LIMITED, AUTH_EXPIRED, PROVIDER_UNAVAILABLE, LEASE_REJECTED,
// AdapterTerminating, FINAL_USAGE_REPORT, and CheckpointBarrierAck).
//
// The checkpoint, interrupt, credential-acknowledgement, and
// deadline-approaching frames are intra-pod runtime-operations frames on the
// CH-RUNTIMEOPS socket, so neither comment may credit this stream with them,
// and neither may send the reader to §15.4 or to
// schemas/lenny-adapter-jsonl.schema.json, which schematizes the binary
// stdin/stdout frames and no adapter→gateway event.
//
// This gate reads the comment text because that is the surface a runtime
// author reads; a descriptor carries no comments.

// eventTaxonomyOwner is the specification statement of the adapter→gateway
// event taxonomy that both comments must name.
const eventTaxonomyOwner = "spec/04_system-components.md"

// eventTaxonomySection is the section of that file which tables the events.
const eventTaxonomySection = "§4.7.3"

// eventTaxonomySite is one comment on the shipped contract that states the
// adapter→gateway event taxonomy. anchor is a substring unique to the
// comment, required are tokens the comment must carry, and retired are
// tokens it must not.
type eventTaxonomySite struct {
	name     string
	anchor   string
	required []string
	retired  []string
}

// retiredEventTaxonomyTokens are the intra-pod frames and the two wrong
// taxonomy owners neither comment may name.
var retiredEventTaxonomyTokens = []string{
	"checkpoint_ready",
	"credentials_acknowledged",
	"deadline_approaching",
	"interrupt_acknowledged",
	"lenny-adapter-jsonl.schema.json",
	"§15.4",
}

// eventTaxonomySites are the two comments on the adapter→gateway stream.
var eventTaxonomySites = []eventTaxonomySite{
	{
		name:   "AdapterEvents RPC",
		anchor: "AdapterEvents is ",
		required: []string{
			"AUTH_EXPIRED",
			"AdapterTerminating",
			"CheckpointBarrierAck",
			"FINAL_USAGE_REPORT",
			"LEASE_REJECTED",
			"PROVIDER_UNAVAILABLE",
			"RATE_LIMITED",
			eventTaxonomyOwner,
			eventTaxonomySection,
		},
		retired: retiredEventTaxonomyTokens,
	},
	{
		name:   "AdapterEventsRequest/AdapterEventsResponse envelope",
		anchor: "event envelope.",
		required: []string{
			eventTaxonomyOwner,
			eventTaxonomySection,
		},
		retired: retiredEventTaxonomyTokens,
	},
}

// eventTaxonomyViolations reports every way the proto source fails the gate:
// a comment that is missing, one that omits an event or the taxonomy's owner,
// and one that still names an intra-pod frame or a wrong owner. The result is
// sorted so a caller can compare it deterministically.
func eventTaxonomyViolations(src string) []string {
	var out []string
	for _, site := range eventTaxonomySites {
		block, ok := protoCommentBlock(src, site.anchor)
		if !ok {
			out = append(out, site.name+": comment not found")
			continue
		}
		for _, want := range site.required {
			if !strings.Contains(block, want) {
				out = append(out, site.name+": missing "+want)
			}
		}
		for _, unwanted := range site.retired {
			if strings.Contains(block, unwanted) {
				out = append(out, site.name+": names "+unwanted)
			}
		}
	}
	sort.Strings(out)
	return out
}

// spec: 4.7.3 (adapter events on CH-ADAPTEREVENTS), 28.5.3 (intra-pod
//
//	contract cards), 28.7 (wire-contract artifact register)
//
// diagnosis: a comment on the shipped gateway ↔ adapter gRPC contract states
//
//	the wrong event set for the adapter→gateway stream, or sends the
//	reader somewhere other than the §4.7.3 events table for the
//	taxonomy. A runtime author implementing the stream either
//	implements intra-pod frames on it or cannot find the events it
//	actually carries.
func TestAdapterProtoEventCommentsStateTheAdapterEventSet(t *testing.T) {
	t.Parallel()

	src := readAdapterProto(t)
	if got := eventTaxonomyViolations(src); len(got) > 0 {
		t.Errorf("%s adapter→gateway event comments: %v", adapterProtoRel, got)
	}
}

// spec: 4.7.3 (adapter events on CH-ADAPTEREVENTS)
// diagnosis: the gate no longer detects a comment that credits the
//
//	adapter→gateway stream with the intra-pod frames or names the
//	wrong taxonomy owner, so the shipped contract can regress with
//	the tier green.
func TestEventTaxonomyGateDetectsStaleAndMissingComments(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	for _, tc := range []struct {
		name    string
		fixture string
		want    []string
	}{
		{
			name:    "stale taxonomy",
			fixture: "stale-taxonomy.proto.txt",
			want: []string{
				"AdapterEvents RPC: missing AUTH_EXPIRED",
				"AdapterEvents RPC: missing AdapterTerminating",
				"AdapterEvents RPC: missing CheckpointBarrierAck",
				"AdapterEvents RPC: missing FINAL_USAGE_REPORT",
				"AdapterEvents RPC: missing LEASE_REJECTED",
				"AdapterEvents RPC: missing PROVIDER_UNAVAILABLE",
				"AdapterEvents RPC: missing RATE_LIMITED",
				"AdapterEvents RPC: missing spec/04_system-components.md",
				"AdapterEvents RPC: missing §4.7.3",
				"AdapterEvents RPC: names checkpoint_ready",
				"AdapterEvents RPC: names credentials_acknowledged",
				"AdapterEvents RPC: names deadline_approaching",
				"AdapterEvents RPC: names interrupt_acknowledged",
				"AdapterEvents RPC: names §15.4",
				"AdapterEventsRequest/AdapterEventsResponse envelope: missing spec/04_system-components.md",
				"AdapterEventsRequest/AdapterEventsResponse envelope: missing §4.7.3",
				"AdapterEventsRequest/AdapterEventsResponse envelope: names lenny-adapter-jsonl.schema.json",
			},
		},
		{
			name:    "empty source",
			fixture: "empty.proto.txt",
			want: []string{
				"AdapterEvents RPC: comment not found",
				"AdapterEventsRequest/AdapterEventsResponse envelope: comment not found",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(root, "tests/tier0_static/testdata/adapter-proto-event-taxonomy", tc.fixture)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			got := eventTaxonomyViolations(string(data))
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("violations for %s:\ngot:  %v\nwant: %v", tc.fixture, got, tc.want)
			}
		})
	}
}
