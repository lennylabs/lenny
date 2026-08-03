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
// §28.4's own statement of where the claim register lives is pinned the
// same way, as a byte-exact sentence.
//
// spec: §28.3, §28.4, §12.6, §4.6.3, §7.2

package tier11_docs_test

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The register tables of §28.3 this check reads, by heading.
//
// spec: §28.3
const (
	linkRegisterHeading          = "Link register"
	channelRegisterHeading       = "Channel register"
	registerEntryRegisterHeading = "Register-entry register"
)

// noLinkCell is what a channel row's Link column reads when the
// connection carrying the channel is referred to by that row alone.
//
// spec: §28.3
const noLinkCell = "None"

// linkTokenExpr matches a link identifier standing in a cell, which is
// the form a channel row names the connection its calls are forwarded
// over in its message-vocabulary cell.
//
// spec: §28.3
var linkTokenExpr = regexp.MustCompile(`LNK-[A-Z0-9]+(?:-[A-Z0-9]+)*`)

// The register entries and the link entry whose derived cells this check
// reconciles with the section specifying the store or the connection.
//
// spec: §28.3
const (
	podStateRegisterEntry = "REG-PODSTATE"
	claimRegisterEntry    = "REG-CLAIM"
	interReplicaLink      = "LNK-INTERREPLICA"
)

// linkReferenceColumns are the two channel-register columns a channel row
// refers to the link register from: the Link column names the connection
// the channel is carried on, and the message-vocabulary column names the
// connection a channel's calls are forwarded over.
//
// spec: §28.3
var linkReferenceColumns = []string{"Link", "Message vocabulary"}

// minimumReferringChannelRows is the threshold §28.3 sets for declaring a
// link entry: the link register declares a connection more than one
// channel row refers to, and a connection referred to by one row alone
// reads `None` in that row instead, so it takes an entry at the point a
// second channel row refers to it.
//
// spec: §28.3
const minimumReferringChannelRows = 2

// unreferencedLinkEntries are the link entries §28.3 declares that no
// channel row refers to, each with the reason the section states for it.
// The exemption is closed in both directions: an entry listed here that
// gains a referring channel row is reported as stale, so the list cannot
// outlive the gap it records.
//
// spec: §28.3
var unreferencedLinkEntries = map[string]string{
	interReplicaLink: "the specification states the connection and the cross-replica message routing it carries " +
		"is not implemented, which §28.3 records as a claim-register row rather than as an absent transport",
}

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

// The two writer-set cells of §28.3, byte-exact and whole. Each writer is
// pinned together with what it writes, so a cell that swaps the two roles,
// widens one writer's set of columns, or names a third writer fails the
// case even though every individual name it used to carry is still
// present. §12.6 states a split-writer rule for `agent_pod_state` (the
// WarmPoolController maintains the mirror and exactly `sessions_served`
// and `scrub_failure_count` are gateway-written), and §4.6.3 assigns the
// create, the status-subresource writes, and the hold-expiry delete to the
// gateway and only the pod-termination and orphan-GC deletes to the
// WarmPoolController leader; an unattributed name check cannot tell either
// rule from its inverse.
//
// spec: §28.3, §12.6, §4.6.3
const (
	podStateWriterSetCell = "WarmPoolController for the mirrored `Sandbox` status columns, and gateway replicas " +
		"for `sessions_served` and `scrub_failure_count`"

	claimWriterSetCell = "Gateway replicas for the create, the status-subresource binding-state writes, and the " +
		"hold-expiry delete, and the WarmPoolController leader for the deletes at pod termination and orphan " +
		"garbage collection"
)

// The heading of the claim-register subsection and the sentence it opens
// with, byte-exact once line wrapping is collapsed. §28.4 is the only
// place the specification states where the claim register lives, and the
// deferral case it records for the interval before the file lands cites
// this sentence as where the location is stated, so a rewording that
// drops the path leaves both without a source.
//
// spec: §28.4
const (
	claimRegisterHeading = "### 28.4 "

	claimRegisterLocationSentence = "Every normative statement this section makes about a mechanism carries " +
		"a row in the claim register at `tests/claim-map.json`, with a status drawn from a closed set."
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

// withCell returns a copy of the table carrying the given cell in place of
// the one the specification landed, so a case can state what the check
// must reject without editing spec/.
//
// spec: §28.3
func (r registerTable) withCell(identifier, column, cell string) registerTable {
	copied := registerTable{heading: r.heading, columns: r.columns, rows: map[string][]string{}}
	for key, row := range r.rows {
		copied.rows[key] = append([]string(nil), row...)
	}
	if row, ok := copied.rows[identifier]; ok {
		if index, ok := r.columns[column]; ok && index < len(row) {
			row[index] = cell
		}
	}
	return copied
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

// requireCellEquals holds one register cell to the whole literal the
// section it is derived from fixes. Equality is the check a derived cell
// carrying an attribution needs: a containment check passes on a cell that
// keeps every name it used to carry while reassigning what each one
// writes.
//
// spec: §28.3
func requireCellEquals(t *testing.T, table registerTable, identifier, column, want string) {
	t.Helper()
	if err := table.cellDivergence(identifier, column, want); err != nil {
		t.Error(err)
	}
}

// cellDivergence returns the divergence between one register cell and the
// literal the section it is derived from fixes, or nil when the two agree.
// The comparison is a separate function so a case can assert that a
// mutated cell is rejected without failing itself.
//
// spec: §28.3
func (r registerTable) cellDivergence(identifier, column, want string) error {
	got, err := r.cell(identifier, column)
	if err != nil {
		return fmt.Errorf("read the %s cell of %s: %w", column, identifier, err)
	}
	if got != want {
		return fmt.Errorf("§28.3's %s cell of %s is %q and the section it is derived from states %q",
			column, identifier, got, want)
	}
	return nil
}

// readChannelsRegisters returns the three registers of the landed
// section.
//
// spec: §28.3
func readChannelsRegisters(t *testing.T) (links, channels, entries registerTable) {
	t.Helper()
	section := readChannelsSection(t)

	links, err := readRegisterTable(section, linkRegisterHeading)
	if err != nil {
		t.Fatalf("read the link register from %s: %v", channelsSpecFile, err)
	}
	channels, err = readRegisterTable(section, channelRegisterHeading)
	if err != nil {
		t.Fatalf("read the channel register from %s: %v", channelsSpecFile, err)
	}
	entries, err = readRegisterTable(section, registerEntryRegisterHeading)
	if err != nil {
		t.Fatalf("read the register-entry register from %s: %v", channelsSpecFile, err)
	}
	return links, channels, entries
}

// diagnosis: a failure means spec/28's register and the store's or the
// connection's own section disagree on who writes it or on how it is
// carried. §28.3 derives three of its cells from the current spec/ text
// rather than from the channel inventory its provenance column cites: a
// writer set that drops a writer from `REG-PODSTATE` or `REG-CLAIM`,
// reassigns what one of them writes, or widens one writer's set of columns
// describes a store nobody writes the way §12.6 and §4.6.3 state it is
// written, and a
// `LNK-INTERREPLICA` row that states another transport, dial direction,
// or lifetime describes a connection §7.2 does not specify. A failure of
// the link-reference case means a channel row names a connection the
// link register does not declare, which is a reference §28.3 states as
// resolving and the declaration index reads as a declaration of its own
// instead. A failure of the declaration-threshold case means the link
// register declares a connection fewer than two channel rows refer to,
// which §28.3 states must read `None` in the one row that refers to it,
// or an entry listed as referred to by no channel row has gained one.
//
// spec: §28.3, §12.6, §4.6.3, §7.2
func TestSection28RegisterWritersMatchTheSpec_spec_28_3(t *testing.T) {
	links, channels, entries := readChannelsRegisters(t)

	t.Run("pod state writer set", func(t *testing.T) { assertPodStateWriterSetMatchesStorage(t, entries) })
	t.Run("claim writer set", func(t *testing.T) { assertClaimWriterSetMatchesOwnership(t, entries) })
	t.Run("inter-replica link", func(t *testing.T) { assertInterReplicaLinkMatchesSessionLifecycle(t, links) })
	t.Run("reassigned writer set", func(t *testing.T) { assertReassignedWriterSetIsRejected(t, entries) })
	t.Run("link references resolve", func(t *testing.T) { assertChannelLinkReferencesResolve(t, links, channels) })
	t.Run("dangling link reference", func(t *testing.T) { assertDanglingLinkReferenceIsRejected(t, links, channels) })
	t.Run("link declaration threshold", func(t *testing.T) { assertLinkDeclarationThresholdHolds(t, links, channels) })
	t.Run("single-reference link entry", func(t *testing.T) { assertSingleReferenceLinkEntryIsRejected(t, links, channels) })
}

// assertChannelLinkReferencesResolve holds every reference a channel row
// makes to the link register to a row of that register. §28.3 states the
// referential rule between the two: a channel's Link column names the
// entry declaring the connection it is carried on, and reads `None` when
// that connection is referred to by its own row alone. A row naming the
// connection its calls are forwarded over states that link in its
// message-vocabulary cell, which is a reference to the same register.
//
// Both populations are asserted non-empty, because a register whose rows
// all read `None`, or whose rows all name a link, passes the resolution
// walk while stating neither half of the rule.
//
// spec: §28.3
func assertChannelLinkReferencesResolve(t *testing.T, links, channels registerTable) {
	t.Helper()

	for _, divergence := range unresolvedLinkReferences(links, channels) {
		t.Error(divergence)
	}

	carried, standalone := 0, 0
	for identifier := range channels.rows {
		cell, err := channels.cell(identifier, "Link")
		if err != nil {
			t.Fatalf("read the Link cell of %s: %v", identifier, err)
		}
		if strings.Trim(cell, "`") == noLinkCell {
			standalone++
			continue
		}
		carried++
	}
	if carried == 0 {
		t.Errorf("no channel row names a link entry, so the register states no carried channel")
	}
	if standalone == 0 {
		t.Errorf("every channel row names a link entry, so the register states no channel whose connection its own row alone refers to")
	}
}

// assertDanglingLinkReferenceIsRejected holds the resolution walk itself
// to the rule, by feeding it a channel row whose Link cell and whose
// message-vocabulary cell each name a connection the link register does
// not declare. A walk that read the cell without resolving it against
// the link register would accept both.
//
// spec: §28.3
func assertDanglingLinkReferenceIsRejected(t *testing.T, links, channels registerTable) {
	t.Helper()

	const undeclared = "`LNK-GHOST`"
	carried := carriedChannelIdentifier(t, channels)

	mutated := channels.withCell(carried, "Link", undeclared)
	if got := unresolvedLinkReferences(links, mutated); len(got) == 0 {
		t.Errorf("the resolution walk accepts a Link cell of %s reading %s, which the link register does not declare",
			carried, undeclared)
	}

	forwarded := channels.withCell(carried, "Message vocabulary", "Platform tool calls, forwarded over "+undeclared)
	if got := unresolvedLinkReferences(links, forwarded); len(got) == 0 {
		t.Errorf("the resolution walk accepts a message-vocabulary cell of %s forwarding over %s, which the link register does not declare",
			carried, undeclared)
	}
}

// carriedChannelIdentifier returns one channel the register states a link
// entry for, so a reject case mutates a row whose cells the accept case
// resolves.
func carriedChannelIdentifier(t *testing.T, channels registerTable) string {
	t.Helper()
	identifiers := make([]string, 0, len(channels.rows))
	for identifier := range channels.rows {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	for _, identifier := range identifiers {
		cell, err := channels.cell(identifier, "Link")
		if err != nil {
			t.Fatalf("read the Link cell of %s: %v", identifier, err)
		}
		if strings.Trim(cell, "`") != noLinkCell {
			return identifier
		}
	}
	t.Fatalf("the channel register names no link entry, so no row carries the reference the reject case mutates")
	return ""
}

// unresolvedLinkReferences returns one report per reference a channel row
// makes to a link the link register does not declare, in a stable order.
// It is a separate function so a case can assert that a dangling
// reference is reported without failing itself.
//
// spec: §28.3
func unresolvedLinkReferences(links, channels registerTable) []string {
	var out []string
	for identifier := range channels.rows {
		for _, column := range []string{"Link", "Message vocabulary"} {
			cell, err := channels.cell(identifier, column)
			if err != nil {
				out = append(out, fmt.Sprintf("read the %s cell of %s: %v", column, identifier, err))
				continue
			}
			if column == "Link" && strings.Trim(cell, "`") == noLinkCell {
				continue
			}
			for _, token := range linkTokenExpr.FindAllString(cell, -1) {
				if _, declared := links.rows[token]; !declared {
					out = append(out, fmt.Sprintf("§28.3's %s cell of %s names %s, which the link register does not declare",
						column, identifier, token))
				}
			}
			if column == "Link" && len(linkTokenExpr.FindAllString(cell, -1)) == 0 {
				out = append(out, fmt.Sprintf("§28.3's Link cell of %s is %q, which is neither %s nor a link identifier",
					identifier, cell, noLinkCell))
			}
		}
	}
	sort.Strings(out)
	return out
}

// referringChannelRows counts, per link identifier the link register
// declares, the channel rows that refer to it. A row that names the same
// link in both its Link cell and its message-vocabulary cell counts once,
// because §28.3's threshold is stated over channel rows rather than over
// references.
//
// spec: §28.3
func referringChannelRows(links, channels registerTable) map[string]int {
	counts := make(map[string]int, len(links.rows))
	for identifier := range links.rows {
		counts[identifier] = 0
	}
	for identifier := range channels.rows {
		referred := map[string]bool{}
		for _, column := range linkReferenceColumns {
			cell, err := channels.cell(identifier, column)
			if err != nil {
				continue
			}
			for _, token := range linkTokenExpr.FindAllString(cell, -1) {
				referred[token] = true
			}
		}
		for token := range referred {
			if _, declared := links.rows[token]; declared {
				counts[token]++
			}
		}
	}
	return counts
}

// underDeclaredLinkEntries returns one report per link entry the register
// declares that the declaration threshold does not admit, in a stable
// order. An entry fewer than minimumReferringChannelRows channel rows
// refer to is a connection §28.3 states must read `None` in the one row
// that refers to it, and an entry listed as referred to by no channel row
// that does gain one is a stale exemption. It is a separate function so a
// case can assert that a mutated table is reported without failing itself.
//
// spec: §28.3
func underDeclaredLinkEntries(links, channels registerTable) []string {
	var out []string
	for identifier, count := range referringChannelRows(links, channels) {
		reason, exempt := unreferencedLinkEntries[identifier]
		switch {
		case exempt && count > 0:
			out = append(out, fmt.Sprintf("§28.3 declares %s as referred to by no channel row because %s, and %d channel row(s) refer to it",
				identifier, reason, count))
		case exempt:
		case count < minimumReferringChannelRows:
			out = append(out, fmt.Sprintf("§28.3's link register declares %s, which %d channel row(s) refer to; the register declares a connection more than one channel row refers to",
				identifier, count))
		}
	}
	sort.Strings(out)
	return out
}

// assertLinkDeclarationThresholdHolds holds the link register to the
// declaration threshold §28.3 states, which is the half of the referential
// rule the resolution walk does not reach. The walk reports a channel row
// naming a link the register does not declare; this reports the other
// direction, a link entry the register declares that fewer than two
// channel rows refer to, which §28.3 states must read `None` in the one
// row that refers to it instead. The exemption for an entry no channel row
// refers to is closed, so an exempt entry that gains a referring row is
// reported too.
//
// spec: §28.3
func assertLinkDeclarationThresholdHolds(t *testing.T, links, channels registerTable) {
	t.Helper()
	for _, divergence := range underDeclaredLinkEntries(links, channels) {
		t.Error(divergence)
	}
}

// assertSingleReferenceLinkEntryIsRejected holds the threshold check
// itself to the rule, by dropping one of the two references a declared
// link carries. Every remaining reference still resolves and both
// populations of the Link column stay non-empty, so the resolution walk
// and the non-emptiness assertions accept the mutated table; only the
// threshold reports it. The exempt entry gaining a referring row is
// rejected the same way, so the exemption cannot outlive its reason.
//
// spec: §28.3
func assertSingleReferenceLinkEntryIsRejected(t *testing.T, links, channels registerTable) {
	t.Helper()

	link, first, second := twiceReferredLink(t, links, channels)
	mutated := channels.withCell(second, "Message vocabulary", "Connector tool calls")
	if got := unresolvedLinkReferences(links, mutated); len(got) != 0 {
		t.Fatalf("dropping the reference of %s to %s left an unresolved reference: %v", second, link, got)
	}
	if !reportsEntry(underDeclaredLinkEntries(links, mutated), link) {
		t.Errorf("the threshold accepts %s declared while only %s refers to it", link, first)
	}

	exempted := channels.withCell(first, "Link", "`"+interReplicaLink+"`")
	if !reportsEntry(underDeclaredLinkEntries(links, exempted), interReplicaLink) {
		t.Errorf("the threshold accepts %s listed as referred to by no channel row while %s refers to it",
			interReplicaLink, first)
	}
}

// reportsEntry reports whether any report names the given identifier.
func reportsEntry(reports []string, identifier string) bool {
	for _, report := range reports {
		if strings.Contains(report, identifier) {
			return true
		}
	}
	return false
}

// twiceReferredLink returns a link entry two channel rows refer to,
// together with those two rows in a stable order, so a reject case drops a
// reference the landed table carries rather than one it invents.
func twiceReferredLink(t *testing.T, links, channels registerTable) (link, first, second string) {
	t.Helper()

	identifiers := make([]string, 0, len(links.rows))
	for identifier := range links.rows {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)

	for _, identifier := range identifiers {
		referring := referringChannelRowsFor(channels, identifier)
		if len(referring) == minimumReferringChannelRows {
			return identifier, referring[0], referring[1]
		}
	}
	t.Fatalf("no link entry is referred to by exactly %d channel rows, so no reject case can drop one reference",
		minimumReferringChannelRows)
	return "", "", ""
}

// referringChannelRowsFor returns the channel rows referring to one link
// entry, sorted.
func referringChannelRowsFor(channels registerTable, link string) []string {
	var out []string
	for identifier := range channels.rows {
		for _, column := range linkReferenceColumns {
			cell, err := channels.cell(identifier, column)
			if err != nil {
				continue
			}
			if containsToken(linkTokenExpr.FindAllString(cell, -1), link) {
				out = append(out, identifier)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// containsToken reports whether tokens carries want.
func containsToken(tokens []string, want string) bool {
	for _, token := range tokens {
		if token == want {
			return true
		}
	}
	return false
}

// assertReassignedWriterSetIsRejected holds the writer-set check itself to
// the attribution §12.6 and §4.6.3 state, by feeding it writer sets that
// keep every name and column the landed cells carry while reassigning what
// each writer writes. Each of these describes a store written the way
// neither section states, so each must be rejected: a check that only
// looked for the names somewhere in the cell would accept all of them.
//
// spec: §28.3, §12.6, §4.6.3
func assertReassignedWriterSetIsRejected(t *testing.T, entries registerTable) {
	t.Helper()

	for name, cases := range map[string]struct {
		identifier string
		want       string
		reassigned []string
	}{
		"pod state": {
			identifier: podStateRegisterEntry,
			want:       podStateWriterSetCell,
			reassigned: []string{
				"WarmPoolController for `sessions_served` and `scrub_failure_count`, and gateway replicas " +
					"for the mirrored `Sandbox` status columns",
				"WarmPoolController for the mirrored `Sandbox` status columns, and gateway replicas " +
					"for `sessions_served`, `scrub_failure_count`, and `phase`",
			},
		},
		"claim": {
			identifier: claimRegisterEntry,
			want:       claimWriterSetCell,
			reassigned: []string{
				"WarmPoolController leader for the create, the status-subresource binding-state writes, and " +
					"the hold-expiry delete, and the Gateway replicas for the deletes at pod termination and " +
					"orphan garbage collection",
				claimWriterSetCell + ", and the RuntimeAdapter for the binding-state writes",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, cell := range cases.reassigned {
				mutated := entries.withCell(cases.identifier, "Writer set", cell)
				if err := mutated.cellDivergence(cases.identifier, "Writer set", cases.want); err == nil {
					t.Errorf("the writer-set check accepts %q for %s, which reassigns what each writer writes",
						cell, cases.identifier)
				}
			}
		})
	}
}

// collapseWrapping returns text with every run of whitespace replaced by
// a single space, so a sentence the specification wraps across source
// lines compares as the one sentence it reads as.
func collapseWrapping(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// diagnosis: a failure means §28.4 no longer states where the claim
// register lives. The section is the only normative statement of the
// register's path, and the register's seed rows and validator are written
// against that path, so a §28.4 that names no location leaves the file
// with no specified home and leaves the deferral §28.4 records for the
// interval before the file lands citing a sentence that does not exist.
//
// spec: §28.4
func TestSection28ClaimRegisterNamesItsLocation_spec_28_4(t *testing.T) {
	section := collapseWrapping(specSection(t, filepath.Join(repoRoot(t), "spec", channelsSpecFile), claimRegisterHeading))

	if !strings.Contains(section, claimRegisterLocationSentence) {
		t.Errorf("§28.4 does not open with %q; the section states no location for the claim register",
			claimRegisterLocationSentence)
	}
}

// assertPodStateWriterSetMatchesStorage holds `REG-PODSTATE`'s writer
// set to §12.6's statement of who writes the mirror and its two
// gateway-written recycle counters.
//
// spec: §28.3, §12.6
func assertPodStateWriterSetMatchesStorage(t *testing.T, entries registerTable) {
	t.Helper()

	requireCellEquals(t, entries, podStateRegisterEntry, "Writer set", podStateWriterSetCell)

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

	requireCellEquals(t, entries, claimRegisterEntry, "Writer set", claimWriterSetCell)

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

	requireCellEquals(t, links, interReplicaLink, "Transport", "gRPC")
	requireCellContains(t, links, interReplicaLink, "Dial direction", []string{"Forwarding replica"})
	requireCellContains(t, links, interReplicaLink, "Lifetime", []string{"coordinating replica"})

	sessions := specSection(t, filepath.Join(repoRoot(t), "spec", "07_session-lifecycle.md"), "### 7.2 ")
	requireAllContain(t, "§7.2 cross-replica message routing", sessions, []string{
		forwardMessageTransportSentence,
		forwardingReplicaSentence,
	})
}
