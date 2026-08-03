// SPDX-License-Identifier: MIT

// Tier-11 documentation check reconciling the derived cells of §28.3's
// registers with the sections they are derived from. Three cells of
// §28.3 are stated against the current spec/ text rather than against
// the channel inventory the provenance column cites, because the
// inventory predates the specification statement it summarizes:
// `REG-PODSTATE`'s writer set, `REG-CLAIM`'s writer set, and
// `LNK-INTERREPLICA`'s transport, dial direction, and lifetime. Each is
// held here against the sentence in §12.6, §4.6.3, or §7.2 it was read
// from, pinned as a byte-exact literal, so an edit to either side fails
// the case. These tests are NOT under a build tag because they exercise
// the repository state directly — no external infrastructure required.
//
// spec: §28.3, §12.6, §4.6.3, §7.2

package tier11_docs_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The register tables of §28.3 this check reads, by heading.
//
// spec: §28.3
const (
	linkRegisterHeading          = "Link register"
	registerEntryRegisterHeading = "Register-entry register"
)

// The register entries and the link entry whose derived cells this check
// reconciles with the section specifying the store or the connection.
//
// spec: §28.3
const (
	podStateRegisterEntry = "REG-PODSTATE"
	claimRegisterEntry    = "REG-CLAIM"
	interReplicaLink      = "LNK-INTERREPLICA"
)

// The sentences of §12.6, §4.6.3, and §7.2 the three derived cells are
// read from, byte-exact. A specification edit that rewords one of them
// fails the case that pins it, so the derivation is re-checked by hand
// rather than silently outliving its source.
//
// spec: §12.6, §4.6.3, §7.2
const (
	podStateGatewayWrittenSentence = "The `sessions_served` and `scrub_failure_count` columns are the exception to " +
		"the WarmPoolController-maintained mirror: they are gateway-written recycle counters, incremented at each " +
		"session release (`ReportSessionScrub`) and on each failed whole-pod scrub (`ReportPodScrub`) respectively"

	claimOwnershipRowSentence = "Created by the gateway at pod acquisition; binding state written via the status " +
		"subresource; deleted by the gateway at hold expiry or by the WarmPoolController at pod termination and orphan GC"

	forwardMessageTransportSentence = "The coordinator forwarding mechanism reuses the same internal gRPC " +
		"`ForwardMessage` RPC used for all cross-replica message routing"

	forwardingReplicaSentence = "When a `delivery: immediate` message lands on a non-coordinator replica, that " +
		"replica forwards the message to the session's coordinator (identified via the coordination lease in " +
		"Redis/Postgres)."
)

// registerTable is one table of §28.3, indexed by its header labels and
// keyed by the identifier its first column carries. Reading a cell
// through the header rather than through a fixed offset means a column
// inserted or reordered fails the lookup by name instead of silently
// returning a neighbouring cell.
//
// spec: §28.3
type registerTable struct {
	heading string
	columns map[string]int
	rows    map[string][]string
}

// tableRowCells splits one markdown table row into its trimmed cells.
func tableRowCells(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	cells := strings.Split(trimmed, "|")
	for i, cell := range cells {
		cells[i] = strings.TrimSpace(cell)
	}
	return cells
}

// isSeparatorRow reports whether cells are the alignment row markdown
// writes between a table's header and its body.
func isSeparatorRow(cells []string) bool {
	for _, cell := range cells {
		if strings.Trim(cell, ":- ") != "" {
			return false
		}
	}
	return len(cells) > 0
}

// readRegisterTable returns the table published under the given heading
// of the communication-channels section. The scan runs from that heading
// to the next heading of any level, so a table under a later heading is
// never read as part of this one.
//
// spec: §28.3
func readRegisterTable(section, heading string) (registerTable, error) {
	table := registerTable{heading: heading, columns: map[string]int{}, rows: map[string][]string{}}
	inSection := false
	for _, line := range strings.Split(section, "\n") {
		if m := atxHeading.FindStringSubmatch(line); m != nil {
			if inSection {
				break
			}
			inSection = strings.TrimSpace(m[2]) == heading
			continue
		}
		if !inSection || !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := tableRowCells(line)
		if isSeparatorRow(cells) {
			continue
		}
		if len(table.columns) == 0 {
			for i, label := range cells {
				table.columns[label] = i
			}
			continue
		}
		table.rows[strings.Trim(cells[0], "`")] = cells
	}
	if !inSection {
		return registerTable{}, fmt.Errorf("the section carries no heading %q", heading)
	}
	if len(table.rows) == 0 {
		return registerTable{}, fmt.Errorf("the table under %q carries no entry row", heading)
	}
	return table, nil
}

// cell returns the cell one entry of the table carries in the named
// column.
//
// spec: §28.3
func (r registerTable) cell(identifier, column string) (string, error) {
	row, ok := r.rows[identifier]
	if !ok {
		return "", fmt.Errorf("the %s table carries no row for %s", r.heading, identifier)
	}
	index, ok := r.columns[column]
	if !ok {
		return "", fmt.Errorf("the %s table carries no column %q", r.heading, column)
	}
	if index >= len(row) {
		return "", fmt.Errorf("the %s row for %s carries %d cell(s) and the %q column is cell %d",
			r.heading, identifier, len(row), column, index+1)
	}
	return row[index], nil
}

// requireCellContains holds one register cell to the substrings the
// section it is derived from fixes.
//
// spec: §28.3
func requireCellContains(t *testing.T, table registerTable, identifier, column string, want []string) {
	t.Helper()
	got, err := table.cell(identifier, column)
	if err != nil {
		t.Fatalf("read the %s cell of %s: %v", column, identifier, err)
	}
	for _, substring := range want {
		if !strings.Contains(got, substring) {
			t.Errorf("§28.3's %s cell of %s is %q and states no %q", column, identifier, got, substring)
		}
	}
}

// readChannelsRegisters returns the link register and the register-entry
// register of the landed section.
//
// spec: §28.3
func readChannelsRegisters(t *testing.T) (links, entries registerTable) {
	t.Helper()
	section := readChannelsSection(t)

	links, err := readRegisterTable(section, linkRegisterHeading)
	if err != nil {
		t.Fatalf("read the link register from %s: %v", channelsSpecFile, err)
	}
	entries, err = readRegisterTable(section, registerEntryRegisterHeading)
	if err != nil {
		t.Fatalf("read the register-entry register from %s: %v", channelsSpecFile, err)
	}
	return links, entries
}

// diagnosis: a failure means spec/28's register and the store's or the
// connection's own section disagree on who writes it or on how it is
// carried. §28.3 derives three of its cells from the current spec/ text
// rather than from the channel inventory its provenance column cites: a
// writer set that drops the gateway replicas from `REG-PODSTATE` or the
// WarmPoolController leader from `REG-CLAIM` describes a store nobody
// writes the way §12.6 and §4.6.3 state it is written, and a
// `LNK-INTERREPLICA` row that states another transport, dial direction,
// or lifetime describes a connection §7.2 does not specify.
//
// spec: §28.3, §12.6, §4.6.3, §7.2
func TestSection28RegisterWritersMatchTheSpec_spec_28_3(t *testing.T) {
	links, entries := readChannelsRegisters(t)

	t.Run("pod state writer set", func(t *testing.T) { assertPodStateWriterSetMatchesStorage(t, entries) })
	t.Run("claim writer set", func(t *testing.T) { assertClaimWriterSetMatchesOwnership(t, entries) })
	t.Run("inter-replica link", func(t *testing.T) { assertInterReplicaLinkMatchesSessionLifecycle(t, links) })
}

// assertPodStateWriterSetMatchesStorage holds `REG-PODSTATE`'s writer
// set to §12.6's statement of who writes the mirror and its two
// gateway-written recycle counters.
//
// spec: §28.3, §12.6
func assertPodStateWriterSetMatchesStorage(t *testing.T, entries registerTable) {
	t.Helper()

	requireCellContains(t, entries, podStateRegisterEntry, "Writer set", []string{
		"WarmPoolController",
		"gateway replicas",
		"`sessions_served`",
		"`scrub_failure_count`",
	})

	storage := specSection(t, filepath.Join(repoRoot(t), "spec", "12_storage-architecture.md"), "### 12.6 ")
	requireAllContain(t, "§12.6 agent_pod_state writers", storage, []string{podStateGatewayWrittenSentence})
}

// assertClaimWriterSetMatchesOwnership holds `REG-CLAIM`'s writer set to
// §4.6.3's ownership row, which assigns the deletes at pod termination
// and orphan garbage collection to the WarmPoolController and every
// other write to the gateway.
//
// spec: §28.3, §4.6.3
func assertClaimWriterSetMatchesOwnership(t *testing.T, entries registerTable) {
	t.Helper()

	requireCellContains(t, entries, claimRegisterEntry, "Writer set", []string{
		"Gateway replicas",
		"WarmPoolController leader",
		"pod termination and orphan garbage collection",
	})

	components := specSection(t, filepath.Join(repoRoot(t), "spec", "04_system-components.md"), "#### 4.6.3 ")
	requireAllContain(t, "§4.6.3 SandboxClaim ownership row", components, []string{claimOwnershipRowSentence})
}

// assertInterReplicaLinkMatchesSessionLifecycle holds
// `LNK-INTERREPLICA`'s transport, dial direction, and lifetime cells to
// the two §7.2 sentences they are read from: the one naming the internal
// gRPC `ForwardMessage` RPC as the carrier of all cross-replica message
// routing, and the one stating that the replica the message lands on
// forwards it to the session's coordinator.
//
// spec: §28.3, §7.2
func assertInterReplicaLinkMatchesSessionLifecycle(t *testing.T, links registerTable) {
	t.Helper()

	transport, err := links.cell(interReplicaLink, "Transport")
	if err != nil {
		t.Fatalf("read the transport cell of %s: %v", interReplicaLink, err)
	}
	if transport != "gRPC" {
		t.Errorf("§28.3's transport cell of %s is %q, and §7.2 specifies gRPC", interReplicaLink, transport)
	}
	requireCellContains(t, links, interReplicaLink, "Dial direction", []string{"Forwarding replica"})
	requireCellContains(t, links, interReplicaLink, "Lifetime", []string{"coordinating replica"})

	sessions := specSection(t, filepath.Join(repoRoot(t), "spec", "07_session-lifecycle.md"), "### 7.2 ")
	requireAllContain(t, "§7.2 cross-replica message routing", sessions, []string{
		forwardMessageTransportSentence,
		forwardingReplicaSentence,
	})
}
