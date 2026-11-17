// SPDX-License-Identifier: MIT

package defs

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// CatalogDir is the repo-relative folder of the generated catalogs.
const CatalogDir = SpecDir + "/catalogs"

// catalog is one generated catalog file.
type catalog struct {
	// file is the base name under CatalogDir.
	file string
	// code is the page code a new catalog file is given. An existing
	// catalog keeps the front matter it has, so the code the registry
	// assigns survives regeneration.
	code    string
	title   string
	summary string
	// source names what the catalog is generated from, for its header.
	source string
	// render returns the body after the title, or "" when the tree holds
	// nothing the catalog lists.
	render func(t *Tree, l linker) string
}

// catalogs is the catalog table, in file-name order.
var catalogs = []catalog{
	{
		file: "alerts.md", code: "CATALERT", title: "Alerts",
		summary: "Lists every alert with its severity, the requirements whose breach it detects, and its owner block.",
		source:  "the alert definition blocks", render: renderAlerts,
	},
	{
		file: "api-operations.md", code: "CATOPS", title: "API operations",
		summary: "Lists every interface operation with its identifier, method, path, and owner block.",
		source:  "the operation definition blocks", render: renderOperations,
	},
	{
		file: "channels.md", code: "CATCHAN", title: "Channels",
		summary: "Lists every register with its store, writers, readers, and owner block.",
		source:  "the register definition blocks", render: renderChannels,
	},
	{
		file: "contributions.md", code: "CATCONTR", title: "Contributions",
		summary: "Lists every RBAC grant and network rule with the running unit that carries it, the page that states it, and the requirement behind it.",
		source:  "the grant, rbac-grant, and network-rule definition blocks", render: renderContributions,
	},
	{
		file: "custom-resources.md", code: "CATCRD", title: "Custom resources",
		summary: "Lists every custom resource kind with its group, version, scope, and owner block.",
		source:  "the custom-resource definition blocks", render: renderCustomResources,
	},
	{
		file: "errors.md", code: "CATERR", title: "Errors",
		summary: "Lists every error code, admission refusal, and condition reason with its status and owner block.",
		source:  "the error, admission-refusal, and condition-reason definition blocks", render: renderErrors,
	},
	{
		file: "events.md", code: "CATEVT", title: "Events",
		summary: "Lists every event type with its emitting components, its payload, and its owner block.",
		source:  "the event definition blocks", render: renderEvents,
	},
	{
		file: "glossary.md", code: "CATGLOSS", title: "Glossary",
		summary: "Lists every owned term with a one-sentence gloss and a link to its owner.",
		source:  "the owned terms of each page's Terms section", render: renderGlossary,
	},
	{
		file: "labels.md", code: "CATLABEL", title: "Labels and annotations",
		summary: "Lists every label, annotation, finalizer, and readiness gate with its carriers, mutability, and owner block.",
		source:  "the label, annotation, finalizer, and readiness-gate definition blocks", render: renderLabels,
	},
	{
		file: "limits.md", code: "CATLIMIT", title: "Limits",
		summary: "Lists every limit with its value, unit, and owner block.",
		source:  "the limit definition blocks", render: renderLimits,
	},
	{
		file: "metrics.md", code: "CATMETRIC", title: "Metrics",
		summary: "Lists every metric with its type, labels, emitting components, and owner block.",
		source:  "the metric definition blocks", render: renderMetrics,
	},
	{
		file: "requirements.md", code: "CATREQ", title: "Requirements",
		summary: "Lists every requirement identifier with its title and page.",
		source:  "the requirement anchors of each page", render: renderRequirements,
	},
	{
		file: "settings.md", code: "CATSET", title: "Settings",
		summary: "Lists every setting with its type, default, range, scopes, the components that read it, its tier presets, and its owner block.",
		source:  "the setting definition blocks", render: renderSettings,
	},
	{
		file: "state-machines.md", code: "CATSTATE", title: "State machines",
		summary: "Lists every state machine with its object, its states, and its owner block.",
		source:  "the state definition blocks", render: renderStates,
	},
}

// catalogIndex is the front matter a new catalogs/index.md is given.
var catalogIndex = catalog{
	file: "index.md", code: "CATIDX", title: "Catalogs",
	summary: "Lists the catalogs generated from the definition blocks, the terms, and the requirements of the specification's pages.",
}

// defaultFrontMatter returns the front matter a new generated file is
// given. It stays short so the header line falls inside the leading lines
// the generated-artifact rule reads.
func defaultFrontMatter(c catalog, pageType string) string {
	return fmt.Sprintf("---\ncode: %s\ntype: %s\nsummary: %s\n---\n", c.code, pageType, c.summary)
}

// renderCatalog returns the full text of one catalog file.
func renderCatalog(t *Tree, c catalog, front string) string {
	l := linker{fromDir: CatalogDir, codes: t.Codes()}
	body := c.render(t, l)
	if body == "" {
		body = fmt.Sprintf("This catalog is generated from %s, and it is empty because the specification holds none yet.\n", c.source)
	}
	return front + "\n" + generatedHeader(c.source) + "\n# " + c.title + "\n\n" + strings.TrimRight(body, "\n") + "\n"
}

// sections joins titled sections, dropping the empty ones, and returns ""
// when every section is empty.
func sections(parts ...[2]string) string {
	var out []string
	for _, p := range parts {
		if p[1] == "" {
			continue
		}
		out = append(out, "## "+p[0]+"\n\n"+p[1])
	}
	return strings.Join(out, "\n")
}

func renderSettings(t *Tree, l linker) string {
	blocks := blocksOf(t, KindSetting)
	if len(blocks) == 0 {
		return ""
	}
	tb := table{header: []string{"Setting", "Type", "Default", "Range or values", "Unit", "Scopes", "Read by", "Summary"}}
	for _, b := range blocks {
		rng := b.Text("range")
		if n := b.fields["range"]; n != nil && n.Kind == yaml.SequenceNode {
			rng = "[" + rng + "]"
		}
		if b.Has("values") {
			rng = codes(b.List("values"))
		}
		tb.add(l.block(b), b.Text("type"), b.Text("default"), rng, b.Text("unit"), b.Text("scopes"), l.codeList(b.List("read-by")), b.Text("summary"))
	}
	return tb.render() + "\n" + renderTierPresets(blocks, l)
}

// renderTierPresets returns the tier presets section of the settings
// catalog: one row per setting with per-tier defaults, one column per tier.
func renderTierPresets(blocks []*Block, l linker) string {
	var tiers []string
	seen := map[string]bool{}
	var with []*Block
	for _, b := range blocks {
		pairs := mappingPairs(b, "defaults-by-tier")
		if len(pairs) == 0 {
			continue
		}
		with = append(with, b)
		for _, p := range pairs {
			if !seen[p[0]] {
				seen[p[0]] = true
				tiers = append(tiers, p[0])
			}
		}
	}
	out := "## Tier presets\n\n"
	if len(with) == 0 {
		return out + "No setting defines per-tier defaults yet.\n"
	}
	sortTiers(tiers)
	tb := table{header: append([]string{"Setting"}, tiers...)}
	for _, b := range with {
		values := map[string]string{}
		for _, p := range mappingPairs(b, "defaults-by-tier") {
			values[p[0]] = p[1]
		}
		row := []string{l.block(b)}
		for _, tier := range tiers {
			row = append(row, values[tier])
		}
		tb.add(row...)
	}
	return out + tb.render()
}

// tierRank orders the tier names the specification uses. A tier outside
// the list sorts after them, alphabetically.
var tierRank = map[string]int{"small": 1, "medium": 2, "large": 3}

var trailingNumber = regexp.MustCompile(`^(.*?)(\d+)$`)

func sortTiers(tiers []string) {
	sort.SliceStable(tiers, func(i, j int) bool {
		ri, rj := tierRank[strings.ToLower(tiers[i])], tierRank[strings.ToLower(tiers[j])]
		if ri != rj && ri != 0 && rj != 0 {
			return ri < rj
		}
		if (ri == 0) != (rj == 0) {
			return ri != 0
		}
		return naturalLess(tiers[i], tiers[j])
	})
}

// naturalLess compares two names, ordering a shared prefix by its
// trailing number, so "tier2" sorts before "tier10".
func naturalLess(a, b string) bool {
	ma, mb := trailingNumber.FindStringSubmatch(a), trailingNumber.FindStringSubmatch(b)
	if ma != nil && mb != nil && ma[1] == mb[1] {
		na, _ := strconv.Atoi(ma[2])
		nb, _ := strconv.Atoi(mb[2])
		return na < nb
	}
	return a < b
}

// mappingPairs returns the key and value text of a mapping field in
// document order.
func mappingPairs(b *Block, field string) [][2]string {
	n := b.fields[field]
	if n == nil {
		return nil
	}
	var out [][2]string
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, [2]string{nodeText(n.Content[i]), nodeText(n.Content[i+1])})
	}
	return out
}

func renderLimits(t *Tree, l linker) string {
	blocks := blocksOf(t, KindLimit)
	if len(blocks) == 0 {
		return ""
	}
	tb := table{header: []string{"Limit", "Value", "Unit", "Scopes", "Overridden by", "Summary"}}
	for _, b := range blocks {
		tb.add(l.block(b), b.Text("value"), b.Text("unit"), b.Text("scopes"), code(b.Text("setting")), b.Text("summary"))
	}
	return tb.render()
}

func renderErrors(t *Tree, l linker) string {
	errs := table{header: []string{"Code", "Status", "Category", "Retryable", "Summary"}}
	for _, b := range blocksOf(t, KindError) {
		errs.add(l.block(b), b.Text("status"), b.Text("category"), b.Text("retryable"), b.Text("summary"))
	}
	refusals := table{header: []string{"Refusal", "Status", "Message", "Summary"}}
	for _, b := range blocksOf(t, KindAdmissionRefusal) {
		status := b.Text("status")
		if status == "" {
			status = "403"
		}
		refusals.add(l.block(b), status, code(b.Text("message")), b.Text("summary"))
	}
	reasons := table{header: []string{"Reason", "Resource", "Condition", "Summary"}}
	for _, b := range blocksOf(t, KindConditionReason) {
		reasons.add(l.block(b), code(b.Text("resource")), code(b.Text("condition")), b.Text("summary"))
	}
	return sections(
		[2]string{"Error codes", renderIfRows(errs)},
		[2]string{"Admission refusals", renderIfRows(refusals)},
		[2]string{"Condition reasons", renderIfRows(reasons)},
	)
}

// renderIfRows renders a table that has rows, and returns "" otherwise.
func renderIfRows(tb table) string {
	if len(tb.rows) == 0 {
		return ""
	}
	return tb.render()
}

func renderMetrics(t *Tree, l linker) string {
	tb := table{header: []string{"Metric", "Type", "Unit", "Labels", "Emitted by", "Summary"}}
	for _, b := range blocksOf(t, KindMetric) {
		tb.add(l.block(b), b.Text("type"), b.Text("unit"), codes(b.List("labels")), l.codeList(b.List("emitter")), b.Text("summary"))
	}
	return renderIfRows(tb)
}

func renderAlerts(t *Tree, l linker) string {
	tb := table{header: []string{"Alert", "Severity", "For", "Detects", "Summary"}}
	for _, b := range blocksOf(t, KindAlert) {
		tb.add(l.block(b), b.Text("severity"), b.Text("for"), b.Text("detects"), b.Text("summary"))
	}
	return renderIfRows(tb)
}

func renderEvents(t *Tree, l linker) string {
	tb := table{header: []string{"Event type", "Emitted by", "Payload", "Summary"}}
	for _, b := range blocksOf(t, KindEvent) {
		tb.add(l.block(b), l.codeList(b.List("emitter")), b.Text("payload"), b.Text("summary"))
	}
	return renderIfRows(tb)
}

func renderLabels(t *Tree, l linker) string {
	tb := table{header: []string{"Key", "Kind", "Carriers", "Values", "Mutability", "Summary"}}
	for _, b := range blocksOf(t, KindLabel, KindAnnotation, KindFinalizer, KindReadinessGate) {
		tb.add(l.block(b), b.Schema.Title, b.Text("carriers"), b.Text("values"), b.Text("mutability"), b.Text("summary"))
	}
	return renderIfRows(tb)
}

func renderCustomResources(t *Tree, l linker) string {
	tb := table{header: []string{"Kind", "Group", "Version", "Scope", "Summary"}}
	for _, b := range blocksOf(t, KindCustomResource) {
		tb.add(l.block(b), code(b.Text("group")), code(b.Text("version")), b.Text("scope"), b.Text("summary"))
	}
	return renderIfRows(tb)
}

func renderOperations(t *Tree, l linker) string {
	tb := table{header: []string{"Operation", "Method", "Path", "Summary"}}
	for _, b := range blocksOf(t, KindOperation) {
		id := b.Page.Front.Code + "-" + b.Name
		link := "[`" + id + "`](" + relLink(l.fromDir, b.Page.Path) + "#" + b.Anchor + ")"
		tb.add(link, b.Text("method"), code(b.Text("path")), b.Text("summary"))
	}
	return renderIfRows(tb)
}

func renderStates(t *Tree, l linker) string {
	tb := table{header: []string{"State machine", "Object", "States", "Summary"}}
	for _, b := range blocksOf(t, KindState) {
		tb.add(l.block(b), b.Text("object"), codes(b.List("states")), b.Text("summary"))
	}
	return renderIfRows(tb)
}

func renderChannels(t *Tree, l linker) string {
	tb := table{header: []string{"Register", "Identifier", "Store", "Writers", "Readers", "Summary"}}
	for _, b := range blocksOf(t, KindRegister) {
		tb.add(l.block(b), code(b.Text("identifier")), b.Text("store"), b.Text("writers"), b.Text("readers"), b.Text("summary"))
	}
	return sections([2]string{"Registers", renderIfRows(tb)})
}

func renderContributions(t *Tree, l linker) string {
	grants := grantTable(blocksOf(t, KindGrant, KindRBACGrant), l, true)
	rules := ruleTable(blocksOf(t, KindNetworkRule), l, true)
	return sections(
		[2]string{"RBAC grants", renderIfRows(grants)},
		[2]string{"Network rules", renderIfRows(rules)},
	)
}

// grantUnit returns the code of the unit that holds a grant: its unit
// field for a contributed grant, and its own page's code otherwise.
func grantUnit(b *Block) string {
	if b.Kind == KindRBACGrant {
		return b.Text("unit")
	}
	return b.Page.Front.Code
}

// grantTable renders RBAC grants; withUnit adds the unit column, which a
// unit's own page leaves out.
func grantTable(blocks []*Block, l linker, withUnit bool) table {
	header := []string{"Grant", "Stated on", "API groups", "Resources", "Verbs", "Scope", "Requirement"}
	if withUnit {
		header = append([]string{"Unit"}, header...)
	}
	tb := table{header: header}
	for _, b := range blocks {
		row := []string{l.block(b), l.page(b.Page), apiGroups(b.List("api-groups")), codes(b.List("resources")), codes(b.List("verbs")), b.Text("namespace-scope"), b.Text("requirement")}
		if withUnit {
			row = append([]string{l.code(grantUnit(b))}, row...)
		}
		tb.add(row...)
	}
	return tb
}

// apiGroups renders API groups, naming the core group.
func apiGroups(groups []string) string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		if g == "" {
			out = append(out, "core")
			continue
		}
		out = append(out, code(g))
	}
	return strings.Join(out, ", ")
}

func ruleTable(blocks []*Block, l linker, withUnit bool) table {
	header := []string{"Rule", "Stated on", "Direction", "Peer", "Ports", "Requirement"}
	if withUnit {
		header = append([]string{"Unit"}, header...)
	}
	tb := table{header: header}
	for _, b := range blocks {
		row := []string{l.block(b), l.page(b.Page), b.Text("direction"), b.Text("peer"), codes(b.List("ports")), b.Text("requirement")}
		if withUnit {
			row = append([]string{l.code(b.Text("unit"))}, row...)
		}
		tb.add(row...)
	}
	return tb
}

func renderGlossary(t *Tree, l linker) string {
	var terms []Term
	for _, p := range t.Pages {
		terms = append(terms, p.Terms...)
	}
	sort.SliceStable(terms, func(i, j int) bool {
		a, b := strings.ToLower(terms[i].Term), strings.ToLower(terms[j].Term)
		if a != b {
			return a < b
		}
		return terms[i].Anchor < terms[j].Anchor
	})
	tb := table{header: []string{"Term", "Gloss", "Owner"}}
	for _, term := range terms {
		link := "[" + term.Term + "](" + relLink(l.fromDir, term.Page.Path) + "#" + term.Anchor + ")"
		tb.add(link, firstSentence(term.Definition), l.page(term.Page))
	}
	return renderIfRows(tb)
}

var requirementNumber = regexp.MustCompile(`^([A-Z]+)-R(\d+)$`)

func renderRequirements(t *Tree, l linker) string {
	var reqs []Requirement
	for _, p := range t.Pages {
		reqs = append(reqs, p.Requirements...)
	}
	sort.SliceStable(reqs, func(i, j int) bool { return requirementLess(reqs[i].Identifier, reqs[j].Identifier) })
	tb := table{header: []string{"Requirement", "Title", "Page"}}
	for _, r := range reqs {
		link := "[" + r.Identifier + "](" + relLink(l.fromDir, r.Page.Path) + "#" + r.Anchor + ")"
		tb.add(link, strings.TrimSuffix(r.Title, "."), l.page(r.Page))
	}
	return renderIfRows(tb)
}

// requirementLess orders requirement identifiers by page code and then by
// number.
func requirementLess(a, b string) bool {
	ma, mb := requirementNumber.FindStringSubmatch(a), requirementNumber.FindStringSubmatch(b)
	if ma == nil || mb == nil || ma[1] != mb[1] {
		return a < b
	}
	na, _ := strconv.Atoi(ma[2])
	nb, _ := strconv.Atoi(mb[2])
	return na < nb
}

// catalogPath returns the repo-relative path of a catalog file.
func catalogPath(file string) string { return path.Join(CatalogDir, file) }
