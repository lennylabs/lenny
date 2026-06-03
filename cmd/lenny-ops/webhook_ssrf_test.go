// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"strings"
	"testing"
)

// spec: §25.5 lines 2735-2745 ops.webhooks.{allowHTTP,domainAllowlist} —
// F-25.4.9. buildWebhookSSRF translates the Helm values into the
// validator the subscription service and delivery transport enforce. The
// assertions key on the pre-DNS gates (scheme, IP-literal, allowlist) so
// the test does not depend on live name resolution; the DNS-resolution
// branch is covered by eventsubscription's own ssrf_test with a stub
// resolver.
func TestBuildWebhookSSRF_spec_25_5(t *testing.T) {
	ctx := context.Background()

	// Strict default: an http:// callback is rejected at the scheme gate.
	strict := buildWebhookSSRF(false, "", "")
	err := strict.Validate(ctx, "http://hooks.acme.com/x")
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("default policy on http callback: err=%v, want a scheme rejection", err)
	}

	// allowHTTP opens the scheme gate: an http:// callback now advances past
	// the scheme check and is rejected later (here at the IP-literal gate),
	// proving the flag flowed through.
	httpOK := buildWebhookSSRF(true, "", "")
	err = httpOK.Validate(ctx, "http://127.0.0.1/x")
	if err == nil || !strings.Contains(err.Error(), "IP literal") {
		t.Errorf("allowHTTP policy on http IP callback: err=%v, want the IP-literal gate (scheme passed)", err)
	}

	// domainAllowlist rejects an off-list host at the allowlist gate.
	allowlisted := buildWebhookSSRF(false, "", "hooks.acme.com, *.acme.com")
	err = allowlisted.Validate(ctx, "https://evil.example.com/x")
	if err == nil || !strings.Contains(err.Error(), "domainAllowlist") {
		t.Errorf("off-allowlist host: err=%v, want the allowlist gate", err)
	}

	// An allowlisted host clears the allowlist gate (it fails later at DNS,
	// not at the allowlist), proving the wildcard entry was honored.
	err = allowlisted.Validate(ctx, "https://deploy.acme.com/x")
	if err != nil && strings.Contains(err.Error(), "domainAllowlist") {
		t.Errorf("wildcard-allowlisted host rejected at the allowlist gate: %v", err)
	}
}

// spec: §25.5 line 2743 — F-25.4.9. A malformed blockedCIDRs entry is
// skipped rather than aborting policy construction, so one typo does not
// disable the whole SSRF posture.
func TestBuildWebhookSSRFSkipsMalformedCIDR_spec_25_5(t *testing.T) {
	v := buildWebhookSSRF(false, "not-a-cidr, 10.0.0.0/8", "")
	if v == nil {
		t.Fatal("buildWebhookSSRF returned nil despite a valid trailing CIDR")
	}
	// The validator still functions: an http:// callback is rejected at the
	// scheme gate (a pre-DNS check), confirming the validator was built.
	if err := v.Validate(context.Background(), "http://hooks.acme.com/x"); err == nil {
		t.Error("validator built from a partially-malformed CIDR list admitted an http callback")
	}
}

// spec: §25.5 — F-25.4.9. splitCSV trims tokens and drops empties so an
// empty or whitespace-only flag yields the zero-policy default.
func TestSplitCSV(t *testing.T) {
	if got := splitCSV(""); got != nil {
		t.Errorf("splitCSV(\"\") = %v, want nil", got)
	}
	if got := splitCSV("  a , ,b ,"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("splitCSV trimmed result = %v, want [a b]", got)
	}
}
