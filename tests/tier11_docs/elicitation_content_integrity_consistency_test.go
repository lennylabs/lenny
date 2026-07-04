// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency check for the §9.2 elicitation
// content-integrity gateway-origin binding (proposal 0030, F-9.2.1).
// Proposal 0030 reconciled the §9.2 line-56 self-contradiction to the
// v1 server-internal resolution model: the gateway resolves the
// elicitation chain internally and delivers the recorded original to the
// resolver, no intermediate pod re-emits the {message, schema} payload,
// and the SHA-256 digest check is the forward-compatible enforcement
// point that is dormant until a per-hop re-emission wire mechanism
// exists. This test pins that reconciliation so a later spec edit cannot
// silently reintroduce the present-tense re-emission claim the code does
// not build.
//
// The test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: §9.2 (gateway-origin binding; v1 structural enforcement).

package tier11_docs_test

import (
	"path/filepath"
	"testing"
)

// spec: 9.2
// diagnosis: §9.2's gateway-origin-binding paragraph drifted back to the
// self-contradictory state F-9.2.1 named, asserting both that
// intermediate pods forward by elicitation_id only and that an
// intermediate pod re-emits the upstream elicitation/create frame
// carrying the {message, schema} payload. A failure here means the spec
// again presents the per-hop re-emission wire mechanism as present-tense
// v1 behavior the implementation does not build, so a reader cannot tell
// whether the content-integrity detector is a live control or a dormant
// forward-compatible enforcement point.
func TestSpecElicitationContentIntegrityReconciled_F921(t *testing.T) {
	root := repoRoot(t)
	s92 := specSection(t, filepath.Join(root, "spec", "09_mcp-integration.md"), "### 9.2 ")

	// The reconciled v1 model: the gateway resolves the chain internally
	// and delivers the recorded original to the resolver; no per-hop
	// re-emission wire mechanism exists; the digest check is the dormant
	// enforcement point.
	requireAllContain(t, "§9.2 gateway-origin binding", s92, []string{
		"forward elicitations upstream by `elicitation_id` only",
		"the gateway resolves the elicitation chain internally",
		"delivers the gateway-recorded original to that resolver",
		"no v1 wire path lets an intermediate hop substitute altered text",
		"v1 provides no per-hop re-emission wire mechanism",
		"the digest check is the dormant enforcement point",
	})

	// The removed present-tense re-emission sentence must not return; it
	// is the exact phrasing F-9.2.1 flagged as un-built v1 behavior. The
	// enforcement-mode bullets keep their own conditional phrasing, so
	// this banned string is scoped to the removed sentence only.
	requireNoneContain(t, "§9.2 gateway-origin binding", s92, []string{
		"re-emits the upstream `elicitation/create` frame carrying the original `{message, schema}` payload",
	})
}
