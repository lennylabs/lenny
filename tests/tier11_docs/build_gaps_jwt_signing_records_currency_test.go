// SPDX-License-Identifier: MIT

// Tier-11 consistency checks between the records of the shared JWT signing
// key work (proposal 0097) and the tree they describe.
//
// The build loops read BUILD-GAPS.md and TEST-GAPS.md as statements about the
// current tree. These checks hold the shared-signing-key records to it:
//
//   - The per-process signing-key finding stays closed only while the
//     per-process HMAC signer is absent and the shared key set is present, and
//     every test its resolution names exists.
//   - The closed findings the work superseded carry the note that says so,
//     and none describes the work as closing a "signing half".
//   - The test-gap records the work moved are resolved or annotated as it left
//     them, and every test they name exists.
//   - The tier-6 record states what the tier-6 test file does: the
//     verification path it calls, the skip reason it uses, and whether the
//     skip register carries it.
//   - Every tier-8 chaos test of the shared signing key is recorded against a
//     finding it closes or advances.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// jwtSigningProposal is the directory of the proposal whose records these
// checks hold, as the records name it.
const jwtSigningProposal = "proposals/0097_fix_a-bearer-token-that-lenny-issues-verifies-only-on/"

// Tree paths the records describe.
const (
	jwtSigningTier6File = "tests/tier6_e2e_cloud/jwt_signing_kms_test.go"
	jwtSigningChaosFile = "tests/tier8_chaos/jwt_keyset_chaos_test.go"
	skipReasonsRegister = "tests/registers/skip-reasons.yaml"
)

// recordTestRef matches a test file a record names, optionally followed by
// "::" and a test function, inside or outside backticks.
var recordTestRef = regexp.MustCompile(`((?:pkg|tests|cmd)/[\w./-]+_test\.go)(?:::(Test\w+))?`)

// recordTestRefs returns the test files and file::function pairs a record
// names. A bare file reference has an empty function.
func recordTestRefs(text string) [][2]string {
	var out [][2]string
	for _, m := range recordTestRef.FindAllStringSubmatch(text, -1) {
		out = append(out, [2]string{m[1], m[2]})
	}
	return out
}

// assertRecordTestRefsExist fails the test for each test file a record names
// that is absent, and for each named test function its file does not
// declare. A record may name a deleted file only in the sentence that says it
// was deleted, so references in such a sentence are skipped.
func assertRecordTestRefsExist(t *testing.T, root, where, text string) {
	t.Helper()
	for _, sentence := range strings.SplitAfter(text, ". ") {
		if strings.Contains(sentence, "deleted") || strings.Contains(sentence, "deletes") {
			continue
		}
		for _, ref := range recordTestRefs(sentence) {
			src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ref[0])))
			if err != nil {
				t.Errorf("%s names %s, which does not exist", where, ref[0])
				continue
			}
			if ref[1] != "" && !strings.Contains(string(src), "func "+ref[1]+"(") {
				t.Errorf("%s names %s::%s, which that file does not declare", where, ref[0], ref[1])
			}
		}
	}
}

// fileText returns the content of the repository file rel.
func fileText(t *testing.T, root, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}

// diagnosis: F-10.2.22 was reopened, lost its proposal 0097 resolution, or
// names a test that no longer exists; or the tree regained the per-process
// HMAC signer or lost the shared key set that closed it. Restore the
// resolution, or reopen the finding when the tree no longer holds the fix.
//
// spec: 10.2 (Authentication)
func TestJWTSigningRecordsCloseThePerProcessKeyFinding_spec_10_2(t *testing.T) {
	root := repoRoot(t)
	f := requireFinding(t, buildGapsFindingsByID(t, root), "F-10.2.22")
	if f.open || !strings.HasSuffix(strings.TrimSpace(f.heading), "— CLOSED") {
		t.Fatalf("BUILD-GAPS.md:%d %s\n  is not closed", f.line, f.heading)
	}
	if !strings.Contains(f.body, "**Resolution (proposal 0097") || !strings.Contains(f.body, jwtSigningProposal) {
		t.Errorf("BUILD-GAPS.md:%d %s\n  carries no proposal 0097 resolution naming %s", f.line, f.heading, jwtSigningProposal)
	}
	assertRecordTestRefsExist(t, root, "BUILD-GAPS.md F-10.2.22", f.body)

	jwtPkg := ""
	for _, name := range []string{"keyset.go", "kmssigner.go", "loadsigning.go"} {
		jwtPkg += fileText(t, root, "pkg/auth/jwt/"+name)
	}
	for _, gone := range []string{"func NewKMSSigner(", "func KMSSignerFromSealed(", "type RotatingVerifier "} {
		if strings.Contains(jwtPkg, gone) {
			t.Errorf("pkg/auth/jwt declares %q again, so the per-process key F-10.2.22 records as fixed is back", gone)
		}
	}
	for _, kept := range []string{"func LoadKeySet(", "func NewRemoteSigner(", "func LoadSigning("} {
		if !strings.Contains(jwtPkg, kept) {
			t.Errorf("pkg/auth/jwt no longer declares %q, which the F-10.2.22 resolution names", kept)
		}
	}
}

// supersededFinding is a closed finding the shared-signing-key work
// annotated, and the label its note opens with.
type supersededFinding struct {
	id    string
	label string
	want  []string
}

// supersededFindings are the closed findings proposal 0097 annotated.
var supersededFindings = []supersededFinding{
	{"F-10.2.13", "**Resolution note (proposal 0097", []string{"remote ES256 signing", "F-10.2.11"}},
	{"F-10.2.11", "**Supersession note (proposal 0097", []string{"supersedes", "`--jwks-publish`"}},
	{"F-10.2.14", "**Supersession note (proposal 0097", []string{"supersedes", "`/.well-known/jwks.json`", "`--jwks-publish`"}},
	{"F-4.3.1", "**Note (proposal 0097", []string{"holds no private signing key on a cloud provider"}},
}

// diagnosis: a closed finding that proposal 0097 superseded or narrowed lost
// its note, was reopened, names a test that no longer exists, or describes the
// work as closing a "signing half". Restore the note as the work left it.
//
// spec: 10.2 (Authentication)
func TestJWTSigningRecordsNoteSupersededFindings_spec_10_2(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, c := range supersededFindings {
		t.Run(c.id, func(t *testing.T) {
			f := requireFinding(t, findings, c.id)
			if f.open {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  is open; proposal 0097 annotated it as closed", f.line, f.heading)
			}
			_, note, ok := strings.Cut(f.body, c.label)
			if !ok {
				t.Fatalf("BUILD-GAPS.md:%d %s\n  carries no note opening %q", f.line, f.heading, c.label)
			}
			note, _, _ = strings.Cut(note, "\n")
			for _, w := range append([]string{jwtSigningProposal}, c.want...) {
				if !strings.Contains(note, w) {
					t.Errorf("BUILD-GAPS.md:%d %s\n  note does not mention %q", f.line, f.heading, w)
				}
			}
			if strings.Contains(strings.ToLower(note), "signing half") {
				t.Errorf("BUILD-GAPS.md:%d %s\n  note describes the work as closing a \"signing half\"", f.line, f.heading)
			}
			assertRecordTestRefsExist(t, root, "BUILD-GAPS.md "+c.id, note)
		})
	}
}

// diagnosis: a test-gap record proposal 0097 moved was reopened, closed, or
// lost its note, or names a test that no longer exists. T-10.2.9 is closed by
// replacement, T-10.2.3 is resolved as a covered claim half and a retired
// JWKS half, T-10.2.2 is resolved once proposal 0104 removed the
// session-creation mint its open half waited on, and T-10.2.8 stays open
// until tier 6 runs. Restore the record, or record the later change that
// moved it.
//
// spec: 10.2 (Authentication)
func TestJWTSigningTestGapRecords_spec_10_2(t *testing.T) {
	root := repoRoot(t)
	for _, c := range []struct {
		id       string
		resolved bool
		want     []string
	}{
		{"T-10.2.9", true, []string{"Closed by replacement", "TestSigningKeyRotationUnderLocal_spec_10_2"}},
		{"T-10.2.3", true, []string{"claim half is covered", "JWKS half is retired"}},
		{"T-10.2.2", true, []string{"**Annotation (proposal 0097", "throttle and breaker half is covered", "proposals/0104_fix_two-gaps-leave-lenny-s-session-scoped-and-delegation/"}},
		{"T-10.2.8", false, []string{"**Annotation (proposal 0097", "stays OPEN until tier 6 runs"}},
	} {
		t.Run(c.id, func(t *testing.T) {
			resolved, body := testGapsEntry(t, root, c.id)
			if resolved != c.resolved {
				t.Fatalf("TEST-GAPS.md %s resolved = %v, want %v", c.id, resolved, c.resolved)
			}
			for _, w := range append([]string{jwtSigningProposal}, c.want...) {
				if !strings.Contains(body, w) {
					t.Errorf("TEST-GAPS.md %s does not mention %q", c.id, w)
				}
			}
			_, note, _ := strings.Cut(body, "Proposal 0097 (`"+jwtSigningProposal)
			assertRecordTestRefsExist(t, root, "TEST-GAPS.md "+c.id, note)
		})
	}
}

// tier6SkipReason captures the categorized skip-reason format string the
// tier-6 signing test passes to t.Skipf.
var tier6SkipReason = regexp.MustCompile(`t\.Skipf\("(blocked: [^"]+)"`)

// skipReasonFragments returns the literal fragments of a skip-reason format
// string between its %s verbs, the parts a record quoting the reason with a
// placeholder for each verb must contain.
func skipReasonFragments(format string) []string {
	var out []string
	for _, frag := range strings.Split(format, "%s") {
		if frag != "" {
			out = append(out, frag)
		}
	}
	return out
}

// diagnosis: the T-10.2.8 record no longer states what the tier-6 signing
// test does. The record names the verification path the file calls, the
// categorized skip reason it uses, and whether the skip register carries it,
// so a change to the file or the register needs a matching edit to the
// record, written from the file rather than from an earlier plan.
//
// spec: 10.2 (Authentication)
func TestJWTSigningTier6RecordMatchesTheTree_spec_10_2(t *testing.T) {
	root := repoRoot(t)
	_, body := testGapsEntry(t, root, "T-10.2.8")
	_, note, ok := strings.Cut(body, "**Annotation (proposal 0097")
	if !ok {
		t.Fatal("TEST-GAPS.md T-10.2.8 carries no proposal 0097 annotation")
	}
	src := fileText(t, root, jwtSigningTier6File)

	for _, call := range []string{"jwt.LoadKeySet", "jwt.NewRemoteSigner", "providerflags.Resolve"} {
		if strings.Contains(src, call+"(") != strings.Contains(note, "`"+call+"`") {
			t.Errorf("%s calls %s = %v, but the T-10.2.8 record names it = %v",
				jwtSigningTier6File, call, strings.Contains(src, call+"("), strings.Contains(note, "`"+call+"`"))
		}
	}
	m := tier6SkipReason.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%s no longer skips with a categorized blocked: reason; rewrite the T-10.2.8 record from the file", jwtSigningTier6File)
	}
	for _, frag := range skipReasonFragments(m[1]) {
		if !strings.Contains(note, frag) {
			t.Errorf("the T-10.2.8 record does not quote %q from the skip reason %q that %s uses", frag, m[1], jwtSigningTier6File)
		}
	}
	registered := strings.Contains(fileText(t, root, skipReasonsRegister), filepath.Base(jwtSigningTier6File))
	saysUnregistered := strings.Contains(note, "`"+skipReasonsRegister+"` carries no entry")
	if registered == saysUnregistered {
		t.Errorf("%s registers the tier-6 signing skips = %v, but the T-10.2.8 record says it carries no entry = %v",
			skipReasonsRegister, registered, saysUnregistered)
	}
}

// chaosTestFunc matches a top-level test function declaration.
var chaosTestFunc = regexp.MustCompile(`(?m)^func (Test\w+)\(`)

// diagnosis: a tier-8 chaos test of the shared signing key is named in no
// proposal 0097 record, so the records no longer say which finding it closes
// or advances. Name it in the F-10.2.22 resolution or the T-10.2.2 or
// T-10.2.8 annotation, against the behavior it asserts.
//
// spec: 10.2 (Authentication), 25.4 (The lenny-ops Service)
func TestJWTSigningChaosCoverageIsRecorded_spec_10_2(t *testing.T) {
	root := repoRoot(t)
	records := requireFinding(t, buildGapsFindingsByID(t, root), "F-10.2.22").body
	for _, id := range []string{"T-10.2.2", "T-10.2.8"} {
		_, body := testGapsEntry(t, root, id)
		records += body
	}
	funcs := chaosTestFunc.FindAllStringSubmatch(fileText(t, root, jwtSigningChaosFile), -1)
	if len(funcs) == 0 {
		t.Fatalf("%s declares no test function", jwtSigningChaosFile)
	}
	for _, m := range funcs {
		if !strings.Contains(records, jwtSigningChaosFile+"::"+m[1]) {
			t.Errorf("%s::%s is named in no proposal 0097 record", jwtSigningChaosFile, m[1])
		}
	}
}

// diagnosis: the record test-reference parser or the tier-6 skip-reason
// matcher no longer reads the forms the records and the test file use, so
// the checks above pass without reading what they claim to.
//
// spec: 10.2 (Authentication)
func TestJWTSigningRecordParsers_spec_10_2(t *testing.T) {
	got := recordTestRefs("see `tests/tier4_integration/a_test.go::TestX_spec_10_2` and pkg/auth/jwt/keyset_test.go, not pkg/auth/jwt/keyset.go")
	want := [][2]string{
		{"tests/tier4_integration/a_test.go", "TestX_spec_10_2"},
		{"pkg/auth/jwt/keyset_test.go", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("recordTestRefs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("recordTestRefs[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	m := tier6SkipReason.FindStringSubmatch(`t.Skipf("blocked: T-17.5.2: %s names no key; export %s", name, ref)`)
	if m == nil || m[1] != "blocked: T-17.5.2: %s names no key; export %s" {
		t.Fatalf("tier6SkipReason captured %v, want the whole format string", m)
	}
	frags := skipReasonFragments(m[1])
	wantFrags := []string{"blocked: T-17.5.2: ", " names no key; export "}
	if strings.Join(frags, "|") != strings.Join(wantFrags, "|") {
		t.Errorf("skipReasonFragments = %q, want %q", frags, wantFrags)
	}
}
