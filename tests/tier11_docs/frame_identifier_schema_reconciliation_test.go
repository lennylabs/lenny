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
// spec: 4.6.1 (warm pool controller pod lifecycle), 4.6.2 (frame carriers),
// 28.5.3 (session-scoped frame addressing in the intra-pod contract cards)

package tier11_docs_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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

// spec: 4.6.1, 4.6.2, 28.5.3
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

	for frame, wantIdentifier := range frames {
		block := frameContractCard(t, cards, frame)
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

// frameContractCard returns the §28.5.3 block for one frame. The blocks
// are bold labels rather than headings, so the card runs from its own
// label to the next one.
func frameContractCard(t *testing.T, cards, frame string) string {
	t.Helper()
	lines := strings.Split(cards, "\n")
	label := regexp.MustCompile("^\\*\\*(Inbound|Outbound): `([a-z_]+)`")
	start := -1
	for i, ln := range lines {
		m := label.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		if start < 0 {
			if m[2] == frame {
				start = i
			}
			continue
		}
		return strings.Join(lines[start:i], "\n")
	}
	if start < 0 {
		t.Fatalf("spec §28.5.3: no contract card for the %s frame (renamed or removed?)", frame)
	}
	return strings.Join(lines[start:], "\n")
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
