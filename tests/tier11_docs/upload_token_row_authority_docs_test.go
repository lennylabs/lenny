// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for the client pages that describe the
// uploadToken after the session row became its authority: the token is a
// random secret rather than a signed value, UPLOAD_TOKEN_INVALID is
// catalogued, and a valid token on a session past finalization answers by
// the session's state. No external infrastructure.

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestUploadTokenDocsDescribeARandomSecret_spec_7_1 asserts the pages that
// state the token format name the secret segment and carry no retired
// signature wording. wire-format.md keeps HMAC-SHA256 for the webhook
// signature, so only hmac_hex is absent from that page.
//
// diagnosis: A failure means a reader-facing page still describes the
// uploadToken as an HMAC-signed value, or the format line lost the
// secret_hex segment.
//
// spec: 7.1 (uploadToken format and security properties)
func TestUploadTokenDocsDescribeARandomSecret_spec_7_1(t *testing.T) {
	const format = "`<session_id>.<expiry_unix_seconds>.<secret_hex>`"
	runDocPageChecks(t, []docPageCheck{
		{
			page:       "client-guide/session-lifecycle.md",
			contains:   []string{"The `uploadToken` is a short-lived, session-scoped secret."},
			notContain: []string{"hmac_hex", "HMAC-SHA256"},
		},
		{
			page:       "client-guide/wire-format.md",
			contains:   []string{format},
			notContain: []string{"hmac_hex"},
		},
		{
			page:       "reference/glossary.md",
			contains:   []string{"A short-lived, session-scoped secret issued at session creation.", format},
			notContain: []string{"hmac_hex", "HMAC-SHA256"},
		},
	})
}

// TestUploadTokenInvalidCatalogRows_spec_15_1 asserts both error tables
// carry the UPLOAD_TOKEN_INVALID row with its category and status, and that
// the REST finalize table lists every token code with its status.
//
// diagnosis: A failure means a client cannot look up the code a refused
// upload or finalize returns, or the catalog rows drifted from the §15.1
// status.
//
// spec: 15.1 (Error code catalog), 7.1 (Authentication)
func TestUploadTokenInvalidCatalogRows_spec_15_1(t *testing.T) {
	const meaning = "does not match the digest recorded on the session"
	runDocPageChecks(t, []docPageCheck{
		{
			page:     "reference/error-catalog.md",
			row:      "| `UPLOAD_TOKEN_INVALID` |",
			contains: []string{"| 401 |", meaning},
		},
		{
			page:     "client-guide/error-handling.md",
			row:      "| `UPLOAD_TOKEN_INVALID` |",
			contains: []string{"| `PERMANENT` | 401 | No |", meaning},
		},
		{
			page:     "api/rest.md",
			row:      "| `UPLOAD_TOKEN_INVALID` |",
			contains: []string{"| 401 |", meaning},
		},
		{
			page:     "api/rest.md",
			row:      "| `UPLOAD_TOKEN_MISMATCH` |",
			contains: []string{"| 403 |"},
		},
		{
			page:     "api/rest.md",
			row:      "| `UPLOAD_TOKEN_CONSUMED` |",
			contains: []string{"| 410 |"},
		},
		{
			page:     "api/rest.md",
			row:      "| `UPLOAD_TOKEN_EXPIRED` |",
			contains: []string{"| 401 |"},
		},
	})
}

// TestUploadTokenPrecedenceDocs_spec_15_1 asserts the 409 rows name the
// states that reach the precondition and send a consumed token to 410, and
// that the lifecycle page states that a token error precedes the 409. The
// mcp.md rows repeat across the upload_files and finalize_workspace tables,
// so they are counted rather than matched one line at a time.
//
// diagnosis: A failure means a reference table still tells a client that
// every session outside created answers 409, which §7.1 contradicts for
// ready and later non-terminal states.
//
// spec: 7.1 (Single-use invalidation, Check order), 15.1 (State-mutating
// endpoint preconditions)
func TestUploadTokenPrecedenceDocs_spec_15_1(t *testing.T) {
	const statesRow = "The session is finalizing or has ended. A valid token on a session in `ready` or a later non-terminal state returns `UPLOAD_TOKEN_CONSUMED`."
	runDocPageChecks(t, []docPageCheck{
		{
			page:     "client-guide/session-lifecycle.md",
			contains: []string{"Upload and finalize requests check the `uploadToken` first, so a token error takes precedence over this response."},
		},
		{
			page:       "api/rest.md",
			row:        "| `INVALID_STATE_TRANSITION` | 409 | The session is finalizing",
			contains:   []string{statesRow},
			notContain: []string{"is not in `created`"},
		},
		{
			page:       "api/mcp.md",
			notContain: []string{"Session already finalized or started", "Session not in `created` state"},
		},
	})
	mcp := readDocPage(t, filepath.Join(repoRoot(t), "docs", "api", "mcp.md"))
	for code, want := range map[string]int{
		statesRow:                     2,
		"| `UPLOAD_TOKEN_INVALID` |":  2,
		"| `UPLOAD_TOKEN_EXPIRED` |":  2,
		"| `UPLOAD_TOKEN_MISMATCH` |": 2,
		"| `UPLOAD_TOKEN_CONSUMED` |": 2,
	} {
		if got := strings.Count(mcp, code); got != want {
			t.Errorf("docs/api/mcp.md: %q appears %d times, want %d", code, got, want)
		}
	}
}
