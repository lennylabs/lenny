// SPDX-License-Identifier: MIT

package workspaceplan_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// Two valid 40-character lowercase-hex commit SHAs for the tests.
const (
	shaA = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
	shaB = "00112233445566778899aabbccddeeff00112233"
)

// fakeResolver resolves refs from a fixed map. When err is set every
// Resolve fails; calls counts invocations.
type fakeResolver struct {
	shas  map[string]string
	err   error
	calls int
}

func (f *fakeResolver) Resolve(_ context.Context, src workspaceplan.GitClone) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.shas[src.Ref], nil
}

func gitClonePlan(clones ...workspaceplan.GitClone) *workspaceplan.Plan {
	p := &workspaceplan.Plan{SchemaVersion: 1}
	for _, gc := range clones {
		p.Sources = append(p.Sources, workspaceplan.Source{
			Type: workspaceplan.TypeGitClone, Variant: gc,
		})
	}
	return p
}

func TestIsCommitSHA(t *testing.T) {
	cases := []struct {
		ref  string
		want bool
	}{
		{shaA, true},
		{shaB, true},
		{"A1B2C3D4E5F60718293A4B5C6D7E8F9012345678", false},  // uppercase
		{"a1b2c3d4e5f60718293a4b5c6d7e8f901234567", false},   // 39 chars
		{"a1b2c3d4e5f60718293a4b5c6d7e8f90123456789", false}, // 41 chars
		{"a1b2c3d4e5f60718293a4b5c6d7e8f901234567g", false},  // non-hex
		{"main", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := workspaceplan.IsCommitSHA(tc.ref); got != tc.want {
			t.Errorf("IsCommitSHA(%q) = %v, want %v", tc.ref, got, tc.want)
		}
	}
}

func TestResolveErrorReasonTransient(t *testing.T) {
	if !workspaceplan.ResolveNetworkError.Transient() {
		t.Error("network_error must be transient (retryable)")
	}
	for _, r := range []workspaceplan.ResolveErrorReason{
		workspaceplan.ResolveAuthFailed, workspaceplan.ResolveRefNotFound,
	} {
		if r.Transient() {
			t.Errorf("%s must not be transient", r)
		}
	}
}

func TestPinCommitSHAsFastPathSkipsResolver(t *testing.T) {
	// A ref already in commit-SHA form is pinned without a resolver call.
	plan := gitClonePlan(workspaceplan.GitClone{URL: "https://example.com/r.git", Ref: shaA})
	fake := &fakeResolver{}
	if err := workspaceplan.PinCommitSHAs(context.Background(), plan, fake); err != nil {
		t.Fatalf("PinCommitSHAs: %v", err)
	}
	if fake.calls != 0 {
		t.Errorf("resolver called %d times for a SHA ref, want 0", fake.calls)
	}
	gc := plan.Sources[0].Variant.(workspaceplan.GitClone)
	if gc.ResolvedCommitSha != shaA {
		t.Errorf("ResolvedCommitSha = %q, want %q", gc.ResolvedCommitSha, shaA)
	}
}

func TestPinCommitSHAsFastPathNilResolver(t *testing.T) {
	// The SHA fast-path works even with no resolver wired.
	plan := gitClonePlan(workspaceplan.GitClone{URL: "https://example.com/r.git", Ref: shaA})
	if err := workspaceplan.PinCommitSHAs(context.Background(), plan, nil); err != nil {
		t.Fatalf("PinCommitSHAs with a nil resolver and a SHA ref: %v", err)
	}
	gc := plan.Sources[0].Variant.(workspaceplan.GitClone)
	if gc.ResolvedCommitSha != shaA {
		t.Errorf("ResolvedCommitSha = %q, want %q", gc.ResolvedCommitSha, shaA)
	}
}

func TestPinCommitSHAsResolvesBranchRef(t *testing.T) {
	plan := gitClonePlan(workspaceplan.GitClone{URL: "https://example.com/r.git", Ref: "main"})
	fake := &fakeResolver{shas: map[string]string{"main": shaB}}
	if err := workspaceplan.PinCommitSHAs(context.Background(), plan, fake); err != nil {
		t.Fatalf("PinCommitSHAs: %v", err)
	}
	if fake.calls != 1 {
		t.Errorf("resolver called %d times, want 1", fake.calls)
	}
	gc := plan.Sources[0].Variant.(workspaceplan.GitClone)
	if gc.ResolvedCommitSha != shaB {
		t.Errorf("ResolvedCommitSha = %q, want %q", gc.ResolvedCommitSha, shaB)
	}
}

func TestPinCommitSHAsAlreadyPinnedIsIdempotent(t *testing.T) {
	// A source whose ResolvedCommitSha is already set is left untouched.
	plan := gitClonePlan(workspaceplan.GitClone{
		URL: "https://example.com/r.git", Ref: "main", ResolvedCommitSha: shaA,
	})
	fake := &fakeResolver{shas: map[string]string{"main": shaB}}
	if err := workspaceplan.PinCommitSHAs(context.Background(), plan, fake); err != nil {
		t.Fatalf("PinCommitSHAs: %v", err)
	}
	if fake.calls != 0 {
		t.Errorf("resolver called %d times for an already-pinned source, want 0", fake.calls)
	}
	gc := plan.Sources[0].Variant.(workspaceplan.GitClone)
	if gc.ResolvedCommitSha != shaA {
		t.Errorf("ResolvedCommitSha = %q, want the original %q", gc.ResolvedCommitSha, shaA)
	}
}

func TestPinCommitSHAsIgnoresNonGitCloneSources(t *testing.T) {
	plan := &workspaceplan.Plan{
		SchemaVersion: 1,
		Sources: []workspaceplan.Source{
			{Type: workspaceplan.TypeInlineFile, Variant: workspaceplan.InlineFile{PathField: "f", Content: "x"}},
			{Type: workspaceplan.TypeGitClone, Variant: workspaceplan.GitClone{URL: "https://example.com/r.git", Ref: "main"}},
		},
	}
	fake := &fakeResolver{shas: map[string]string{"main": shaA}}
	if err := workspaceplan.PinCommitSHAs(context.Background(), plan, fake); err != nil {
		t.Fatalf("PinCommitSHAs: %v", err)
	}
	if fake.calls != 1 {
		t.Errorf("resolver called %d times, want 1 (only the gitClone source)", fake.calls)
	}
}

func TestPinCommitSHAsPropagatesClassifiedError(t *testing.T) {
	// A resolver-returned *ResolveError propagates with SourceIndex set.
	plan := gitClonePlan(
		workspaceplan.GitClone{URL: "https://example.com/a.git", Ref: shaA},
		workspaceplan.GitClone{URL: "https://example.com/b.git", Ref: "missing"},
	)
	fake := &fakeResolver{err: &workspaceplan.ResolveError{
		Reason: workspaceplan.ResolveRefNotFound,
		Err:    errors.New("ref missing"),
	}}
	err := workspaceplan.PinCommitSHAs(context.Background(), plan, fake)
	var re *workspaceplan.ResolveError
	if !errors.As(err, &re) {
		t.Fatalf("error = %v, want *ResolveError", err)
	}
	if re.SourceIndex != 1 {
		t.Errorf("SourceIndex = %d, want 1 (the second source)", re.SourceIndex)
	}
	if re.Reason != workspaceplan.ResolveRefNotFound {
		t.Errorf("Reason = %q, want ref_not_found", re.Reason)
	}
	// The resolver did not classify URL/Ref; PinCommitSHAs backfills them.
	if re.URL != "https://example.com/b.git" || re.Ref != "missing" {
		t.Errorf("URL/Ref = %q/%q, want the failing source's", re.URL, re.Ref)
	}
}

func TestPinCommitSHAsWrapsUnclassifiedError(t *testing.T) {
	// A plain (unclassified) resolver error is treated as transient.
	plan := gitClonePlan(workspaceplan.GitClone{URL: "https://example.com/r.git", Ref: "main"})
	fake := &fakeResolver{err: errors.New("connection reset")}
	err := workspaceplan.PinCommitSHAs(context.Background(), plan, fake)
	var re *workspaceplan.ResolveError
	if !errors.As(err, &re) {
		t.Fatalf("error = %v, want *ResolveError", err)
	}
	if !re.Reason.Transient() {
		t.Errorf("an unclassified resolver error must be transient, got reason %q", re.Reason)
	}
}

func TestPinCommitSHAsRejectsNonSHAResolverResult(t *testing.T) {
	// A resolver that returns a non-SHA string is a failure.
	plan := gitClonePlan(workspaceplan.GitClone{URL: "https://example.com/r.git", Ref: "main"})
	fake := &fakeResolver{shas: map[string]string{"main": "refs/heads/main"}}
	err := workspaceplan.PinCommitSHAs(context.Background(), plan, fake)
	var re *workspaceplan.ResolveError
	if !errors.As(err, &re) {
		t.Fatalf("error = %v, want *ResolveError", err)
	}
	if re.Reason != workspaceplan.ResolveRefNotFound {
		t.Errorf("Reason = %q, want ref_not_found", re.Reason)
	}
}

func TestPinCommitSHAsNilResolverNonSHARefErrors(t *testing.T) {
	plan := gitClonePlan(workspaceplan.GitClone{URL: "https://example.com/r.git", Ref: "main"})
	err := workspaceplan.PinCommitSHAs(context.Background(), plan, nil)
	var re *workspaceplan.ResolveError
	if !errors.As(err, &re) {
		t.Fatalf("error = %v, want *ResolveError for a non-SHA ref with no resolver", err)
	}
}

func TestPinCommitSHAsNilPlan(t *testing.T) {
	if err := workspaceplan.PinCommitSHAs(context.Background(), nil, nil); err != nil {
		t.Errorf("PinCommitSHAs(nil) = %v, want nil", err)
	}
}

func TestMarshalRoundTrips(t *testing.T) {
	raw := []byte(`{
		"schemaVersion": 1,
		"sources": [
			{"type": "inlineFile", "path": "a.txt", "content": "hello"},
			{"type": "gitClone", "url": "https://example.com/r.git", "ref": "main"}
		],
		"setupCommands": [{"cmd": "echo hi", "timeoutSeconds": 30}]
	}`)
	plan, _, err := workspaceplan.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := workspaceplan.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// An unpinned plan round-trips back through Parse unchanged.
	plan2, _, err := workspaceplan.Parse(out)
	if err != nil {
		t.Fatalf("re-Parse: %v\n%s", err, out)
	}
	if plan2.SchemaVersion != 1 || len(plan2.Sources) != 2 {
		t.Fatalf("re-parsed plan = %d sources, schemaVersion %d; want 2 / 1",
			len(plan2.Sources), plan2.SchemaVersion)
	}
	if len(plan2.SetupCommands) != 1 || plan2.SetupCommands[0].Cmd != "echo hi" {
		t.Errorf("setupCommands = %+v, want one echo command", plan2.SetupCommands)
	}
	gc := plan2.Sources[1].Variant.(workspaceplan.GitClone)
	if gc.URL != "https://example.com/r.git" || gc.Ref != "main" {
		t.Errorf("gitClone source not preserved: %+v", gc)
	}
}

func TestMarshalEmitsPinnedCommitSHA(t *testing.T) {
	raw := []byte(`{"schemaVersion":1,"sources":[` +
		`{"type":"gitClone","url":"https://example.com/r.git","ref":"main"}]}`)
	plan, _, err := workspaceplan.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fake := &fakeResolver{shas: map[string]string{"main": shaA}}
	if err := workspaceplan.PinCommitSHAs(context.Background(), &plan, fake); err != nil {
		t.Fatalf("PinCommitSHAs: %v", err)
	}
	out, err := workspaceplan.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Parse rejects a client-supplied resolvedCommitSha, so the pinned
	// stored form is verified by inspecting the marshalled JSON directly.
	var doc struct {
		Sources []map[string]any `json:"sources"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal marshalled plan: %v", err)
	}
	if got := doc.Sources[0]["resolvedCommitSha"]; got != shaA {
		t.Errorf("resolvedCommitSha = %v, want %q", got, shaA)
	}
}

func TestParseStoredAcceptsResolvedCommitSha(t *testing.T) {
	raw := []byte(`{"schemaVersion":1,"sources":[` +
		`{"type":"gitClone","url":"https://example.com/r.git","ref":"main",` +
		`"resolvedCommitSha":"` + shaA + `"}]}`)
	// Parse rejects the gateway-written field on a client request body.
	if _, _, err := workspaceplan.Parse(raw); err == nil {
		t.Error("Parse must reject a client-supplied resolvedCommitSha")
	}
	// ParseStored accepts it — the pinned form read back from storage.
	plan, _, err := workspaceplan.ParseStored(raw)
	if err != nil {
		t.Fatalf("ParseStored: %v", err)
	}
	gc := plan.Sources[0].Variant.(workspaceplan.GitClone)
	if gc.ResolvedCommitSha != shaA {
		t.Errorf("ResolvedCommitSha = %q, want %q", gc.ResolvedCommitSha, shaA)
	}
}

func TestMarshalPinnedRoundTripsThroughParseStored(t *testing.T) {
	// The full creation-time path: parse a client plan, pin its refs,
	// Marshal to the stored form, and read it back with ParseStored.
	raw := []byte(`{"schemaVersion":1,"sources":[` +
		`{"type":"gitClone","url":"https://example.com/r.git","ref":"main"}]}`)
	plan, _, err := workspaceplan.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := workspaceplan.PinCommitSHAs(context.Background(), &plan,
		&fakeResolver{shas: map[string]string{"main": shaB}}); err != nil {
		t.Fatalf("PinCommitSHAs: %v", err)
	}
	out, err := workspaceplan.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	stored, _, err := workspaceplan.ParseStored(out)
	if err != nil {
		t.Fatalf("ParseStored: %v", err)
	}
	gc := stored.Sources[0].Variant.(workspaceplan.GitClone)
	if gc.ResolvedCommitSha != shaB {
		t.Errorf("round-tripped ResolvedCommitSha = %q, want %q", gc.ResolvedCommitSha, shaB)
	}
}

func TestMarshalPreservesUnknownSourceType(t *testing.T) {
	raw := []byte(`{"schemaVersion":1,"sources":[{"type":"vendorThing","custom":"value"}]}`)
	plan, warnings, err := workspaceplan.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("an unknown source type should produce a warning")
	}
	out, err := workspaceplan.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var doc struct {
		Sources []map[string]any `json:"sources"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Sources[0]["type"] != "vendorThing" || doc.Sources[0]["custom"] != "value" {
		t.Errorf("unknown source type not preserved: %v", doc.Sources[0])
	}
}
