// SPDX-License-Identifier: MIT

package tier0_static

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// degradationEnvelopeCitationFiles are the source files whose doc
// comments describe the canonical degradation envelope defined in
// pkg/ops/conventions (the Degradation struct, its DegradationLevel and
// ThresholdSource fields) or embed that envelope in a response type.
var degradationEnvelopeCitationFiles = []string{
	"pkg/ops/conventions/conventions.go",
	"pkg/gateway/operability/health/service.go",
	"pkg/gateway/operability/recommendations/service.go",
	"tests/tier3_contract/ops_endpoints/diagnostics_schema_test.go",
	"tests/tier8_chaos/store_failure_test.go",
}

// staleDegradationLineCitation matches the specific stale line-number
// citation this guard exists to catch: an old line number for the
// Canonical Degradation Envelope JSON example that has since moved.
var staleDegradationLineCitation = regexp.MustCompile(`§25\.4 line 215`)

// staleSectionCitation matches any §25.4 section reference.
var staleSectionCitation = regexp.MustCompile(`§25\.4\b`)

// commentLine matches a Go line comment, capturing indentation.
var commentLine = regexp.MustCompile(`^\s*//`)

// degradationEnvelopeCommentBlocks splits a file's contiguous `//`
// comment runs into blocks (joining each run's lines with a space) and
// returns each block's text alongside the line number it starts on.
func degradationEnvelopeCommentBlocks(t *testing.T, path string) []struct {
	startLine int
	text      string
} {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	var blocks []struct {
		startLine int
		text      string
	}
	var cur strings.Builder
	curStart := 0
	lineNo := 0
	flush := func() {
		if cur.Len() > 0 {
			blocks = append(blocks, struct {
				startLine int
				text      string
			}{curStart, cur.String()})
			cur.Reset()
		}
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if commentLine.MatchString(line) {
			if cur.Len() == 0 {
				curStart = lineNo
			}
			cur.WriteString(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "//")))
			cur.WriteString(" ")
			continue
		}
		flush()
	}
	flush()
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return blocks
}

// spec: §25.2 (Architecture Overview, API Conventions, Canonical
//
//	Degradation Envelope — "Any response whose data quality depends on
//	the availability of an external dependency, or on in-process history
//	having accumulated ... includes a top-level `degradation` object
//	with a uniform schema"; the envelope, its `level` field, and its
//	`thresholdSource` field are all defined in this subsection of §25.2,
//	not in §25.4 "The lenny-ops Service")
//
// diagnosis: A doc comment describing the canonical degradation
//
//	envelope (the Degradation struct, DegradationLevel, or
//	ThresholdSource in pkg/ops/conventions, or a response field that
//	embeds *conventions.Degradation) that cites §25.4 sends a reader to
//	"The lenny-ops Service" section, which does not define the envelope.
//	The envelope's JSON schema and field semantics live under §25.2's
//	"API Conventions" subsection. This guard fails if any such comment
//	names §25.4, whether as a bare section reference or as a
//	line-numbered `spec:` citation.
func TestDegradationEnvelopeCitationsResolveToArchitectureOverview(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	var bad []string
	for _, rel := range degradationEnvelopeCitationFiles {
		path := filepath.Join(root, rel)
		for _, block := range degradationEnvelopeCommentBlocks(t, path) {
			lower := strings.ToLower(block.text)
			describesEnvelope := strings.Contains(lower, "degradation") && strings.Contains(lower, "envelope")
			citesStaleLine := staleDegradationLineCitation.MatchString(block.text)
			if !citesStaleLine && !describesEnvelope {
				continue
			}
			if staleSectionCitation.MatchString(block.text) {
				bad = append(bad, fmt.Sprintf("%s:%d: %s", rel, block.startLine, block.text))
			}
		}
	}
	if len(bad) > 0 {
		t.Errorf("canonical degradation envelope citations must resolve to §25.2 (Architecture Overview, API Conventions), not §25.4 (The lenny-ops Service):\n%s",
			strings.Join(bad, "\n"))
	}
}
