// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// propagationHistogramTests are the in-package test entries that drive
// the input path of the §27.8
// lenny_playground_session_revocation_propagation_seconds histogram: the
// pub/sub payload round-trip that carries the origin replica id and the
// publish instant, the peer message that warms the negative cache and
// records one pubsub_delivered sample, the self-published message that
// warms the cache without being sampled, the unrecognised channel that
// is ignored entirely, the clock-skew case that clamps a negative
// latency to zero, and the cross-replica case that holds every emitted
// outcome label inside the domain the metrics table declares.
// `lenny-test validate-maps` only walks
// tests/tier2_component and above for orphaned test files, so an
// in-package test under pkg/ can encode a §27.8 guarantee and still be
// invisible to the spec map. This list keeps the §27.8 entry honest
// about where the histogram's observations are produced, rather than
// leaving the entry to imply that only the metric catalog and the
// counter-increment cases cover the metric table.
var propagationHistogramTests = []string{
	"pkg/gateway/mcpfabric/playground/revocation_propagation_test.go::TestEncodeParseRevocationMsgRoundTrip_spec_27_8_241",
	"pkg/gateway/mcpfabric/playground/revocation_propagation_test.go::TestHandleRevocationMessagePeerWarmsCacheAndSamples_spec_27_8_241",
	"pkg/gateway/mcpfabric/playground/revocation_propagation_test.go::TestHandleRevocationMessageSelfPublishNotSampled",
	"pkg/gateway/mcpfabric/playground/revocation_propagation_test.go::TestHandleRevocationMessageBadChannelIgnored",
	"pkg/gateway/mcpfabric/playground/revocation_propagation_test.go::TestHandleRevocationMessageNegativeLatencyClamped",
	"pkg/gateway/mcpfabric/playground/revocation_propagation_test.go::TestSubscribeAllRevocationsWarmsCacheForArbitraryTenant_spec_27_6_204",
	"pkg/gateway/mcpfabric/playground/revocation_propagation_outcome_test.go::TestRevocationPropagationOutcomesStayWithinSpec278Domain_spec_27_8_244",
}

// spec: §27.8 (Metrics) — "`lenny_playground_session_revocation_propagation_seconds`
//
//	| Histogram | `outcome` | End-to-end propagation latency from when a
//	revocation is written on the originating replica to when peer
//	replicas observe it on their auth hot path (authoritative Redis
//	`GET` and/or pub/sub-warmed negative cache). `outcome ∈
//	{pubsub_delivered, redis_authoritative, resubscribe}`. P99 alert
//	threshold is the 500 ms logout propagation SLO defined in §27.3.1."
//
// diagnosis: The tests/spec-map.json §27.8 entry no longer references
//
//	the on-disk tests that produce the propagation histogram's samples.
//	Either the entry lost a reference or a test was renamed or deleted.
//	A reviewer reading the §27.8 entry will conclude the histogram's
//	input path is unexercised and either duplicate that coverage or
//	treat a regression in the encode/parse, peer-sampling,
//	self-publish, bad-channel, or negative-latency-clamp behavior as
//	unguarded. Restore the reference in the §27.8 `tests` list, or
//	update this list when a test is renamed.
func TestSpecMap278ReferencesRevocationPropagationTests(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	referenced := map[string]bool{}
	for _, entry := range readSpecMapTestEntries(t)["27.8"] {
		referenced[entry] = true
	}

	missing := []string{}
	absent := []string{}
	for _, entry := range propagationHistogramTests {
		path, name, ok := strings.Cut(entry, "::")
		if !ok {
			t.Fatalf("guard entry %q is not in file::TestName form", entry)
		}
		body, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			absent = append(absent, entry)
			continue
		}
		declared := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(`)
		if !declared.Match(body) {
			absent = append(absent, entry)
			continue
		}
		if !referenced[entry] {
			missing = append(missing, entry)
		}
	}

	sort.Strings(absent)
	sort.Strings(missing)
	if len(absent) > 0 {
		t.Errorf("test(s) named by this guard are absent from disk: %v; a rename must be reflected both here and in the tests/spec-map.json §27.8 entry", absent)
	}
	if len(missing) > 0 {
		t.Errorf("the tests/spec-map.json §27.8 entry does not reference %v; each produces a sample of the §27.8 revocation-propagation histogram and must appear in the section's tests list", missing)
	}
}

// readSpecMapTestEntries returns, per section id, the verbatim `tests`
// list recorded in tests/spec-map.json, keeping the `::TestName`
// selector so a guard can assert a specific test function rather than
// only the file that holds it.
func readSpecMapTestEntries(t *testing.T) map[string][]string {
	t.Helper()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "tests", "spec-map.json"))
	if err != nil {
		t.Fatalf("read spec-map.json: %v", err)
	}
	var doc struct {
		Sections map[string]struct {
			Tests []string `json:"tests"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse spec-map.json: %v", err)
	}
	out := map[string][]string{}
	for id, sec := range doc.Sections {
		out[id] = sec.Tests
	}
	return out
}
