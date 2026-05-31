// SPDX-License-Identifier: MIT

package workspaceplan_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// spec: §14 sources / mode-field notes / §14.1 schema versioning.

func parse(t *testing.T, body string) (workspaceplan.Plan, []workspaceplan.Warning, error) {
	t.Helper()
	return workspaceplan.Parse([]byte(body))
}

func expectErr(t *testing.T, err error, wantReason string) *workspaceplan.ValidationError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected validation error %q, got nil", wantReason)
	}
	var ve *workspaceplan.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T (%v)", err, err)
	}
	if ve.Reason != wantReason && !subErrContainsReason(ve.SubErrs, wantReason) {
		t.Fatalf("expected reason %q, got reason %q (subErrs=%+v)", wantReason, ve.Reason, ve.SubErrs)
	}
	return ve
}

func subErrContainsReason(errs []workspaceplan.SubErr, reason string) bool {
	for _, e := range errs {
		if e.Reason == reason {
			return true
		}
	}
	return false
}

func TestParseHappyPath(t *testing.T) {
	body := `{
		"schemaVersion": 1,
		"sources": [
			{"type": "inlineFile", "path": "main.go", "content": "package main"},
			{"type": "mkdir", "path": "build", "mode": "01755"},
			{"type": "uploadFile", "path": "data.bin", "uploadRef": "lenny-blob://abc"},
			{"type": "uploadArchive", "pathPrefix": "vendor", "uploadRef": "lenny-blob://xyz", "format": "tar.gz", "stripComponents": 1},
			{"type": "gitClone", "url": "https://github.com/example/repo.git", "ref": "main"}
		]
	}`
	plan, warnings, err := parse(t, body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if plan.SchemaVersion != 1 {
		t.Errorf("schemaVersion: got %d", plan.SchemaVersion)
	}
	if len(plan.Sources) != 5 {
		t.Fatalf("sources: got %d, want 5", len(plan.Sources))
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %+v", warnings)
	}

	if inline, ok := plan.Sources[0].Variant.(workspaceplan.InlineFile); !ok || inline.PathField != "main.go" {
		t.Errorf("sources[0] is not InlineFile{main.go}: %+v", plan.Sources[0].Variant)
	}
	if mkdir, ok := plan.Sources[1].Variant.(workspaceplan.Mkdir); !ok || mkdir.Mode != "01755" {
		t.Errorf("sources[1] sticky-bit dir mode: %+v", plan.Sources[1].Variant)
	}
	if archive, ok := plan.Sources[3].Variant.(workspaceplan.UploadArchive); !ok || archive.StripComponents != 1 || archive.Format != "tar.gz" {
		t.Errorf("sources[3] archive: %+v", plan.Sources[3].Variant)
	}
	if g, ok := plan.Sources[4].Variant.(workspaceplan.GitClone); !ok || g.URL != "https://github.com/example/repo.git" || g.Ref != "main" {
		t.Errorf("sources[4] gitClone: %+v", plan.Sources[4].Variant)
	}
}

func TestParseRejectsMalformedJSON(t *testing.T) {
	_, _, err := parse(t, "not-json")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRejectsMissingSchemaVersion(t *testing.T) {
	_, _, err := parse(t, `{"sources": []}`)
	expectErr(t, err, workspaceplan.ReasonMissingRequired)
}

func TestParseRejectsZeroSchemaVersion(t *testing.T) {
	_, _, err := parse(t, `{"schemaVersion": 0, "sources": []}`)
	expectErr(t, err, workspaceplan.ReasonZeroSchemaVersion)
}

func TestParseRejectsUnsupportedHigherSchemaVersion(t *testing.T) {
	_, _, err := parse(t, `{"schemaVersion": 99, "sources": []}`)
	expectErr(t, err, workspaceplan.ReasonUnsupportedSchemaVersion)
}

// spec: §14.1 line 326 — the unsupported-schemaVersion error must carry
// the version pair so the gateway can echo details.knownVersion /
// details.encounteredVersion on the 422 envelope. A reason that is not
// "too new" (e.g. a negative version → invalid) must leave them nil so
// the gateway does not emit a misleading version pair. F-14.1.1.
func TestUnsupportedSchemaVersionCarriesVersionPair_spec_14_1_326(t *testing.T) {
	_, _, err := parse(t, `{"schemaVersion": 99, "sources": []}`)
	var ve *workspaceplan.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T (%v)", err, err)
	}
	if ve.KnownVersion == nil || *ve.KnownVersion != workspaceplan.SchemaVersion {
		t.Errorf("KnownVersion = %v, want %d", ve.KnownVersion, workspaceplan.SchemaVersion)
	}
	if ve.EncounteredVersion == nil || *ve.EncounteredVersion != 99 {
		t.Errorf("EncounteredVersion = %v, want 99", ve.EncounteredVersion)
	}

	// A negative version is an invalid (not unsupported) plan; the version
	// pair must stay nil.
	_, _, err = parse(t, `{"schemaVersion": -1, "sources": []}`)
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T (%v)", err, err)
	}
	if ve.KnownVersion != nil || ve.EncounteredVersion != nil {
		t.Errorf("invalid (negative) schemaVersion must not carry a version pair; got known=%v encountered=%v", ve.KnownVersion, ve.EncounteredVersion)
	}
}

func TestParseRejectsNegativeSchemaVersion(t *testing.T) {
	_, _, err := parse(t, `{"schemaVersion": -1, "sources": []}`)
	expectErr(t, err, workspaceplan.ReasonInvalidSchemaVersion)
}

func TestParseAcceptsEmptySources(t *testing.T) {
	plan, warns, err := parse(t, `{"schemaVersion": 1, "sources": []}`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(plan.Sources) != 0 || len(warns) != 0 {
		t.Errorf("plan should be empty: %+v / warns: %+v", plan, warns)
	}
}

func TestParseSkipsUnknownSourceTypeWithWarning(t *testing.T) {
	body := `{"schemaVersion": 1, "sources": [
		{"type": "ferrousMode", "magicNumber": 42}
	]}`
	plan, warns, err := parse(t, body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(plan.Sources) != 1 || plan.Sources[0].Variant != nil {
		t.Errorf("unknown variant kept as Source with nil Variant: %+v", plan.Sources)
	}
	if len(warns) != 1 || warns[0].Code != workspaceplan.WarnUnknownSourceType {
		t.Errorf("expected unknown-type warning, got %+v", warns)
	}
}

// spec: §14 line 334 — `workspace_plan_unknown_source_type` carries
// `schemaVersion` and `unknownType` as structured fields, not only in
// the free-form message. F-14.1.18.
func TestUnknownSourceTypeWarningCarriesStructuredFields_spec_14_334(t *testing.T) {
	body := `{"schemaVersion": 1, "sources": [
		{"type": "ferrousMode", "magicNumber": 42}
	]}`
	_, warns, err := parse(t, body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("warns: got %d, want 1", len(warns))
	}
	w := warns[0]
	if w.SchemaVersion == nil || *w.SchemaVersion != 1 {
		t.Errorf("warn.SchemaVersion = %v, want *1", w.SchemaVersion)
	}
	if w.UnknownType != "ferrousMode" {
		t.Errorf("warn.UnknownType = %q, want ferrousMode", w.UnknownType)
	}
}

// spec: §14 line 334 — JSON marshalling preserves the structured
// fields so downstream consumers reading the wire format can extract
// `schemaVersion` and `unknownType` without parsing the message. The
// other warning codes must not leak the unknown-source-type fields.
// F-14.1.18.
func TestUnknownSourceTypeWarningJSONRoundTrip_spec_14_334(t *testing.T) {
	body := `{"schemaVersion": 1, "sources": [
		{"type": "ferrousMode"}
	]}`
	_, warns, err := parse(t, body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	encoded, err := json.Marshal(warns[0])
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"schemaVersion":1`) {
		t.Errorf("encoded warn missing schemaVersion: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"unknownType":"ferrousMode"`) {
		t.Errorf("encoded warn missing unknownType: %s", encoded)
	}
	// Path-collision-only fields must not be present on this code.
	if strings.Contains(string(encoded), `"winningSourceIndex"`) {
		t.Errorf("encoded unknown-source-type warn leaked path-collision fields: %s", encoded)
	}
}

func TestParseRejectsMissingType(t *testing.T) {
	body := `{"schemaVersion": 1, "sources": [{"path": "x"}]}`
	_, _, err := parse(t, body)
	expectErr(t, err, workspaceplan.ReasonMissingRequired)
}

func TestParseRejectsExtraFieldsOnKnownVariant(t *testing.T) {
	body := `{"schemaVersion": 1, "sources": [
		{"type": "inlineFile", "path": "x", "content": "y", "extraField": 42}
	]}`
	_, _, err := parse(t, body)
	expectErr(t, err, workspaceplan.ReasonUnknownField)
}

func TestParseInlineFileDefaultsMode(t *testing.T) {
	plan, _, err := parse(t, `{"schemaVersion": 1, "sources":[{"type":"inlineFile","path":"a","content":"b"}]}`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	inline := plan.Sources[0].Variant.(workspaceplan.InlineFile)
	if inline.Mode != "0644" {
		t.Errorf("default inlineFile mode: got %q, want 0644", inline.Mode)
	}
}

func TestParseMkdirDefaultsMode(t *testing.T) {
	plan, _, err := parse(t, `{"schemaVersion": 1, "sources":[{"type":"mkdir","path":"d"}]}`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	mkdir := plan.Sources[0].Variant.(workspaceplan.Mkdir)
	if mkdir.Mode != "0755" {
		t.Errorf("default mkdir mode: got %q, want 0755", mkdir.Mode)
	}
}

func TestParseRejectsInvalidModeFormat(t *testing.T) {
	cases := []string{
		`644`, `rw-r--r--`, `0o644`, `0x1A4`, `garbage`, `0888`, // bad octal digit
	}
	for _, m := range cases {
		body := `{"schemaVersion": 1, "sources":[{"type":"inlineFile","path":"a","content":"b","mode":"` + m + `"}]}`
		_, _, err := parse(t, body)
		expectErr(t, err, workspaceplan.ReasonInvalidModeFormat)
	}
}

func TestParseRejectsSetuidMode(t *testing.T) {
	// 04xxx setuid prohibited; 06xxx setuid+setgid prohibited.
	for _, m := range []string{"04755", "05755", "06755", "07755"} {
		body := `{"schemaVersion": 1, "sources":[{"type":"inlineFile","path":"a","content":"b","mode":"` + m + `"}]}`
		_, _, err := parse(t, body)
		expectErr(t, err, workspaceplan.ReasonSetuidSetgidProhibited)
	}
}

func TestParseRejectsSetgidMode(t *testing.T) {
	for _, m := range []string{"02755", "03755"} {
		body := `{"schemaVersion": 1, "sources":[{"type":"mkdir","path":"a","mode":"` + m + `"}]}`
		_, _, err := parse(t, body)
		expectErr(t, err, workspaceplan.ReasonSetuidSetgidProhibited)
	}
}

func TestParseRejectsStickyOnFile(t *testing.T) {
	for _, ty := range []string{"inlineFile", "uploadFile"} {
		var body string
		switch ty {
		case "inlineFile":
			body = `{"schemaVersion": 1, "sources":[{"type":"inlineFile","path":"a","content":"b","mode":"01644"}]}`
		case "uploadFile":
			body = `{"schemaVersion": 1, "sources":[{"type":"uploadFile","path":"a","uploadRef":"r","mode":"01644"}]}`
		}
		_, _, err := parse(t, body)
		expectErr(t, err, workspaceplan.ReasonStickyOnFileProhibited)
	}
}

func TestParseAcceptsStickyOnMkdir(t *testing.T) {
	body := `{"schemaVersion": 1, "sources":[{"type":"mkdir","path":"tmp","mode":"01755"}]}`
	_, _, err := parse(t, body)
	if err != nil {
		t.Errorf("sticky-on-mkdir should be allowed, got %v", err)
	}
}

func TestParseRejectsAbsolutePath(t *testing.T) {
	body := `{"schemaVersion": 1, "sources":[{"type":"inlineFile","path":"/etc/passwd","content":""}]}`
	_, _, err := parse(t, body)
	expectErr(t, err, workspaceplan.ReasonAbsolutePath)
}

func TestParseRejectsPathTraversal(t *testing.T) {
	body := `{"schemaVersion": 1, "sources":[{"type":"inlineFile","path":"a/../b","content":""}]}`
	_, _, err := parse(t, body)
	expectErr(t, err, workspaceplan.ReasonPathTraversal)
}

func TestParseRejectsExcessivePathDepth(t *testing.T) {
	deep := strings.Repeat("a/", workspaceplan.MaxPathDepth+1) + "x"
	body := `{"schemaVersion": 1, "sources":[{"type":"inlineFile","path":"` + deep + `","content":""}]}`
	_, _, err := parse(t, body)
	expectErr(t, err, workspaceplan.ReasonPathTooDeep)
}

func TestParseRejectsExcessivePathLength(t *testing.T) {
	long := strings.Repeat("a", workspaceplan.MaxPathLength+1)
	body := `{"schemaVersion": 1, "sources":[{"type":"inlineFile","path":"` + long + `","content":""}]}`
	_, _, err := parse(t, body)
	expectErr(t, err, workspaceplan.ReasonPathTooLong)
}

func TestParseRejectsNonHTTPSGitURL(t *testing.T) {
	for _, u := range []string{
		"git://github.com/example/repo.git",
		"ssh://git@github.com/example/repo.git",
		"http://github.com/example/repo.git",
	} {
		body := `{"schemaVersion": 1, "sources":[{"type":"gitClone","url":"` + u + `","ref":"main"}]}`
		_, _, err := parse(t, body)
		expectErr(t, err, workspaceplan.ReasonGitNonHTTPS)
	}
}

func TestParseRejectsSCPStyleGitURL(t *testing.T) {
	// "git@github.com:owner/repo.git" doesn't parse as a URL — falls to
	// the invalid URL path. Either error is acceptable; we assert
	// rejection.
	body := `{"schemaVersion": 1, "sources":[{"type":"gitClone","url":"git@github.com:owner/repo.git","ref":"main"}]}`
	_, _, err := parse(t, body)
	if err == nil {
		t.Fatal("SCP-style URL should be rejected")
	}
}

func TestParseRejectsGatewayWrittenResolvedCommitSha(t *testing.T) {
	body := `{"schemaVersion": 1, "sources":[{"type":"gitClone","url":"https://x.example/repo.git","ref":"main","resolvedCommitSha":"0123456789abcdef0123456789abcdef01234567"}]}`
	_, _, err := parse(t, body)
	expectErr(t, err, workspaceplan.ReasonGatewayWrittenField)
}

func TestParseRejectsInvalidAuthMode(t *testing.T) {
	body := `{"schemaVersion": 1, "sources":[{"type":"gitClone","url":"https://x.example/r.git","ref":"main","auth":{"mode":"oauth","leaseScope":"vcs.github.read"}}]}`
	_, _, err := parse(t, body)
	expectErr(t, err, workspaceplan.ReasonInvalidAuthMode)
}

func TestParseRejectsInvalidLeaseScope(t *testing.T) {
	for _, scope := range []string{
		"vcs.github.full",
		"vcs..read",
		"bad.github.read",
		"vcs.github",
	} {
		body := `{"schemaVersion": 1, "sources":[{"type":"gitClone","url":"https://x.example/r.git","ref":"main","auth":{"mode":"credential-lease","leaseScope":"` + scope + `"}}]}`
		_, _, err := parse(t, body)
		expectErr(t, err, workspaceplan.ReasonInvalidLeaseScope)
	}
}

func TestParseRejectsArchiveBadFormat(t *testing.T) {
	body := `{"schemaVersion": 1, "sources":[{"type":"uploadArchive","pathPrefix":"v","uploadRef":"r","format":"rar"}]}`
	_, _, err := parse(t, body)
	expectErr(t, err, workspaceplan.ReasonInvalidEnumValue)
}

func TestParseRejectsArchiveNegativeStripComponents(t *testing.T) {
	body := `{"schemaVersion": 1, "sources":[{"type":"uploadArchive","pathPrefix":"v","uploadRef":"r","format":"tar","stripComponents":-1}]}`
	_, _, err := parse(t, body)
	expectErr(t, err, workspaceplan.ReasonNegativeDepth)
}

func TestParseAggregatesMultipleSourceErrors(t *testing.T) {
	// Two bad sources — parser should report both in SubErrs.
	body := `{"schemaVersion": 1, "sources":[
		{"type":"inlineFile","path":"/abs","content":""},
		{"type":"gitClone","url":"ftp://x","ref":"main"}
	]}`
	_, _, err := parse(t, body)
	var ve *workspaceplan.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %T", err)
	}
	if len(ve.SubErrs) != 2 {
		t.Fatalf("subErrs: got %d, want 2 (%+v)", len(ve.SubErrs), ve.SubErrs)
	}
}

func TestParseEmitsPathCollisionWarning(t *testing.T) {
	body := `{"schemaVersion": 1, "sources":[
		{"type":"inlineFile","path":"a","content":"x"},
		{"type":"inlineFile","path":"a","content":"y"}
	]}`
	_, warns, err := parse(t, body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(warns) != 1 || warns[0].Code != workspaceplan.WarnPathCollision || warns[0].SourceIndex != 1 {
		t.Errorf("expected collision warning at sources[1]; got %+v", warns)
	}
}

func TestSourcePathReturnsPlanRelativePath(t *testing.T) {
	plan, _, err := parse(t, `{"schemaVersion":1,"sources":[{"type":"inlineFile","path":"a/b","content":"x"}]}`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := plan.Sources[0].Variant.Path(); got != "a/b" {
		t.Errorf("Source.Path: got %q, want a/b", got)
	}
}

func TestParseMissingRequiredFieldsForEachVariant(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"inlineFile no path", `{"schemaVersion":1,"sources":[{"type":"inlineFile","content":"x"}]}`},
		{"inlineFile no content not required to be non-empty", `{"schemaVersion":1,"sources":[{"type":"inlineFile","path":"x","content":""}]}`}, // accepted
		{"uploadFile no uploadRef", `{"schemaVersion":1,"sources":[{"type":"uploadFile","path":"x"}]}`},
		{"uploadArchive no format", `{"schemaVersion":1,"sources":[{"type":"uploadArchive","pathPrefix":"x","uploadRef":"r"}]}`},
		{"mkdir no path", `{"schemaVersion":1,"sources":[{"type":"mkdir"}]}`},
		{"gitClone no url", `{"schemaVersion":1,"sources":[{"type":"gitClone","ref":"main"}]}`},
		{"gitClone no ref", `{"schemaVersion":1,"sources":[{"type":"gitClone","url":"https://x.example/r.git"}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := parse(t, c.body)
			if c.name == "inlineFile no content not required to be non-empty" {
				if err != nil {
					t.Errorf("expected accept, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

// TestParseRejectsNegativeSetupCommandTimeout covers F-7.5.14 / §14 line 99:
// per-command timeoutSeconds is an unsigned duration. A negative value
// must be rejected at the §14 ingress rather than silently degrading to
// the "no per-command bound" path (the downstream `> 0` gate after the
// F-7.5.6 fix).
func TestParseRejectsNegativeSetupCommandTimeout(t *testing.T) {
	body := `{
		"schemaVersion": 1,
		"sources": [],
		"setupCommands": [
			{"cmd": "echo hi", "timeoutSeconds": -1}
		]
	}`
	_, _, err := parse(t, body)
	expectErr(t, err, workspaceplan.ReasonNegativeDepth)
}

// TestParseAcceptsZeroSetupCommandTimeout: a zero or omitted timeoutSeconds
// is the §14 line 99 "no per-command bound" form and must continue to
// parse cleanly. Regression guard around the F-7.5.14 negative-check.
func TestParseAcceptsZeroSetupCommandTimeout(t *testing.T) {
	body := `{
		"schemaVersion": 1,
		"sources": [],
		"setupCommands": [
			{"cmd": "echo hi"},
			{"cmd": "true", "timeoutSeconds": 0}
		]
	}`
	plan, _, err := parse(t, body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(plan.SetupCommands) != 2 {
		t.Fatalf("plan.SetupCommands = %d, want 2", len(plan.SetupCommands))
	}
}

// spec: §15.5 lines 2471-2474 — durable consumers MUST forward-read
// records with unrecognized `schemaVersion`. A stored plan whose
// schemaVersion exceeds this gateway's known revision is parsed (not
// rejected) and surfaces a `workspace_plan_durable_schema_version_ahead`
// warning carrying knownVersion + encounteredVersion so durable-
// consumer alerts route on the §15.5 line 2466 catalog body shape.
// F-15.5.8.
func TestParseStoredForwardReadsNewerSchemaVersion_spec_15_5_2474(t *testing.T) {
	body := `{"schemaVersion": 99, "sources": [
		{"type": "inlineFile", "path": "hello.txt", "content": "hi"}
	]}`
	plan, warns, err := workspaceplan.ParseStored([]byte(body))
	if err != nil {
		t.Fatalf("ParseStored: %v", err)
	}
	if plan.SchemaVersion != 99 {
		t.Errorf("plan.SchemaVersion = %d, want 99 (forward-read preserves stamped version)", plan.SchemaVersion)
	}
	if len(plan.Sources) != 1 {
		t.Fatalf("plan.Sources len = %d, want 1", len(plan.Sources))
	}
	var got *workspaceplan.Warning
	for i := range warns {
		if warns[i].Code == workspaceplan.WarnDurableSchemaVersionAhead {
			got = &warns[i]
		}
	}
	if got == nil {
		t.Fatalf("expected durable_schema_version_ahead warning, got %+v", warns)
	}
	if got.KnownVersion == nil || *got.KnownVersion != workspaceplan.SchemaVersion {
		t.Errorf("warning.KnownVersion = %v, want %d", got.KnownVersion, workspaceplan.SchemaVersion)
	}
	if got.EncounteredVersion == nil || *got.EncounteredVersion != 99 {
		t.Errorf("warning.EncounteredVersion = %v, want 99", got.EncounteredVersion)
	}
}

// spec: §15.5 lines 2471-2473 — the durable forward-read MUST preserve
// unknown fields verbatim so a downstream pass-through consumer (e.g.
// a controller that re-reads a stored plan) sees the future-version
// payload intact. F-15.5.8.
func TestParseStoredForwardReadsPreservesUnknownFields_spec_15_5_2473(t *testing.T) {
	body := `{"schemaVersion": 99, "sources": [
		{"type": "inlineFile", "path": "hello.txt", "content": "hi", "futureField": {"x": 1}}
	]}`
	plan, _, err := workspaceplan.ParseStored([]byte(body))
	if err != nil {
		t.Fatalf("ParseStored: %v", err)
	}
	if plan.Sources[0].Raw == nil {
		t.Fatal("Source.Raw is nil; unknown fields lost")
	}
	if _, ok := plan.Sources[0].Raw["futureField"]; !ok {
		t.Errorf("Source.Raw missing futureField; got keys %v", keys(plan.Sources[0].Raw))
	}
}

// spec: §15.5 line 2474 — durable consumers MUST NOT silently discard
// records based solely on an unrecognized schemaVersion. The fresh
// ingress path (Parse) must still hard-reject so a client uploading a
// future-version plan gets a 400, not silent acceptance. F-15.5.8.
func TestParseFreshIngressStillRejectsNewerSchemaVersion_spec_14(t *testing.T) {
	body := `{"schemaVersion": 99, "sources": []}`
	_, _, err := workspaceplan.Parse([]byte(body))
	if err == nil {
		t.Fatal("Parse must reject a fresh client request at schemaVersion > known")
	}
	verr, _ := err.(*workspaceplan.ValidationError)
	if verr == nil || verr.Reason != workspaceplan.ReasonUnsupportedSchemaVersion {
		t.Errorf("got reason %v, want unsupported_schema_version", err)
	}
}

// spec: §15.5 — stored input whose schemaVersion is lower than known
// (e.g. negative due to a bug or older write that bypassed validation)
// must still hard-reject; forward-read covers only the new-writer →
// old-reader direction. F-15.5.8.
func TestParseStoredStillRejectsLowerSchemaVersion(t *testing.T) {
	body := `{"schemaVersion": -1, "sources": []}`
	_, _, err := workspaceplan.ParseStored([]byte(body))
	if err == nil {
		t.Fatal("ParseStored must reject a negative schemaVersion")
	}
	verr, _ := err.(*workspaceplan.ValidationError)
	if verr == nil || verr.Reason != workspaceplan.ReasonInvalidSchemaVersion {
		t.Errorf("got reason %v, want invalid_schema_version", err)
	}
}

// spec: §15.5 line 2473 — when forward-reading, unknown fields inside
// a known variant MUST NOT trigger a hard reject. F-15.5.8.
func TestParseStoredForwardReadsTolerantToUnknownVariantFields(t *testing.T) {
	body := `{"schemaVersion": 99, "sources": [
		{"type": "uploadFile", "path": "a", "uploadRef": "ref1", "newOption": true}
	]}`
	plan, _, err := workspaceplan.ParseStored([]byte(body))
	if err != nil {
		t.Fatalf("ParseStored unexpectedly rejected forward-read variant: %v", err)
	}
	if len(plan.Sources) != 1 {
		t.Fatalf("plan.Sources len = %d, want 1", len(plan.Sources))
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
