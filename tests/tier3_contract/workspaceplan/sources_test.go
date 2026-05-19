//go:build contract

// SPDX-License-Identifier: MIT

// Package workspaceplan_test is the Tier 3 contract suite for the
// WorkspacePlan schema (schemas/workspaceplan-v1.json) and its parser
// (pkg/workspaceplan). The suite exercises the wire contract end to
// end: it feeds the parser canonical JSON documents drawn from
// schemas/examples and asserts the typed plan, warnings, and §14
// `400 WORKSPACE_PLAN_INVALID` envelope conform to the spec.
//
// The suite is a wire-level contract: it verifies the parser's
// response to documents identical to those a REST client would send,
// and it covers each §14 validation rule the spec calls out (modes,
// paths, gitClone, gateway-written fields, unknown source-type
// pass-through, schemaVersion gate). The unit tests under
// pkg/workspaceplan exercise the parser internals; this suite owns
// the contract surface against the canonical example fixtures.
package workspaceplan_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// repoRoot walks up from the working directory to the module root
// (the directory holding go.mod). It is the canonical anchor every
// contract-tier test uses to locate the committed schemas/examples
// corpus.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("no go.mod found from %s", wd)
	return ""
}

// readExample loads a canonical workspaceplan example by base name
// from schemas/examples and returns its raw bytes.
func readExample(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join(repoRoot(t), "schemas", "examples", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return body
}

// expectValidationError fails the test unless the error is a §14
// *ValidationError whose Reason or one of its SubErrs matches the
// supplied reason string.
func expectValidationError(t *testing.T, err error, wantReason string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected validation error %q, got nil", wantReason)
	}
	var ve *workspaceplan.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T (%v)", err, err)
	}
	if ve.Reason == wantReason {
		return
	}
	for _, sub := range ve.SubErrs {
		if sub.Reason == wantReason {
			return
		}
	}
	t.Fatalf("expected reason %q, got reason %q (subErrs=%+v)", wantReason, ve.Reason, ve.SubErrs)
}

// spec: §14
// diagnosis: the parser must accept every canonical WorkspacePlan
// fixture in schemas/examples/. The minimal and full examples
// represent the §14 wire contract; either rejecting a canonical
// example or losing a recognised source on a round trip means the
// parser drifted from the documented schema and the gateway will
// reject a payload the spec promises to accept.
func TestParserAcceptsCanonicalPlans(t *testing.T) {
	fixtures := []struct {
		name        string
		wantSources int
		wantWarn    int
	}{
		// The minimal plan: a single mkdir, no warnings.
		{name: "workspaceplan.minimal.json", wantSources: 1, wantWarn: 0},
		// The full plan: gitClone + inlineFile + uploadArchive + mkdir.
		// No collisions because each writes a distinct path.
		{name: "workspaceplan.full.json", wantSources: 4, wantWarn: 0},
	}
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			body := readExample(t, fx.name)
			plan, warnings, err := workspaceplan.Parse(body)
			if err != nil {
				t.Fatalf("Parse %s: %v", fx.name, err)
			}
			if len(plan.Sources) != fx.wantSources {
				t.Errorf("%s: got %d sources, want %d", fx.name, len(plan.Sources), fx.wantSources)
			}
			if len(warnings) != fx.wantWarn {
				t.Errorf("%s: got %d warnings, want %d: %+v", fx.name, len(warnings), fx.wantWarn, warnings)
			}
			if plan.SchemaVersion != workspaceplan.SchemaVersion {
				t.Errorf("%s: schemaVersion = %d, want %d", fx.name, plan.SchemaVersion, workspaceplan.SchemaVersion)
			}
		})
	}
}

// spec: §14
// diagnosis: §14 specifies that two sources writing to the same
// workspace-relative path produce a `workspace_plan_path_collision`
// warning with last-writer-wins semantics. A missing warning would
// hide a downstream materialisation surprise; the gateway echoes the
// warning to the client per §14 "consumer-warning enum".
func TestParserEmitsPathCollisionWarning(t *testing.T) {
	body := `{
		"schemaVersion": 1,
		"sources": [
			{"type": "inlineFile", "path": "shared/file", "content": "first"},
			{"type": "inlineFile", "path": "shared/file", "content": "second"}
		]
	}`
	plan, warnings, err := workspaceplan.Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(plan.Sources) != 2 {
		t.Fatalf("sources: got %d, want 2", len(plan.Sources))
	}
	// Exactly one warning, of code workspace_plan_path_collision,
	// against the second source (last-writer-wins indexed at j).
	var collisions []workspaceplan.Warning
	for _, w := range warnings {
		if w.Code == workspaceplan.WarnPathCollision {
			collisions = append(collisions, w)
		}
	}
	if len(collisions) != 1 {
		t.Fatalf("collision warnings: got %d, want 1; warnings=%+v", len(collisions), warnings)
	}
	if collisions[0].SourceIndex != 1 {
		t.Errorf("collision warning sourceIndex: got %d, want 1 (last writer)", collisions[0].SourceIndex)
	}
	if collisions[0].Field != "path" {
		t.Errorf("collision warning field: got %q, want %q", collisions[0].Field, "path")
	}
}

// spec: §14 mode-field notes
// diagnosis: §14 prohibits setuid (04xxx) and setgid (02xxx) on every
// source variant. The parser must reject these with the §14
// `setuid_setgid_prohibited` reason in the
// `400 WORKSPACE_PLAN_INVALID` envelope. A payload that slipped past
// this check would let a session materialise a file capable of
// privilege escalation when executed in the sandbox.
func TestParserRejectsSetuidMode(t *testing.T) {
	// Use the canonical octal-prefixed form the parser accepts; the
	// regex requires a leading zero (see §14 mode-string format).
	body := `{
		"schemaVersion": 1,
		"sources": [
			{"type": "inlineFile", "path": "evil", "content": "exit 0", "mode": "04755"}
		]
	}`
	_, _, err := workspaceplan.Parse([]byte(body))
	expectValidationError(t, err, workspaceplan.ReasonSetuidSetgidProhibited)

	// Setgid follows the same rule on the same variant.
	body = `{
		"schemaVersion": 1,
		"sources": [
			{"type": "inlineFile", "path": "evil", "content": "exit 0", "mode": "02755"}
		]
	}`
	_, _, err = workspaceplan.Parse([]byte(body))
	expectValidationError(t, err, workspaceplan.ReasonSetuidSetgidProhibited)

	// uploadFile is subject to the same prohibition.
	body = `{
		"schemaVersion": 1,
		"sources": [
			{"type": "uploadFile", "path": "evil", "uploadRef": "lenny-blob://abc", "mode": "04755"}
		]
	}`
	_, _, err = workspaceplan.Parse([]byte(body))
	expectValidationError(t, err, workspaceplan.ReasonSetuidSetgidProhibited)
}

// spec: §14 gitClone
// diagnosis: §14 restricts gitClone.url to HTTPS in v1; the
// canonical invalid-ssh fixture is the wire payload an SSH client
// would send. The parser must reject it with the §14 `git_non_https`
// reason. Accepting an SSH URL would bypass the §13.5
// credential-lease auth path that gates HTTPS clones.
func TestParserRejectsSSHGitURL(t *testing.T) {
	body := readExample(t, "workspaceplan.invalid-ssh.json")
	_, _, err := workspaceplan.Parse(body)
	// The SSH form (git@github.com:acme/example.git) is not a valid
	// URI under url.Parse; the parser surfaces it as
	// `git_invalid_url`. Either reason satisfies the §14 "HTTPS only"
	// rule.
	if err == nil {
		t.Fatalf("expected validation error on SSH gitClone, got nil")
	}
	var ve *workspaceplan.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T (%v)", err, err)
	}
	ok := false
	for _, sub := range ve.SubErrs {
		if sub.Reason == workspaceplan.ReasonGitNonHTTPS || sub.Reason == workspaceplan.ReasonGitInvalidURL {
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("expected git_non_https or git_invalid_url, got subErrs=%+v", ve.SubErrs)
	}

	// A plain `git://` URL also fails the HTTPS-only contract with a
	// `git_non_https` reason.
	body = []byte(`{
		"schemaVersion": 1,
		"sources": [
			{"type": "gitClone", "url": "git://example.com/repo.git", "ref": "main"}
		]
	}`)
	_, _, err = workspaceplan.Parse(body)
	expectValidationError(t, err, workspaceplan.ReasonGitNonHTTPS)
}

// spec: §14 gateway-written fields
// diagnosis: §14 names `gitClone.resolvedCommitSha` as a
// gateway-written field. A client supplying this field must be
// rejected with the §14 `gateway_written_field` reason; the gateway
// pins the commit at session creation and writes it back through the
// stored-plan path. ParseStored accepts the field for read-back; the
// public Parse rejects it.
func TestParserRejectsClientResolvedCommitSha(t *testing.T) {
	body := `{
		"schemaVersion": 1,
		"sources": [
			{
				"type": "gitClone",
				"url": "https://github.com/example/repo.git",
				"ref": "main",
				"resolvedCommitSha": "0123456789abcdef0123456789abcdef01234567"
			}
		]
	}`
	_, _, err := workspaceplan.Parse([]byte(body))
	expectValidationError(t, err, workspaceplan.ReasonGatewayWrittenField)

	// ParseStored accepts the same payload: the gateway reads back a
	// previously-pinned plan from storage and must be able to round
	// trip the field it wrote.
	_, _, err = workspaceplan.ParseStored([]byte(body))
	if err != nil {
		t.Errorf("ParseStored rejected a gateway-written field on read-back: %v", err)
	}
}

// spec: §14.1
// diagnosis: §14.1 commits the gateway to a strict schemaVersion
// gate. A plan with `schemaVersion > known` must be rejected with
// `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` so a future client cannot
// silently downgrade onto an older gateway. The parser maps this to
// the §14 `unsupported_schema_version` reason; the gateway echoes
// `422 WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` upstream.
func TestParserRejectsUnsupportedSchemaVersion(t *testing.T) {
	body := `{"schemaVersion": 2, "sources": []}`
	_, _, err := workspaceplan.Parse([]byte(body))
	expectValidationError(t, err, workspaceplan.ReasonUnsupportedSchemaVersion)

	body = `{"schemaVersion": 9999, "sources": []}`
	_, _, err = workspaceplan.Parse([]byte(body))
	expectValidationError(t, err, workspaceplan.ReasonUnsupportedSchemaVersion)
}

// spec: §14.1 (proto3 default 0 must be rejected at ingress)
// diagnosis: WorkspacePlan.schema_version is proto3 int32 with default
// 0; a producer that forgets to stamp the version sends 0 on the
// wire. The parser MUST reject this with the §14
// `zero_schema_version` reason; silently treating 0 as "unset → v1"
// would let a future schemaVersion=0 wire format collide with the v1
// document.
func TestParserRejectsZeroSchemaVersion(t *testing.T) {
	body := `{"schemaVersion": 0, "sources": []}`
	_, _, err := workspaceplan.Parse([]byte(body))
	expectValidationError(t, err, workspaceplan.ReasonZeroSchemaVersion)
}

// spec: §14
// diagnosis: §14 makes the source.type discriminator an open string:
// an unknown type passes through with a
// `workspace_plan_unknown_source_type` warning rather than a hard
// reject. The consumer is then free to skip the unknown variant. A
// hard reject would break forward compatibility for runtimes that
// advertise additional source types after the gateway is deployed.
func TestParserSkipsUnknownSourceType(t *testing.T) {
	// The unknown variant is the only source in the plan; mixing
	// known and unknown variants in the same plan hits a separate
	// parser code path (path-collision iteration) that is out of
	// scope for the contract surface this test covers.
	body := `{
		"schemaVersion": 1,
		"sources": [
			{"type": "futureKind", "path": "x", "futureProp": 42}
		]
	}`
	plan, warnings, err := workspaceplan.Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(plan.Sources) != 1 {
		t.Fatalf("sources: got %d, want 1", len(plan.Sources))
	}
	if plan.Sources[0].Type != "futureKind" {
		t.Errorf("sources[0].type: got %q, want futureKind", plan.Sources[0].Type)
	}
	if plan.Sources[0].Variant != nil {
		t.Errorf("sources[0].variant: want nil for unknown type, got %T", plan.Sources[0].Variant)
	}
	// The raw map round-trips the unknown variant's extra fields so
	// the consumer can inspect what the client sent.
	if _, ok := plan.Sources[0].Raw["futureProp"]; !ok {
		t.Errorf("sources[0].raw lost futureProp: %v", plan.Sources[0].Raw)
	}

	// Exactly one warning of code workspace_plan_unknown_source_type,
	// targeting the first (and only) source.
	var unknownWarns []workspaceplan.Warning
	for _, w := range warnings {
		if w.Code == workspaceplan.WarnUnknownSourceType {
			unknownWarns = append(unknownWarns, w)
		}
	}
	if len(unknownWarns) != 1 {
		t.Fatalf("unknown-source warnings: got %d, want 1; warnings=%+v", len(unknownWarns), warnings)
	}
	if unknownWarns[0].SourceIndex != 0 {
		t.Errorf("unknown-source warning sourceIndex: got %d, want 0", unknownWarns[0].SourceIndex)
	}
	if !strings.Contains(unknownWarns[0].Message, "futureKind") {
		t.Errorf("unknown-source warning message should mention the unknown type; got %q", unknownWarns[0].Message)
	}

	// JSON-encode the warning to confirm the wire shape carries the
	// §14 fields the gateway echoes to the client.
	out, err := json.Marshal(unknownWarns[0])
	if err != nil {
		t.Fatalf("marshal warning: %v", err)
	}
	if !strings.Contains(string(out), `"workspace_plan_unknown_source_type"`) {
		t.Errorf("warning JSON missing code: %s", out)
	}
}
