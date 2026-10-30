// SPDX-License-Identifier: MIT

// Tier-11 reconciliation of the JSONL frame identifier across its three
// statements.
//
// Each JSON Lines frame on the adapter-to-runtime leg is stated three
// times: as a property of the published schema
// (schemas/lenny-adapter-jsonl.schema.json), as a canonical frame block
// in the specification's intra-pod contract cards, and as a reference
// section in docs/reference/adapter-contract.md. A runtime author reads
// the last two and a validator reads the first, so a frame that declares
// the per-session identifier in one carrier and omits it in another
// produces a runtime that validates and is rejected, or one that a
// reader writes without the identifier the adapter needs to address it.
//
// The predicate is driven off the schema rather than off a fixed list of
// frames, so a frame that gains or loses the property in the schema is
// reconciled without the gate being edited.
//
// spec: 28.5.3 (intra-pod frame addressing), 15.4 (sessionId on
// session-scoped frames)

package tier11_docs_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// frameIdentifierProperty is the per-session address every session-scoped
// frame carries.
const frameIdentifierProperty = "sessionId"

// jsonlEnvelopeDef is the schema definition of the inbound `message`
// frame, which the schema names for the envelope rather than for the
// frame type the other definitions are named for.
const jsonlEnvelopeDef = "messageEnvelope"

// jsonlNonFrameDefs are schema definitions that describe a member of a
// frame rather than a frame, so no contract card or reference section
// states them on their own.
var jsonlNonFrameDefs = map[string]bool{"from": true}

// jsonlSchema is the part of the published JSONL schema this gate reads.
type jsonlSchema struct {
	Defs map[string]struct {
		Properties map[string]json.RawMessage `json:"properties"`
	} `json:"$defs"`
}

// fencedJSON matches a fenced JSON block, which is how both the
// specification's frame blocks and the reference sections state a
// frame's fields.
var fencedJSON = regexp.MustCompile("(?s)```json\n(.*?)```")

// spec: 28.5.3, 15.4
// diagnosis: the published JSONL schema and one of the two prose
//
//	statements of the same frame disagree about the per-session
//	identifier. A frame the schema declares it on but the specification's
//	§28.5.3 block or the docs/reference/adapter-contract.md section omits
//	is a frame a runtime author writes unaddressed, which the adapter
//	rejects on a pod holding more than one slot; a frame the prose states
//	it on but the schema omits fails schema validation in the conformance
//	battery. A failure names the frame and the carrier that is behind.
func TestFrameIdentifierAgreesAcrossSchemaSpecAndReference(t *testing.T) {
	root := repoRoot(t)
	frames := jsonlFramesDeclaringIdentifier(t, root)
	if len(frames) == 0 {
		t.Fatal("schemas/lenny-adapter-jsonl.schema.json declares no frame carrying the per-session identifier (renamed or removed?)")
	}

	cards := specSection(t, filepath.Join(root, "spec", "28_communication-channels.md"), "28.5.3")
	reference := adapterContractDoc(t, root)

	// The frames are read from a map, so the walk is ordered by name: a run
	// that reports a divergence reports the same set of frames every time,
	// and no frame's reconciliation depends on where an earlier one failed.
	for _, frame := range sortedFrameNames(frames) {
		wantIdentifier := frames[frame]
		block, ok := frameContractCard(cards, frame)
		if !ok {
			t.Errorf("spec §28.5.3: no contract card for the %s frame (renamed or removed?)", frame)
			continue
		}
		if got := fencedJSONDeclares(block, frameIdentifierProperty); got != wantIdentifier {
			t.Errorf("spec §28.5.3 %s block declares %s = %t; the published JSONL schema declares it %t",
				frame, frameIdentifierProperty, got, wantIdentifier)
		}
		sec := section(reference, "`"+frame+"` ---")
		if sec == "" {
			t.Errorf("docs/reference/adapter-contract.md: no reference section for the %s frame (renamed or removed?)", frame)
			continue
		}
		if got := fencedJSONDeclares(sec, frameIdentifierProperty); got != wantIdentifier {
			t.Errorf("docs/reference/adapter-contract.md %s section declares %s = %t; the published JSONL schema declares it %t",
				frame, frameIdentifierProperty, got, wantIdentifier)
		}
	}
}

// jsonlFramesDeclaringIdentifier returns every frame the published JSONL
// schema states, mapped to whether that frame declares the per-session
// identifier. Both arms are returned so the gate reconciles the absence
// of the property as well as its presence: a frame that gains it in the
// prose alone is as much a divergence as one that loses it there.
func jsonlFramesDeclaringIdentifier(t *testing.T, root string) map[string]bool {
	t.Helper()
	path := filepath.Join(root, "schemas", "lenny-adapter-jsonl.schema.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var schema jsonlSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	frames := map[string]bool{}
	for name, def := range schema.Defs {
		if jsonlNonFrameDefs[name] {
			continue
		}
		frame := name
		if name == jsonlEnvelopeDef {
			frame = "message"
		}
		_, declared := def.Properties[frameIdentifierProperty]
		frames[frame] = declared
	}
	return frames
}

// frameCardLabel matches the bolded lead-in that opens one frame's
// contract card. The capture is the frame name.
var frameCardLabel = regexp.MustCompile("^\\*\\*(?:Inbound|Outbound): `([a-z_]+)`")

// boldedLeadIn matches any bolded lead-in in the section. A card ends at
// the next one of these rather than at the next frame label, because the
// prose that follows the last card (the addressing rules, the annotated
// protocol trace, the internal part format) also carries fenced JSON. A
// card delimited only by the next frame label would swallow all of it,
// and the identifier assertion for the last card would hold on a
// neighbour's fence whatever the card itself declares.
var boldedLeadIn = regexp.MustCompile(`^\*\*`)

// sortedFrameNames returns the frame names in name order.
func sortedFrameNames(frames map[string]bool) []string {
	names := make([]string, 0, len(frames))
	for name := range frames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// frameContractCard returns the §28.5.3 block for one frame and reports
// whether the section carries a card for it. The blocks are bold labels
// rather than headings, so the card runs from its own label to the next
// bolded lead-in, or to the end of the section when it is the last block
// in it. A missing card is returned as an absence rather than raised as
// a fatal, so the caller reports it beside every other frame's result
// instead of halting the run at whichever frame it reached first.
func frameContractCard(cards, frame string) (string, bool) {
	lines := strings.Split(cards, "\n")
	start := -1
	for i, ln := range lines {
		if start < 0 {
			m := frameCardLabel.FindStringSubmatch(ln)
			if m != nil && m[1] == frame {
				start = i
			}
			continue
		}
		if boldedLeadIn.MatchString(ln) {
			return strings.Join(lines[start:i], "\n"), true
		}
	}
	if start < 0 {
		return "", false
	}
	return strings.Join(lines[start:], "\n"), true
}

// fencedJSONDeclares reports whether any fenced JSON block in body
// declares the named property. The frames are stated as JSON schemas and
// JSON examples rather than as field tables, so the declaration is a
// quoted key inside a fence.
func fencedJSONDeclares(body, property string) bool {
	key := `"` + property + `"`
	for _, m := range fencedJSON.FindAllStringSubmatch(body, -1) {
		if strings.Contains(m[1], key) {
			return true
		}
	}
	return false
}

// spec: 28.5.3
// diagnosis: the §28.5.3 card delimiter runs past the end of a frame's
//
//	block and into the prose that follows it. The last labelled card in
//	the section has no frame label after it, so a delimiter that ends only
//	at the next frame label absorbs the addressing rules, the annotated
//	protocol trace, and the internal part format, every one of which
//	carries a fenced JSON block naming the per-session identifier. The
//	identifier assertion for that card then holds on a neighbour's fence
//	and stays green when the card itself loses the property. A failure
//	means the delimiter is reading content the card does not own.
func TestFrameContractCardEndsAtTheNextBoldedLeadIn(t *testing.T) {
	const cards = "**Outbound: `last_frame`**\n" +
		"\n" +
		"```json\n" +
		"{\"type\": \"last_frame\"}\n" +
		"```\n" +
		"\n" +
		"**Addressing.** Unrelated prose that follows the last card.\n" +
		"\n" +
		"```json\n" +
		"{\"type\": \"last_frame\", \"sessionId\": \"alice-1\"}\n" +
		"```\n"

	block, ok := frameContractCard(cards, "last_frame")
	if !ok {
		t.Fatal("the fixture card was not found")
	}
	if strings.Contains(block, "Unrelated prose") {
		t.Errorf("the last card absorbed the prose that follows it:\n%s", block)
	}
	if fencedJSONDeclares(block, frameIdentifierProperty) {
		t.Errorf("the last card's identifier assertion read a fence the card does not own:\n%s", block)
	}

	// The same boundary holds against the section as it stands: the card for
	// the last labelled frame stops before the addressing rules.
	cardsSection := specSection(t, filepath.Join(repoRoot(t), "spec", "28_communication-channels.md"), "28.5.3")
	live, ok := frameContractCard(cardsSection, "set_tracing_context")
	if !ok {
		t.Fatal("spec §28.5.3: no contract card for the set_tracing_context frame (renamed or removed?)")
	}
	if strings.Contains(live, "**Addressing.**") {
		t.Errorf("spec §28.5.3: the set_tracing_context card runs past its own block into the addressing rules:\n%s", live)
	}
}

// spec: 28.5.3
// diagnosis: a §28.5.3 card that is renamed or removed halts the
//
//	reconciliation instead of being reported as one divergence among the
//	rest. The frames come from a map, so a halt lands at an arbitrary
//	point and leaves an unknown, run-dependent subset of frames
//	unchecked, including the frame whose three statements the
//	reconciliation exists to hold in agreement. A failure means a missing
//	card is fatal again, or the walk is unordered.
func TestAMissingFrameContractCardIsReportedRatherThanHalting(t *testing.T) {
	const cards = "**Outbound: `present_frame`**\n" +
		"\n" +
		"```json\n" +
		"{\"type\": \"present_frame\", \"sessionId\": \"alice-1\"}\n" +
		"```\n"

	if _, ok := frameContractCard(cards, "absent_frame"); ok {
		t.Error("a frame with no card in the section was reported as carrying one")
	}
	if _, ok := frameContractCard(cards, "present_frame"); !ok {
		t.Error("a frame with a card in the section was reported as carrying none")
	}

	got := sortedFrameNames(map[string]bool{"status": true, "message": false, "absent_frame": true})
	want := []string{"absent_frame", "message", "status"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the reconciliation walks the frames as %v; want the stable order %v", got, want)
	}
}
