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

// The gateway ↔ adapter gRPC contract is a shipped wire artifact a runtime
// author reads directly (§28.7 lists schemas/lenny-adapter.proto among the
// wire-contract artifacts). Three of its comments describe intra-pod
// behavior: the observed-integration-level classification, the window the
// adapter waits for the runtime's first lifecycle handshake, and the
// interrupt status returned when the deadline elapses. The material each
// one describes is stated by the §28.5.3 contract cards (CH-RUNTIMEOPS for
// the runtime-operations channel, CH-MCP-PLATFORM for the intra-pod
// platform MCP server), so each comment cites the card that owns it rather
// than §4.7, which no longer states any of it.
//
// This gate reads the comment text because that is the surface the runtime
// author reads; a descriptor carries no comments.

// adapterProtoRel is the shipped gRPC contract this gate checks.
const adapterProtoRel = "schemas/lenny-adapter.proto"

// retiredIntraPodPointer is the section pointer these three comments must
// no longer carry. §4.7 keeps the adapter manifest and the assignment
// sequence, so the token is legitimate elsewhere in the file and the gate
// is scoped to the three comments below.
const retiredIntraPodPointer = "§4.7"

// intraPodPointerSite is one comment on the shipped contract whose subject
// matter is stated by a §28.5.3 card. anchor is a substring unique to the
// comment, and cards are the tokens the comment must carry.
type intraPodPointerSite struct {
	name   string
	anchor string
	cards  []string
}

// intraPodPointerSites are the three comments the intra-pod material moved
// out from under.
var intraPodPointerSites = []intraPodPointerSite{
	{
		name:   "GetObservedIntegrationLevel RPC",
		anchor: "GetObservedIntegrationLevel reports",
		cards:  []string{"§28.5.3", "CH-RUNTIMEOPS", "CH-MCP-PLATFORM"},
	},
	{
		name:   "GetObservedIntegrationLevelRequest.wait_ms",
		anchor: "wait_ms bounds how long",
		cards:  []string{"§28.5.3", "CH-RUNTIMEOPS"},
	},
	{
		name:   "InterruptResponse.Status STATUS_INTERRUPT_TIMEOUT",
		anchor: "STATUS_INTERRUPT_TIMEOUT = 2;",
		cards:  []string{"§28.5.3", "CH-RUNTIMEOPS"},
	},
}

// protoCommentBlock returns the comment text surrounding the first line
// containing anchor. A leading comment block is returned whole; a trailing
// comment on a code line is returned from its `//` onward. The second
// result is false when no line carries the anchor.
func protoCommentBlock(src, anchor string) (string, bool) {
	lines := strings.Split(src, "\n")
	idx := -1
	for i, line := range lines {
		if strings.Contains(line, anchor) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", false
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[idx]), "//") {
		// Trailing comment on a code line.
		at := strings.Index(lines[idx], "//")
		if at < 0 {
			return "", false
		}
		return lines[idx][at:], true
	}
	start := idx
	for start > 0 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "//") {
		start--
	}
	end := idx
	for end+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[end+1]), "//") {
		end++
	}
	return strings.Join(lines[start:end+1], "\n"), true
}

// intraPodPointerViolations reports every way the proto source fails the
// gate: a comment that is missing, one that omits a card it must name, and
// one that still attributes its subject to the retired section pointer.
// The result is sorted so a caller can compare it deterministically.
func intraPodPointerViolations(src string) []string {
	var out []string
	for _, site := range intraPodPointerSites {
		block, ok := protoCommentBlock(src, site.anchor)
		if !ok {
			out = append(out, site.name+": comment not found")
			continue
		}
		for _, card := range site.cards {
			if !strings.Contains(block, card) {
				out = append(out, site.name+": missing "+card)
			}
		}
		if strings.Contains(block, retiredIntraPodPointer) {
			out = append(out, site.name+": cites "+retiredIntraPodPointer)
		}
	}
	sort.Strings(out)
	return out
}

// spec: 28.5.3 (intra-pod contract cards), 28.7 (wire-contract artifact
//
//	register), 5.1 (integration levels)
//
// diagnosis: a comment on the shipped gateway ↔ adapter gRPC contract
//
//	sends a runtime author to §4.7 for intra-pod material that
//	§28.5.3 states instead. The reader looking for the
//	lifecycle_capabilities/lifecycle_support exchange, the platform
//	MCP server, or the INTERRUPT_TIMEOUT status finds a section
//	that no longer carries it.
func TestAdapterProtoIntraPodCommentsCiteChannelCards(t *testing.T) {
	t.Parallel()

	src := readAdapterProto(t)
	if got := intraPodPointerViolations(src); len(got) > 0 {
		t.Errorf("%s intra-pod comments: %v", adapterProtoRel, got)
	}
}

// spec: 4.7 (runtime adapter), 28.5.3 (intra-pod contract cards)
// diagnosis: the STATUS_BUSY comment lost its §4.7 pointer. The
//
//	operation-lock rejection it cites is stated in §4.7 and did not
//	move to §28.5.3, so rewriting it alongside its neighbour sends
//	the reader to a card that does not state it.
func TestAdapterProtoInterruptBusyKeepsRuntimeAdapterPointer(t *testing.T) {
	t.Parallel()

	src := readAdapterProto(t)
	block, ok := protoCommentBlock(src, "STATUS_BUSY = 3;")
	if !ok {
		t.Fatalf("%s: STATUS_BUSY comment not found", adapterProtoRel)
	}
	if !strings.Contains(block, retiredIntraPodPointer) {
		t.Errorf("STATUS_BUSY comment must keep its %s pointer, got: %s", retiredIntraPodPointer, block)
	}
}

// spec: 28.5.3 (intra-pod contract cards)
// diagnosis: the gate no longer detects a stale intra-pod pointer, so the
//
//	shipped contract can regress to §4.7 with the tier green.
func TestIntraPodPointerGateDetectsStaleAndMissingComments(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	for _, tc := range []struct {
		name    string
		fixture string
		want    []string
	}{
		{
			name:    "stale pointers",
			fixture: "stale-pointers.proto.txt",
			want: []string{
				"GetObservedIntegrationLevel RPC: cites §4.7",
				"GetObservedIntegrationLevel RPC: missing CH-MCP-PLATFORM",
				"GetObservedIntegrationLevel RPC: missing CH-RUNTIMEOPS",
				"GetObservedIntegrationLevel RPC: missing §28.5.3",
				"GetObservedIntegrationLevelRequest.wait_ms: cites §4.7",
				"GetObservedIntegrationLevelRequest.wait_ms: missing §28.5.3",
				"InterruptResponse.Status STATUS_INTERRUPT_TIMEOUT: cites §4.7",
				"InterruptResponse.Status STATUS_INTERRUPT_TIMEOUT: missing CH-RUNTIMEOPS",
				"InterruptResponse.Status STATUS_INTERRUPT_TIMEOUT: missing §28.5.3",
			},
		},
		{
			name:    "empty source",
			fixture: "empty.proto.txt",
			want: []string{
				"GetObservedIntegrationLevel RPC: comment not found",
				"GetObservedIntegrationLevelRequest.wait_ms: comment not found",
				"InterruptResponse.Status STATUS_INTERRUPT_TIMEOUT: comment not found",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(root, "tests/tier0_static/testdata/adapter-proto-pointers", tc.fixture)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			got := intraPodPointerViolations(string(data))
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("violations for %s:\ngot:  %v\nwant: %v", tc.fixture, got, tc.want)
			}
		})
	}
}

// readAdapterProto returns the shipped gRPC contract source.
func readAdapterProto(t *testing.T) string {
	t.Helper()
	path := filepath.Join(schematest.RepoRoot(t), adapterProtoRel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", adapterProtoRel, err)
	}
	return string(data)
}
