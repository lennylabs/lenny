// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for the JWT key rotation runbook: the
// KMSSignQuotaApproaching alert resolves to the runbook's "KMS Sign quota"
// section, and the runbook describes the alias-map rotation and key set
// that §10.2 states rather than the per-process HMAC key, the published
// JWK Set, and the timed overlap window that the shared ES256 signing key
// replaced. No external infrastructure.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
)

const jwtKeyRotationSlug = "jwt-key-rotation"

// readJWTKeyRotationRunbook returns the runbook's front matter and body.
func readJWTKeyRotationRunbook(t *testing.T) (front, body string) {
	t.Helper()
	path := filepath.Join(repoRoot(t), "docs", "runbooks", jwtKeyRotationSlug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	f, rest, ok := splitFrontMatter(raw)
	if !ok {
		t.Fatalf("%s: missing or malformed front matter", path)
	}
	return string(f), string(rest)
}

// kramdownAnchor returns the auto-generated heading id kramdown assigns
// to a heading text: lowercased, punctuation dropped, spaces as hyphens.
func kramdownAnchor(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// TestKMSSignQuotaApproachingResolvesToRunbookSection_spec_16_5 asserts the
// KMSSignQuotaApproaching alert's runbook URL names a heading that exists
// in docs/runbooks/jwt-key-rotation.md, and that the runbook's front
// matter names the alert back with its warning severity.
//
// diagnosis: A failure means an operator following the alert's runbook_url
// annotation lands on a page without the "KMS Sign quota" section, or the
// runbook index no longer maps the alert to this runbook. Either the
// alert's section fragment, the runbook heading, or the runbook's
// triggers entry drifted.
//
// spec: 16.5 (KMSSignQuotaApproaching warning-alert row), 10.2 (KMS
// signing failure mode), 17.7 (runbooks in docs/runbooks/)
func TestKMSSignQuotaApproachingResolvesToRunbookSection_spec_16_5(t *testing.T) {
	var got rules.Rule
	for _, r := range rules.Catalog() {
		if r.Name == "KMSSignQuotaApproaching" {
			got = r
			break
		}
	}
	if got.Name == "" {
		t.Fatal("KMSSignQuotaApproaching missing from the alert catalog")
	}
	page, fragment, ok := strings.Cut(strings.TrimPrefix(got.RunbookURL, "https://docs.lenny.dev/runbooks/"), "#")
	if page != jwtKeyRotationSlug || !ok || fragment == "" {
		t.Fatalf("runbook URL = %q, want a section of %s", got.RunbookURL, jwtKeyRotationSlug)
	}

	front, body := readJWTKeyRotationRunbook(t)
	anchors := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		if h, found := strings.CutPrefix(line, "## "); found {
			anchors[kramdownAnchor(strings.TrimSpace(h))] = true
		}
	}
	if !anchors[fragment] {
		t.Errorf("runbook has no section whose anchor is %q; sections: %v", fragment, anchors)
	}

	var meta struct {
		Triggers   []runbookTrigger `yaml:"triggers"`
		Components []string         `yaml:"components"`
	}
	if err := yaml.Unmarshal([]byte(front), &meta); err != nil {
		t.Fatalf("parse front matter: %v", err)
	}
	found := false
	for _, tr := range meta.Triggers {
		if tr.Alert == got.Name {
			found = true
			if tr.Severity != string(got.Severity) {
				t.Errorf("trigger severity = %q, want %q", tr.Severity, got.Severity)
			}
		}
	}
	if !found {
		t.Errorf("front matter triggers do not name %s", got.Name)
	}
}

// TestJWTKeyRotationRunbookCoversEveryVerifier_spec_10_2 asserts the
// runbook lists lenny-ops as a component, because lenny-ops loads the
// JWT key set at startup and so rolls out at every rotation step, and
// that it states the ServiceAccount grants and the step-2 bootstrap
// admin credential rotation.
//
// diagnosis: A failure means the runbook omits a process that verifies
// Lenny-issued tokens, a KMS grant, or the admin credential rotation; an
// operator following it would leave lenny-ops on a stale key set, start
// a crashlooping rollout, or be locked out at the third rollout.
//
// spec: 10.2 (Signing topology; Verifier key set and rotation), 17.6
// (bootstrap admin credential rotation)
func TestJWTKeyRotationRunbookCoversEveryVerifier_spec_10_2(t *testing.T) {
	front, body := readJWTKeyRotationRunbook(t)
	var meta struct {
		Components []string `yaml:"components"`
	}
	if err := yaml.Unmarshal([]byte(front), &meta); err != nil {
		t.Fatalf("parse front matter: %v", err)
	}
	want := map[string]bool{"gateway": false, "tokenService": false, "ops": false}
	for _, c := range meta.Components {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for c, ok := range want {
		if !ok {
			t.Errorf("front matter components omit %q", c)
		}
	}
	for _, phrase := range []string{
		"platform:jwt-signing",
		"platform:jwt-verify",
		"lenny-ctl admin users rotate-token --user lenny-admin",
		"lenny-gateway", "lenny-token-service", "lenny-ops-sa",
		"GetPublicKey",
		"--kms-alias=",
		"unknown_kid",
		"CrashLoopBackOff",
		"KMS_SIGNING_UNAVAILABLE",
		"kms.signQuotaPerSecond",
	} {
		if !strings.Contains(body, phrase) {
			t.Errorf("runbook does not mention %q", phrase)
		}
	}
}

// retiredJWTSurfaces are the surfaces the shared ES256 signing key
// removed. A runbook step that still names one sends the operator to an
// endpoint, table, value, alias, or audit event that no longer exists.
var retiredJWTSurfaces = []string{
	"/.well-known/jwks.json",
	"signing_keys",
	"signingKeySealed",
	"signingKid",
	"platform:token-service-signing",
	"RotatingVerifier",
	"key_retired",
	"RetireExpired",
	"platform.jwt_signing_key_rotated",
	"--jwks-publish",
	"HMAC",
	"--bearer-trust-hmac-key-file",
}

// runbookSpecSectionRE matches a specification section number in published prose.
var runbookSpecSectionRE = regexp.MustCompile(`§\s*\d`)

// TestJWTKeyRotationRunbookNamesNoRetiredSurface_spec_10_2 asserts the
// runbook names none of the retired signing surfaces and cites no
// specification section number.
//
// diagnosis: A failure means the runbook still describes the per-process
// HMAC signer, the published JWK Set, the signing_keys table, the timed
// overlap window, or the dropped rotation audit event, or it cites a spec
// section number that reader-facing docs must not carry.
//
// spec: 10.2 (Verifier key set and rotation), 18 (no rotation audit event)
func TestJWTKeyRotationRunbookNamesNoRetiredSurface_spec_10_2(t *testing.T) {
	front, body := readJWTKeyRotationRunbook(t)
	all := front + "\n" + body
	for _, s := range retiredJWTSurfaces {
		if strings.Contains(all, s) {
			t.Errorf("runbook names retired surface %q", s)
		}
	}
	if loc := runbookSpecSectionRE.FindStringIndex(all); loc != nil {
		t.Errorf("runbook cites a spec section number: %q", all[loc[0]:min(len(all), loc[1]+8)])
	}
}
