// SPDX-License-Identifier: MIT

// Tier-11 spec/code-consistency guard for the §12.6 / §25.3 CloudEvents
// type-alias reconciliation (proposal 0033, F-12.6.21). §12.6 and §25.3
// once mandated `type Event = cloudevents.Event` and
// `type OperationalEvent = cloudevents.Event`, but the code ships a
// native structured-content struct in each package because the released
// go-sdk serializes application/ocsf+json data as an escaped JSON string,
// which double-wraps the audit record and violates the single-envelope
// inline model. The proposal codified the native structs the code already
// ships. These tests pin that reconciliation so a later spec edit cannot
// silently re-introduce the go-sdk alias while the code keeps the native
// struct (the F-12.6.21 divergence), and so the spec struct field set does
// not drift from the shipped envelope-contract attributes.
//
// The tests read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks. They
// reuse repoRoot (docs_test.go) and specSection / requireNoneContain
// (budget_extension_trigger_consistency_test.go).
//
// spec: 12.6 (Event type), 25.3 (OperationalEvent type)

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// goBlockContaining returns the ```go ... ``` fenced code block within
// sec whose body contains anchor, so an assertion targets one Go
// declaration rather than the whole spec section. It fails the test when
// no such fenced block is present, so a moved or renamed declaration
// surfaces here rather than as a silently-empty match.
func goBlockContaining(t *testing.T, label, sec, anchor string) string {
	t.Helper()
	lines := strings.Split(sec, "\n")
	inBlock := false
	var buf []string
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if !inBlock {
			if trimmed == "```go" {
				inBlock = true
				buf = buf[:0]
			}
			continue
		}
		if trimmed == "```" {
			block := strings.Join(buf, "\n")
			if strings.Contains(block, anchor) {
				return block
			}
			inBlock = false
			continue
		}
		buf = append(buf, ln)
	}
	t.Fatalf("%s: no ```go``` block containing %q found (declaration moved or renamed?)", label, anchor)
	return ""
}

// spec: 12.6 (Event type), 25.3 (OperationalEvent type)
// diagnosis: §12.6 or §25.3 re-introduced the go-sdk CloudEvents alias
// (`= cloudevents.Event`) while the code ships a native structured-content
// struct — the F-12.6.21 divergence proposal 0033 closed. The released
// go-sdk serializes application/ocsf+json data as an escaped JSON string,
// double-wrapping the audit record and violating the single-envelope
// inline model both sections mandate. A failure here means the spec type
// declaration once again contradicts the wire-correct native struct the
// code ships; the alias mandate must not return.
func TestSpecEnvelopeTypesAreNativeStructs_F12621(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	// §12.6 EventBus Event block.
	s126 := specSection(t, filepath.Join(specDir, "12_storage-architecture.md"), "### 12.6 ")
	eventBlock := goBlockContaining(t, "§12.6 Event type", s126, "type Event struct")

	// §25.3 OperationalEvent block.
	s253 := specSection(t, filepath.Join(specDir, "25_agent-operability.md"), "## 25.3 ")
	opBlock := goBlockContaining(t, "§25.3 OperationalEvent type", s253, "type OperationalEvent struct")

	// The alias mandate must not return in either form.
	requireNoneContain(t, "§12.6 Event type", eventBlock, []string{
		"= cloudevents.Event",
		"type Event = cloudevents",
	})
	requireNoneContain(t, "§25.3 OperationalEvent type", opBlock, []string{
		"= cloudevents.Event",
		"type OperationalEvent = cloudevents",
	})

	// Each block must declare the native struct.
	if !strings.Contains(eventBlock, "type Event struct") {
		t.Errorf("§12.6 no longer declares `type Event struct`; the native structured-content struct must remain (F-12.6.21)")
	}
	if !strings.Contains(opBlock, "type OperationalEvent struct") {
		t.Errorf("§25.3 no longer declares `type OperationalEvent struct`; the native structured-content struct must remain (F-12.6.21)")
	}
}

// spec: 12.6 (Event type), 25.3 (OperationalEvent type)
// diagnosis: the §12.6 Event or §25.3 OperationalEvent struct in the spec
// drifted from the shipped envelope-contract attribute set. The spec
// struct is the sole spec basis for the corresponding native struct in
// pkg/gateway/storage/eventbus.Event and pkg/events.OperationalEvent; if
// the spec drops or renames a context attribute (or the OperationalEvent
// severity extension the event-buffer query filters on), the spec no
// longer describes the wire-correct code. A failure here means a struct
// field went missing from the spec relative to the envelope-contract
// table the code implements.
func TestSpecEnvelopeStructFieldsMatchEnvelopeContract_F12621(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	s126 := specSection(t, filepath.Join(specDir, "12_storage-architecture.md"), "### 12.6 ")
	eventBlock := goBlockContaining(t, "§12.6 Event type", s126, "type Event struct")

	s253 := specSection(t, filepath.Join(specDir, "25_agent-operability.md"), "## 25.3 ")
	opBlock := goBlockContaining(t, "§25.3 OperationalEvent type", s253, "type OperationalEvent struct")

	// The §12.6 Event struct field set mirrors the envelope-contract table
	// attributes plus the inline payload and the extension map, matching the
	// shipped pkg/gateway/storage/eventbus.Event. Assert each field-name JSON
	// tag is present (Extensions is `json:"-"`, so assert the Go field name).
	requireAllContain(t, "§12.6 Event struct fields", eventBlock, []string{
		`SpecVersion`, `json:"specversion"`,
		`ID`, `json:"id"`,
		`Source`, `json:"source"`,
		`Type`, `json:"type"`,
		`Time`, `json:"time"`,
		`DataContentType`, `json:"datacontenttype"`,
		`Subject`, `json:"subject"`,
		`Data`, `json:"data,omitempty"`,
		`Extensions`, `json:"-"`,
	})

	// The §25.3 OperationalEvent struct field set mirrors the shipped
	// pkg/events.OperationalEvent, including the `severity` extension the
	// event-buffer query filters on.
	requireAllContain(t, "§25.3 OperationalEvent struct fields", opBlock, []string{
		`ID`, `json:"id"`,
		`Source`, `json:"source,omitempty"`,
		`SpecVersion`, `json:"specversion"`,
		`Type`, `json:"type"`,
		`Subject`, `json:"subject,omitempty"`,
		`Time`,
		`Severity`, `json:"severity,omitempty"`,
		`DataContentType`, `json:"datacontenttype,omitempty"`,
		`Data`, `json:"data,omitempty"`,
		`Extensions`, `json:"-"`,
	})
}

// spec: 12.6 (Event type), 25.3 (OperationalEvent type)
// diagnosis: the CloudEvents go-sdk was added to the module graph. The
// proposal chose the native struct (Direction A) precisely to avoid this
// dependency: the released go-sdk double-wraps application/ocsf+json data,
// and the +json-honoring fix is unreleased. A `cloudevents` line in go.mod
// or go.sum means Direction B was taken without amending the spec, or a
// transitive pull re-introduced the SDK the native struct was built to
// avoid. The module graph must stay free of cloudevents.
func TestCloudEventsSDKAbsentFromModuleGraph_F12621(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"go.mod", "go.sum"} {
		body := readDoc(t, filepath.Join(root, name))
		if strings.Contains(strings.ToLower(body), "cloudevents") {
			t.Errorf("%s references cloudevents; the go-sdk must stay out of the module graph (Direction A, F-12.6.21)", name)
		}
	}
}
