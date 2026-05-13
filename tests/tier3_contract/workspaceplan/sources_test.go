//go:build contract

// SPDX-License-Identifier: MIT

// Package workspaceplan_test is the Tier 3 contract suite for the
// WorkspacePlan schema (schemas/workspaceplan-v1.json). Phase 1 ships
// the schema; Phase 2 implements the gateway-side WorkspacePlan parser
// and validator that this suite exercises end-to-end via REST. The
// stubs below fail with diagnoses until that parser exists.
package workspaceplan_test

import (
	"testing"
)

// spec: 14
// diagnosis: pkg/workspaceplan/ does not yet exist. Phase 2 implements
//
//	a parser that consumes a WorkspacePlan JSON document and
//	returns a typed plan, materialising sources in the
//	declared order. This test will round-trip every example
//	under schemas/examples/workspaceplan.*.json through the
//	parser.
func TestParserAcceptsCanonicalPlans(t *testing.T) {
	t.Fatalf("not implemented in Phase 1: pkg/workspaceplan/ parser is a Phase 2 deliverable per spec §14")
}

// spec: 14
// diagnosis: Path-collision warnings (`workspace_plan_path_collision`)
//
//	must be emitted when two sources write to the same path.
//	Last-writer-wins. Phase 2 ships the collision detector.
func TestParserEmitsPathCollisionWarning(t *testing.T) {
	t.Fatalf("not implemented in Phase 1: collision warning ships in Phase 2 per spec §14")
}

// spec: 14
// diagnosis: Setuid/setgid file modes must be rejected with
//
//	400 WORKSPACE_PLAN_INVALID, reason `setuid_setgid_prohibited`.
//	Sticky-on-file likewise. Tier 0 schema validation already
//	rejects on shape; this test exercises the gateway's
//	full-validation layer where the error code is attached.
func TestParserRejectsSetuidMode(t *testing.T) {
	t.Fatalf("not implemented in Phase 1: error-code attachment is a Phase 2 deliverable per spec §14")
}

// spec: 14
// diagnosis: gitClone.url must reject non-HTTPS protocols. Tier 0
//
//	schema validation rejects on pattern; this test exercises
//	the gateway's reason-code attachment.
func TestParserRejectsSSHGitURL(t *testing.T) {
	t.Fatalf("not implemented in Phase 1: gitClone protocol guard is a Phase 2 deliverable per spec §14")
}

// spec: 14
// diagnosis: A gitClone with resolvedCommitSha set in the request must be
//
//	rejected with 400 WORKSPACE_PLAN_INVALID,
//	reason `gateway_written_field`.
func TestParserRejectsClientResolvedCommitSha(t *testing.T) {
	t.Fatalf("not implemented in Phase 1: resolvedCommitSha guard is a Phase 2 deliverable per spec §14")
}

// spec: 14.1
// diagnosis: A plan with schemaVersion > known must be rejected with
//
//	422 WORKSPACE_PLAN_SCHEMA_UNSUPPORTED. Phase 2 ships the
//	version-gate.
func TestParserRejectsUnsupportedSchemaVersion(t *testing.T) {
	t.Fatalf("not implemented in Phase 1: schema-version gate ships in Phase 2 per spec §14.1")
}

// spec: 14
// diagnosis: An unknown source.type must be skipped, not rejected, with a
//
//	workspace_plan_unknown_source_type warning emitted. Phase 2
//	ships the open-string discriminator with pass-through.
func TestParserSkipsUnknownSourceType(t *testing.T) {
	t.Fatalf("not implemented in Phase 1: unknown-type pass-through ships in Phase 2 per spec §14")
}
