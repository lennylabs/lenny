// SPDX-License-Identifier: MIT

package defs

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Check validates the tree: every block against its kind's schema, every
// anchor unique, every page code it names registered, every page code
// registered once, every generated-section marker well formed, and every
// reference page of the documentation carrying the regions it lists. It
// returns the problems sorted by file and line.
func Check(t *Tree) []Problem {
	problems := append([]Problem(nil), t.Problems...)
	codes := t.Codes()
	problems = append(problems, checkCodes(t)...)
	for _, b := range t.Blocks() {
		problems = append(problems, checkBlock(b, codes)...)
	}
	problems = append(problems, checkAnchors(t)...)
	problems = append(problems, checkRegions(t)...)
	problems = append(problems, checkRefPages(t)...)
	sortProblems(problems)
	return problems
}

func sortProblems(problems []Problem) {
	sort.SliceStable(problems, func(i, j int) bool {
		if problems[i].Path != problems[j].Path {
			return problems[i].Path < problems[j].Path
		}
		if problems[i].Line != problems[j].Line {
			return problems[i].Line < problems[j].Line
		}
		return problems[i].Message < problems[j].Message
	})
}

// checkCodes refuses a page code that two pages carry.
func checkCodes(t *Tree) []Problem {
	var problems []Problem
	first := map[string]string{}
	for _, p := range t.Pages {
		if prev, ok := first[p.Front.Code]; ok {
			problems = append(problems, Problem{Path: p.Path, Line: 1, Message: fmt.Sprintf("page code %s is already registered by %s", p.Front.Code, prev)})
			continue
		}
		first[p.Front.Code] = p.Path
	}
	return problems
}

// checkBlock validates one block against its kind's schema.
func checkBlock(b *Block, codes map[string]*Page) []Problem {
	var problems []Problem
	at := func(line int, format string, args ...any) {
		problems = append(problems, Problem{Path: b.Page.Path, Line: line, Message: fmt.Sprintf(format, args...)})
	}
	if len(b.fields) == 0 {
		return nil // the parse already reported why the block is empty
	}
	if b.Schema == nil {
		if b.Kind == "" {
			at(b.Line, "the block has no kind field")
		} else {
			at(b.lineOf("kind"), "unknown kind %q; the kinds are %s", b.Kind, kindNames())
		}
		return problems
	}
	for _, key := range sortedKeys(b.fields) {
		if key == "kind" {
			continue
		}
		f, ok := b.Schema.Field(key)
		if !ok {
			at(b.lineOf(key), "a %s block has no field %q", b.Kind, key)
			continue
		}
		for _, msg := range checkValue(f, b.fields[key], codes) {
			at(b.lineOf(key), "%s: %s", key, msg)
		}
	}
	for _, f := range b.Schema.Fields {
		if f.Required && !b.Has(f.Name) {
			at(b.Line, "a %s block requires the field %q", b.Kind, f.Name)
		}
	}
	problems = append(problems, checkKindRules(b)...)
	if b.Anchor != "" && b.anchorFound != b.Anchor {
		if b.anchorFound == "" {
			at(b.Line, "the block needs the anchor line <a id=%q></a> directly above it", b.Anchor)
		} else {
			at(b.anchorLine, "the anchor above the block is %q; its kind and name give %q", b.anchorFound, b.Anchor)
		}
	}
	return problems
}

// checkKindRules applies the rules that tie one field to another.
func checkKindRules(b *Block) []Problem {
	var problems []Problem
	at := func(line int, format string, args ...any) {
		problems = append(problems, Problem{Path: b.Page.Path, Line: line, Message: fmt.Sprintf(format, args...)})
	}
	if b.Name != "" && b.Anchor == "" {
		at(b.lineOf(b.Schema.NameField), "%s: %q holds no letter or digit, so it gives no anchor", b.Schema.NameField, b.Name)
	}
	switch b.Kind {
	case KindSetting:
		if b.Has("range") && b.Has("values") {
			at(b.lineOf("values"), "a setting carries range or values, not both")
		}
	case KindCustomResource:
		scope := b.Text("scope")
		if scope != "" && !strings.HasPrefix(scope, "Namespaced") && !strings.HasPrefix(scope, "Cluster") {
			at(b.lineOf("scope"), "scope: %q does not start with Namespaced or Cluster", scope)
		}
	case KindState:
		problems = append(problems, checkStates(b)...)
	}
	return problems
}

// checkStates refuses a state machine whose initial state, terminal
// states, or transitions name a state its states list lacks.
func checkStates(b *Block) []Problem {
	var problems []Problem
	states := map[string]bool{}
	for _, s := range b.List("states") {
		states[s] = true
	}
	unknown := func(field, state string) {
		problems = append(problems, Problem{Path: b.Page.Path, Line: b.lineOf(field), Message: fmt.Sprintf("%s: %q is not one of the states", field, state)})
	}
	if s := b.Text("initial"); s != "" && !states[s] {
		unknown("initial", s)
	}
	for _, s := range b.List("terminal") {
		if !states[s] {
			unknown("terminal", s)
		}
	}
	tr := b.fields["transitions"]
	if tr == nil || tr.Kind != yaml.SequenceNode {
		return problems
	}
	for _, t := range tr.Content {
		for _, key := range []string{"from", "to"} {
			for _, s := range listOf(mappingValue(t, key)) {
				if !states[s] {
					unknown("transitions", s)
				}
			}
		}
	}
	return problems
}

// checkValue validates one value against its field's type and returns the
// messages for what is wrong.
func checkValue(f Field, n *yaml.Node, codes map[string]*Page) []string {
	switch f.Type {
	case TypeString:
		if !isScalar(n) || strings.TrimSpace(n.Value) == "" {
			return []string{"must be a non-empty string"}
		}
	case TypeScalar:
		if !isScalar(n) {
			return []string{"must be a scalar value"}
		}
	case TypeInteger:
		if !isScalar(n) || n.Tag != "!!int" {
			return []string{"must be an integer"}
		}
	case TypeBoolean:
		if !isScalar(n) || n.Tag != "!!bool" {
			return []string{"must be true or false"}
		}
	case TypeEnum:
		if !isScalar(n) || !contains(f.Values, n.Value) {
			return []string{fmt.Sprintf("must be one of %s", strings.Join(f.Values, ", "))}
		}
	case TypeList:
		if !isScalarSequence(n) {
			return []string{"must be a list of values"}
		}
	case TypeScalarOrList:
		if !isScalar(n) && !isScalarSequence(n) {
			return []string{"must be a value or a list of values"}
		}
	case TypeEnumList:
		return checkEnumList(f, n)
	case TypeCode, TypeCodes:
		return checkCodeValue(f, n, codes)
	case TypeMapping:
		return checkMapping(n)
	case TypeTransitions:
		return checkTransitions(n)
	}
	return nil
}

func checkEnumList(f Field, n *yaml.Node) []string {
	if !isScalarSequence(n) || len(n.Content) == 0 {
		return []string{"must be a non-empty list"}
	}
	var msgs []string
	for _, c := range n.Content {
		if !contains(f.Values, c.Value) {
			msgs = append(msgs, fmt.Sprintf("%q is not one of %s", c.Value, strings.Join(f.Values, ", ")))
		}
	}
	return msgs
}

func checkCodeValue(f Field, n *yaml.Node, codes map[string]*Page) []string {
	var values []string
	switch {
	case isScalar(n):
		values = []string{n.Value}
	case f.Type == TypeCodes && isScalarSequence(n) && len(n.Content) > 0:
		values = listOf(n)
	default:
		if f.Type == TypeCode {
			return []string{"must be one page code"}
		}
		return []string{"must be a page code or a non-empty list of page codes"}
	}
	var msgs []string
	for _, v := range values {
		if _, ok := codes[v]; !ok {
			msgs = append(msgs, fmt.Sprintf("%q is not a page code any page of the tree registers", v))
		}
	}
	return msgs
}

func checkMapping(n *yaml.Node) []string {
	if n.Kind != yaml.MappingNode || len(n.Content) == 0 {
		return []string{"must be a non-empty mapping"}
	}
	for i := 1; i < len(n.Content); i += 2 {
		if !isScalar(n.Content[i]) {
			return []string{fmt.Sprintf("the value of %q must be a scalar", n.Content[i-1].Value)}
		}
	}
	return nil
}

func checkTransitions(n *yaml.Node) []string {
	if n.Kind != yaml.SequenceNode || len(n.Content) == 0 {
		return []string{"must be a non-empty list of transitions"}
	}
	var msgs []string
	for i, t := range n.Content {
		if t.Kind != yaml.MappingNode {
			msgs = append(msgs, fmt.Sprintf("transition %d must be a mapping with from and to", i+1))
			continue
		}
		for j := 0; j+1 < len(t.Content); j += 2 {
			if k := t.Content[j].Value; !contains([]string{"from", "to", "on", "rule"}, k) {
				msgs = append(msgs, fmt.Sprintf("transition %d has no field %q", i+1, k))
			}
		}
		from, to := mappingValue(t, "from"), mappingValue(t, "to")
		if from == nil || (!isScalar(from) && !isScalarSequence(from)) || to == nil || !isScalar(to) {
			msgs = append(msgs, fmt.Sprintf("transition %d needs from (a state or a list) and to (a state)", i+1))
		}
	}
	return msgs
}

// checkAnchors refuses an anchor that two blocks, terms, or requirements
// share, naming the first holder.
func checkAnchors(t *Tree) []Problem {
	type holder struct {
		path string
		line int
		what string
	}
	var all []holder
	for _, p := range t.Pages {
		for _, b := range p.Blocks {
			if b.Anchor != "" {
				all = append(all, holder{p.Path, b.Line, b.Anchor})
			}
		}
		for _, term := range p.Terms {
			all = append(all, holder{p.Path, term.Line, term.Anchor})
		}
		for _, r := range p.Requirements {
			all = append(all, holder{p.Path, r.Line, r.Anchor})
		}
	}
	var problems []Problem
	first := map[string]holder{}
	for _, h := range all {
		if prev, ok := first[h.what]; ok {
			problems = append(problems, Problem{Path: h.path, Line: h.line, Message: fmt.Sprintf("anchor %q is already defined at %s:%d", h.what, prev.path, prev.line)})
			continue
		}
		first[h.what] = h
	}
	return problems
}

func kindNames() string {
	names := make([]string, 0, len(kinds))
	for _, k := range Kinds() {
		names = append(names, k.Name)
	}
	return strings.Join(names, ", ")
}

func isScalar(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.Tag != "!!null"
}

func isScalarSequence(n *yaml.Node) bool {
	if n == nil || n.Kind != yaml.SequenceNode {
		return false
	}
	for _, c := range n.Content {
		if !isScalar(c) {
			return false
		}
	}
	return true
}

// listOf returns a scalar as a one-item list and a sequence's scalars.
func listOf(n *yaml.Node) []string {
	switch {
	case n == nil:
		return nil
	case n.Kind == yaml.ScalarNode:
		return []string{n.Value}
	case n.Kind == yaml.SequenceNode:
		out := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			out = append(out, c.Value)
		}
		return out
	}
	return nil
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]*yaml.Node) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
