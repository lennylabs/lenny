// SPDX-License-Identifier: MIT

package defs

import (
	"strings"
)

// SchemaReference renders the definition-block format and every kind's
// schema as Markdown, with its kind headings at the given level. The same
// text fills a page's definition-kinds section and SCHEMAS.md, so the
// reference a reader sees is the table the check applies.
func SchemaReference(level int) string {
	h := strings.Repeat("#", level)
	var sb strings.Builder
	sb.WriteString(schemaIntro)
	sb.WriteString("\n" + h + " Field types\n\n")
	types := table{header: []string{"Type", "Accepted value"}}
	for _, ft := range fieldTypeDocs {
		types.add(string(ft[0]), ft[1])
	}
	sb.WriteString(types.render())
	sb.WriteString("\n" + h + " Generated sections\n\n")
	sb.WriteString(regionIntro)
	regions := table{header: []string{"Section", "Content"}}
	for _, r := range regionDocs {
		regions.add("`"+r[0]+"`", r[1])
	}
	sb.WriteString(regions.render())
	for _, k := range Kinds() {
		sb.WriteString("\n" + h + " `" + k.Name + "`\n\n")
		sb.WriteString("- Defines: one " + k.Title + ".\n")
		sb.WriteString("- Owner: " + k.Owner + "\n")
		sb.WriteString("- Catalog: `catalogs/" + k.Catalog + ".md`.\n")
		sb.WriteString("- Name field: `" + k.NameField + "`, which gives the anchor's slug.\n\n")
		fields := table{header: []string{"Field", "Type", "Required", "Meaning"}}
		for _, f := range k.Fields {
			typ := string(f.Type)
			if len(f.Values) > 0 {
				typ += ": " + codes(f.Values)
			}
			required := "no"
			if f.Required {
				required = "yes"
			}
			fields.add("`"+f.Name+"`", typ, required, f.Doc)
		}
		sb.WriteString(fields.render())
	}
	return sb.String()
}

const schemaIntro = "A definition block is a fenced code block whose info string is `lenny-def` and whose body is a YAML mapping. " +
	"The `kind` field names the block's kind, and the kind's schema below lists the other fields. " +
	"A block carries every required field of its kind and no field its kind lacks.\n\n" +
	"Each block's anchor is `<kind>-<slug>`, where the slug is the block's name lower-cased with each run of characters other than letters and digits replaced by `-` and with no `-` at either end: " +
	"the setting `gateway.claimHoldTTLSeconds` is anchored `setting-gateway-claimholdttlseconds`. " +
	"An operation is anchored by its identifier `<CODE>-<name>` slugged the same way: the operation `createPool` on the page `ADMPOOLS` is anchored `admpools-createpool`. " +
	"The anchor is an HTML anchor element on its own line directly above the block, with only blank lines between them, whose id is the anchor. " +
	"No two blocks, owned terms, or requirements of the specification share an anchor.\n\n" +
	"A field of type `page code` or `page codes` names pages by the codes their front matter registers. " +
	"`specshift defs check` refuses a code that no page of the tree carries.\n"

var fieldTypeDocs = [][2]string{
	{string(TypeString), "A non-empty scalar, read as text."},
	{string(TypeScalar), "A string, a number, or a boolean."},
	{string(TypeInteger), "An integer."},
	{string(TypeBoolean), "`true` or `false`."},
	{string(TypeEnum), "One of the values the field lists."},
	{string(TypeList), "A YAML sequence of scalars, which may be empty (`[]`)."},
	{string(TypeScalarOrList), "A scalar or a sequence of scalars."},
	{string(TypeEnumList), "A non-empty sequence of values the field lists."},
	{string(TypeCode), "One registered page code."},
	{string(TypeCodes), "One registered page code, or a non-empty sequence of them."},
	{string(TypeMapping), "A non-empty mapping whose values are scalars."},
	{string(TypeTransitions), "A non-empty sequence of mappings, each with `from` (a state or a sequence of states), `to` (a state), and optionally `on` and `rule`. Each state named is one of the block's `states`."},
}

const regionIntro = "A page carries a generated section between the marker comments `<!-- generated:<section> -->` and `<!-- /generated:<section> -->`, each on its own line. " +
	"`specshift defs generate` replaces what lies between the markers and keeps the rest of the page. " +
	"A page that a block names in `read-by`, `emitter`, or `unit` carries the section that block fills, unless the page is a placeholder.\n\n"

var regionDocs = [][2]string{
	{RegionSettingsRead, "The settings whose `read-by` names the page's code."},
	{RegionMetricsEmitted, "The metrics whose `emitter` names the page's code."},
	{RegionEventsEmitted, "The event types whose `emitter` names the page's code."},
	{RegionRBACGrants, "The page's own `grant` blocks and the `rbac-grant` blocks whose `unit` names the page's code."},
	{RegionNetworkRules, "The `network-rule` blocks whose `unit` names the page's code."},
	{RegionIndex, "On every folder's `index.md`, the table of the folder's pages. The `index.md` keeps its hand-written sections, and only the text between its `index` markers is generated. A folder index with no such section gets one appended under a Pages heading."},
	{RegionDefinitionKinds, "This reference."},
	{RegionCodeRegistry, "On the conventions page, the code registry: every page's code, type, path, and summary, read from the pages' front matter."},
}
