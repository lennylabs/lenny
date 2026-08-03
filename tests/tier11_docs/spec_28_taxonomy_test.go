// SPDX-License-Identifier: MIT

// Tier-11 documentation check holding §28.3's registers to the taxonomy
// §28.2 states over them. §28.2 makes two statements the registers have
// to satisfy: the class of an identifier fixes the columns its entry
// carries in §28.3, and the transport and boundary axes are closed sets
// whose extension takes a specification change. Neither is enforced by a
// pass, because the migration tooling reads the naming table and the
// identifier cells of the registers and no other column, so this is
// where the two sections are reconciled.
//
// These tests are NOT under a build tag because they exercise the
// repository state directly — no external infrastructure required.
//
// spec: §28.2, §28.3

package tier11_docs_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// taxonomyHeading is the heading §28.2 publishes its class table and its
// axes table under.
//
// spec: §28.2
const taxonomyHeading = "28.2 Taxonomy and axes"

// The header labels that tell §28.2's two tables apart, and the columns
// each states its content in.
//
// spec: §28.2
const (
	classColumn       = "Class"
	classColumnsCell  = "Columns it carries"
	axisColumn        = "Axis"
	axisValuesCell    = "Values"
	transportAxis     = "Transport"
	boundaryAxis      = "Boundary"
	identifierColumn  = "Identifier"
	provenanceColumn  = "Provenance"
	classOfLinks      = "Link"
	classOfChannels   = "Channel"
	classOfRegisters  = "Register"
	transportColumnOf = "Transport"
	boundaryColumnOf  = "Boundary"
)

// diagnosis: a failure means §28.2 and §28.3 no longer describe the same
// registers. A column-set failure means a register table carries a column
// the class table does not state, or drops one it does, so a reader
// following §28.2 to find a column reads a table that does not carry it.
// A closed-set failure means a channel row states a transport or a
// boundary outside the set §28.2 closes, which is the undeclared
// extension §28.2 states requires a specification change instead, and the
// boundary half also names the contract-card subsection the channel is
// grouped under.
//
// spec: §28.2, §28.3
func TestSection28TaxonomyFixesTheRegisterColumns_spec_28_2(t *testing.T) {
	section := readChannelsSection(t)
	classes, axes := readTaxonomyTables(t, section)
	links, channels, entries := readChannelsRegisters(t)
	byClass := map[string]registerTable{
		classOfLinks:     links,
		classOfChannels:  channels,
		classOfRegisters: entries,
	}

	t.Run("column sets", func(t *testing.T) {
		for _, divergence := range columnSetDivergences(classes, byClass) {
			t.Error(divergence)
		}
	})

	t.Run("renamed column", func(t *testing.T) {
		renamed := map[string]registerTable{
			classOfLinks:     links.withColumnRenamed("Endpoint", "Address"),
			classOfChannels:  channels,
			classOfRegisters: entries,
		}
		if got := columnSetDivergences(classes, renamed); len(got) == 0 {
			t.Errorf("the column-set check accepts a link register whose Endpoint column is renamed, which §28.2 states as a column of the class")
		}
	})

	t.Run("closed sets", func(t *testing.T) {
		for _, divergence := range closedSetDivergences(axes, channels) {
			t.Error(divergence)
		}
	})

	t.Run("value outside a closed set", func(t *testing.T) {
		carried := carriedChannelIdentifier(t, channels)
		for column, value := range map[string]string{
			boundaryColumnOf:  "`pod-to-pod`",
			transportColumnOf: "AMQP",
		} {
			mutated := channels.withCell(carried, column, value)
			if got := closedSetDivergences(axes, mutated); len(got) == 0 {
				t.Errorf("the closed-set check accepts %s in the %s cell of %s, which §28.2 does not state",
					value, column, carried)
			}
		}
	})
}

// columnSetDivergences returns one report per disagreement between the
// columns §28.2's class table states for a class and the columns the
// register of that class carries in §28.3. The identifier column and the
// provenance column are outside the comparison: the first is the key
// every register is written on and the second carries the entry number
// of the derivation, and §28.2's table states neither.
//
// spec: §28.2, §28.3
func columnSetDivergences(classes registerTable, byClass map[string]registerTable) []string {
	var out []string
	for _, class := range sortedKeys(byClass) {
		stated, err := classes.cell(class, classColumnsCell)
		if err != nil {
			out = append(out, fmt.Sprintf("read the columns §28.2 states for the %s class: %v", class, err))
			continue
		}
		want := listMembers(stated)
		got := registerColumns(byClass[class])
		if !equalStrings(want, got) {
			out = append(out, fmt.Sprintf("§28.2 states the columns %v for the %s class and its register in §28.3 carries %v",
				want, class, got))
		}
	}
	sort.Strings(out)
	return out
}

// closedSetDivergences returns one report per channel row whose transport
// or boundary cell states a value outside the set §28.2 closes for that
// axis.
//
// spec: §28.2, §28.3
func closedSetDivergences(axes, channels registerTable) []string {
	var out []string
	for axis, column := range map[string]string{transportAxis: transportColumnOf, boundaryAxis: boundaryColumnOf} {
		stated, err := axes.cell(axis, axisValuesCell)
		if err != nil {
			out = append(out, fmt.Sprintf("read the values §28.2 closes for the %s axis: %v", axis, err))
			continue
		}
		closed := map[string]bool{}
		for _, member := range listMembers(stated) {
			closed[member] = true
		}
		if len(closed) == 0 {
			out = append(out, fmt.Sprintf("§28.2 closes the %s axis over no value, so the set it states is empty", axis))
			continue
		}
		for _, identifier := range sortedRowKeys(channels) {
			cell, err := channels.cell(identifier, column)
			if err != nil {
				out = append(out, fmt.Sprintf("read the %s cell of %s: %v", column, identifier, err))
				continue
			}
			if value := strings.ToLower(strings.Trim(strings.TrimSpace(cell), "`")); !closed[value] {
				out = append(out, fmt.Sprintf("§28.3's %s cell of %s states %q, which is outside the set §28.2 closes for the %s axis",
					column, identifier, cell, axis))
			}
		}
	}
	sort.Strings(out)
	return out
}

// readTaxonomyTables returns §28.2's class table and its axes table. The
// two stand under one heading, so they are told apart by the header label
// each is keyed on.
//
// spec: §28.2
func readTaxonomyTables(t *testing.T, section string) (classes, axes registerTable) {
	t.Helper()
	tables, err := readSectionTables(section, taxonomyHeading)
	if err != nil {
		t.Fatalf("read the tables of §28.2 from %s: %v", channelsSpecFile, err)
	}
	for _, table := range tables {
		switch {
		case hasColumn(table, classColumn) && hasColumn(table, classColumnsCell):
			classes = table
		case hasColumn(table, axisColumn) && hasColumn(table, axisValuesCell):
			axes = table
		}
	}
	if len(classes.rows) == 0 {
		t.Fatalf("§28.2 carries no table keyed on %q, so it states no class", classColumn)
	}
	if len(axes.rows) == 0 {
		t.Fatalf("§28.2 carries no table keyed on %q, so it states no axis", axisColumn)
	}
	return classes, axes
}

// readSectionTables returns every table published under one heading of
// the section, in source order. A table ends at the first line that is
// not a table row, so two tables separated by prose are read as two.
//
// spec: §28.2
func readSectionTables(section, heading string) ([]registerTable, error) {
	var tables []registerTable
	var block []string
	inSection := false
	flush := func() {
		if len(block) > 0 {
			tables = append(tables, tableFromRows(heading, block))
			block = nil
		}
	}
	for _, line := range strings.Split(section, "\n") {
		if m := atxHeading.FindStringSubmatch(line); m != nil {
			if inSection {
				break
			}
			inSection = strings.TrimSpace(m[2]) == heading
			continue
		}
		if !inSection {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			block = append(block, line)
			continue
		}
		flush()
	}
	flush()
	if !inSection {
		return nil, fmt.Errorf("the section carries no heading %q", heading)
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("the section under %q carries no table", heading)
	}
	return tables, nil
}

// tableFromRows builds one table out of the consecutive table rows of a
// block, keyed by the value its first column carries.
func tableFromRows(heading string, block []string) registerTable {
	table := registerTable{heading: heading, columns: map[string]int{}, rows: map[string][]string{}}
	for _, line := range block {
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
	return table
}

// hasColumn reports whether the table carries the named column.
func hasColumn(table registerTable, label string) bool {
	_, ok := table.columns[label]
	return ok
}

// withColumnRenamed returns a copy of the table whose column carries
// another label, so a case can state what the column-set check must
// reject without editing spec/.
//
// spec: §28.2
func (r registerTable) withColumnRenamed(from, to string) registerTable {
	copied := registerTable{heading: r.heading, columns: map[string]int{}, rows: r.rows}
	for label, index := range r.columns {
		if label == from {
			label = to
		}
		copied.columns[label] = index
	}
	return copied
}

// registerColumns returns the columns one register of §28.3 carries,
// lowercased and sorted, less the identifier key and the provenance
// column §28.2's class table does not state.
//
// spec: §28.2, §28.3
func registerColumns(table registerTable) []string {
	var columns []string
	for label := range table.columns {
		if label == identifierColumn || label == provenanceColumn {
			continue
		}
		columns = append(columns, strings.ToLower(label))
	}
	sort.Strings(columns)
	return columns
}

// listMembers returns the members of a prose list, lowercased and
// sorted. The specification writes such a list with commas, a closing
// conjunction, and the code delimiters a value takes when it is a
// literal, and every one of those is punctuation rather than part of a
// member.
//
// spec: §28.2
func listMembers(cell string) []string {
	var members []string
	for _, part := range strings.Split(cell, ",") {
		member := strings.TrimSpace(part)
		for _, conjunction := range []string{"and ", "or "} {
			member = strings.TrimPrefix(member, conjunction)
		}
		member = strings.ToLower(strings.Trim(strings.TrimSpace(member), "`. "))
		if member != "" {
			members = append(members, member)
		}
	}
	sort.Strings(members)
	return members
}

// sortedKeys returns the keys of a table map in a stable order.
func sortedKeys(byClass map[string]registerTable) []string {
	keys := make([]string, 0, len(byClass))
	for key := range byClass {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// sortedRowKeys returns the entry keys of one table in a stable order.
func sortedRowKeys(table registerTable) []string {
	keys := make([]string, 0, len(table.rows))
	for key := range table.rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
