// SPDX-License-Identifier: MIT

// Tier-11 documentation checks that every protocol overview a runtime author
// reads opens a session with its `session_start` frame and ends it with its
// `session_end` frame.
//
// The adapter writes `session_start` before every other frame addressed to a
// session and `session_end` when the session ends, and `shutdown` is
// process-scoped: it is written when the pod drains, never at a session
// boundary. A protocol trace that begins a session at its first `message`, or
// ends it with `shutdown`, teaches the protocol in which a session lived as
// long as the process. A frame table that omits the session frames teaches
// the same thing by omission.
//
// spec: 28.5.3 (CH-MSGSOCK Inbound: session_start), 28.5.3 (CH-MSGSOCK
// Inbound: session_end), 28.5.3 (CH-MSGSOCK Outbound: session_started)

package tier11_docs_test

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	// traceFrameType matches the frame type a protocol-trace line carries.
	traceFrameType = regexp.MustCompile(`"type"\s*:\s*"([a-z_]+)"`)
	// traceSessionID matches the session a protocol-trace line addresses.
	traceSessionID = regexp.MustCompile(`"sessionId"\s*:\s*"([^"]+)"`)
)

// traceFrame is one frame named on one line of a protocol trace.
type traceFrame struct {
	line      int
	frameType string
	sessionID string
}

// parseTraceFrames returns the frames a fenced block names, in order.
func parseTraceFrames(body string) []traceFrame {
	var frames []traceFrame
	for i, line := range strings.Split(body, "\n") {
		m := traceFrameType.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		f := traceFrame{line: i + 1, frameType: m[1]}
		if s := traceSessionID.FindStringSubmatch(line); s != nil {
			f.sessionID = s[1]
		}
		frames = append(frames, f)
	}
	return frames
}

// isSessionTrace reports whether the frames describe a session from its
// content through the process's shutdown, which is what makes the order of
// the session frames checkable.
func isSessionTrace(frames []traceFrame) bool {
	var message, shutdown bool
	for _, f := range frames {
		message = message || f.frameType == "message"
		shutdown = shutdown || f.frameType == "shutdown"
	}
	return message && shutdown
}

// sessionTraceDefects returns every place a session trace departs from the
// session's frame order: a `message` for a session no earlier
// `session_start` opened, and a `shutdown` written while a session the trace
// opened has had no `session_end`.
func sessionTraceDefects(frames []traceFrame) []string {
	var defects []string
	// live holds every session the trace has addressed and not yet ended,
	// whether a `session_start` opened it or a `message` addressed it first.
	live := map[string]bool{}
	for _, f := range frames {
		switch f.frameType {
		case "session_start":
			live[f.sessionID] = true
		case "session_end":
			delete(live, f.sessionID)
		case "message":
			if !live[f.sessionID] {
				defects = append(defects, fmt.Sprintf("trace line %d: message for session %q precedes its session_start", f.line, f.sessionID))
				live[f.sessionID] = true
			}
		case "shutdown":
			for id := range live {
				defects = append(defects, fmt.Sprintf("trace line %d: shutdown ends session %q, which had no session_end", f.line, id))
			}
		}
	}
	return defects
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_start), 28.5.3 (CH-MSGSOCK
// Inbound: session_end)
// diagnosis: a protocol trace under docs/ opens a session at its first
//
//	`message` or ends it with the process-scoped `shutdown`. The adapter
//	writes `session_start` before every frame addressed to a session and
//	`session_end` when the session ends, so the trace teaches a runtime author
//	a protocol the adapter does not speak.
func TestDocsProtocolTracesOpenAndEndSessionsWithSessionFrames(t *testing.T) {
	docsDir := filepath.Join(repoRoot(t), "docs")
	traces := 0
	err := filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		blocks, err := extractFencedBlocksIncluding(path, true)
		if err != nil {
			return fmt.Errorf("extract fenced blocks from %s: %w", path, err)
		}
		for _, b := range blocks {
			frames := parseTraceFrames(b.Body)
			if !isSessionTrace(frames) {
				continue
			}
			traces++
			for _, defect := range sessionTraceDefects(frames) {
				t.Errorf("%s (block at line %d): %s", path, b.StartLine, defect)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", docsDir, err)
	}
	if traces == 0 {
		t.Fatalf("no session protocol trace found under %s; the check matched nothing", docsDir)
	}
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_start), 28.5.3 (CH-MSGSOCK
// Inbound: session_end)
// diagnosis: the session-trace checker no longer rejects a trace that begins
//
//	a session at its first `message` and ends it with `shutdown`, so the docs
//	sweep above passes vacuously.
func TestSessionTraceCheckerRejectsMessageFirstTrace(t *testing.T) {
	legacy := `STDIN  -> {"type":"message","id":"msg_001","sessionId":"sess_abc"}
STDOUT <- {"type":"response","sessionId":"sess_abc"}
STDIN  -> {"type":"shutdown","reason":"drain","deadline_ms":10000}
`
	frames := parseTraceFrames(legacy)
	if !isSessionTrace(frames) {
		t.Fatal("the legacy trace is not recognized as a session trace")
	}
	if got := len(sessionTraceDefects(frames)); got != 2 {
		t.Errorf("legacy trace: got %d defects, want 2 (missing session_start, missing session_end)", got)
	}

	current := `STDIN  -> {"type":"session_start","sessionId":"sess_abc","startId":"st_1"}
` + strings.Replace(legacy, `STDIN  -> {"type":"shutdown"`, `STDIN  -> {"type":"session_end","sessionId":"sess_abc"}
STDIN  -> {"type":"shutdown"`, 1)
	if defects := sessionTraceDefects(parseTraceFrames(current)); len(defects) != 0 {
		t.Errorf("current trace: unexpected defects %v", defects)
	}
}

// protocolOverviewPages are the pages that summarize the adapter-to-runtime
// frames in a table, and the frame rows each must carry.
var protocolOverviewPages = []struct {
	path string
	rows []string
}{
	{
		path: filepath.Join("docs", "tutorials", "build-a-runtime.md"),
		rows: []string{"| `session_start` |", "| `session_end` |", "| `session_started` |"},
	},
	{
		path: filepath.Join("docs", "api", "internal.md"),
		rows: []string{"| `session_start` |", "| `session_end` |", "| `session_started` |"},
	},
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_start), 28.5.3 (CH-MSGSOCK
// Inbound: session_end), 28.5.3 (CH-MSGSOCK Outbound: session_started)
// diagnosis: a page that summarizes the stdin and stdout frames in a table
//
//	omits a session frame. A reader of the table learns a protocol without
//	session boundaries, in which the first `message` opens a session and the
//	process exit ends it.
func TestProtocolOverviewTablesListTheSessionFrames(t *testing.T) {
	root := repoRoot(t)
	for _, page := range protocolOverviewPages {
		body := readDocPage(t, filepath.Join(root, page.path))
		for _, row := range page.rows {
			if !strings.Contains(body, row) {
				t.Errorf("%s: frame table has no row %q", page.path, row)
			}
		}
	}
	tutorial := readDocPage(t, filepath.Join(root, protocolOverviewPages[0].path))
	const link = "adapter-contract.md#inbound-messages-adapter-writes-to-your-stdin"
	if !strings.Contains(tutorial, link) {
		t.Errorf("%s: does not link the session frames to %s", protocolOverviewPages[0].path, link)
	}
}
