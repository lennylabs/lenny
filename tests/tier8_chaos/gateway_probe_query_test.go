// SPDX-License-Identifier: MIT

//go:build chaos

// Regression coverage for the tier-8 gateway probe helpers. The chaos
// tests reach the gateway by exec'ing curl inside a probe pod, and the
// §25.9 audit query endpoints are driven with multi-parameter query
// strings. This test pins that every query parameter a chaos assertion
// sends actually reaches the gateway, so a probe result can be read as
// the gateway's answer to the query the test asked.

package tier8_chaos_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// auditSummarySince and auditSummaryUntil bound the §25.9 summary
// window the probe requests. They sit far enough in the past that they
// cannot coincide with the endpoint's default look-back, so an echoed
// bound that matches proves the requested value reached the gateway
// rather than the server-side default.
const (
	auditSummarySince = "2026-01-05T00:00:00Z"
	auditSummaryUntil = "2026-01-12T00:00:00Z"
)

// auditSummaryGroupBy is the non-default §25.9 grouping the probe
// requests. The endpoint defaults to eventType, so an echoed actorId
// proves the first parameter reached the gateway.
const auditSummaryGroupBy = "actorId"

// auditSummaryProbePath is a three-parameter §25.9 summary query. The
// parameters are joined by `&`, which is a shell background-job
// separator: a probe helper that interpolates the URL into an `sh -c`
// command line without quoting it sends only the first parameter and
// silently drops the rest.
const auditSummaryProbePath = "/v1/admin/audit-events/summary?groupBy=" + auditSummaryGroupBy +
	"&since=" + auditSummarySince + "&until=" + auditSummaryUntil

// auditSummaryDoc is the subset of the §25.9 summary response the probe
// decodes. The endpoint echoes the window it evaluated and the grouping
// it applied, so the decoded document reports which parameters the
// gateway parsed.
type auditSummaryDoc struct {
	TenantID string `json:"tenantId"`
	GroupBy  string `json:"groupBy"`
	Since    string `json:"since"`
	Until    string `json:"until"`
}

// spec: 25.9
// diagnosis: A multi-parameter §25.9 query issued through the tier-8
// gateway probe did not arrive intact. The spec defines the summary
// endpoint's parameters as "?since=, ?until=, ?groupBy=eventType|
// actorId|resourceType", and the endpoint echoes the window and the
// grouping it evaluated. The probe requests all three and asserts the
// echo matches. A failure means either the probe helper truncated the
// URL before it reached the gateway (the helper interpolates the URL
// into an `sh -c` command line, where an unquoted `&` starts a
// background job and discards the remaining parameters), or the gateway
// stopped honouring the requested window or grouping. Chaos assertions
// that read a decoded probe body are only meaningful when the query
// they sent is the query the gateway answered.
func TestGatewayProbeSendsEveryQueryParameter(t *testing.T) {
	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, gatewayDeployment) {
		t.Skipf("precondition not met: %s Deployment is not Ready (%s); the §25.9 audit query API is gateway-resident",
			gatewayDeployment, deploymentReadyState(t, c, gatewayDeployment))
	}
	if !deploymentReady(t, c, postgresDeployment) {
		t.Skipf("precondition not met: %s Deployment is not Ready (%s); §25.9 audit reads come from the authoritative store",
			postgresDeployment, deploymentReadyState(t, c, postgresDeployment))
	}

	probe := "chaos-queryparam-probe"
	gatewayIP := startGatewayProbePod(t, c, probe)

	p := curlGatewayAuth(t, c, probe, gatewayIP, auditSummaryProbePath)
	if p.curlExit != 0 {
		t.Fatalf("probe curl failed (exit %d) for %s\n%s", p.curlExit, auditSummaryProbePath, p.body)
	}
	if p.statusCode != 200 {
		t.Fatalf("GET %s returned HTTP %d, want 200\n%s",
			auditSummaryProbePath, p.statusCode, p.body)
	}

	var doc auditSummaryDoc
	if err := json.Unmarshal([]byte(p.body), &doc); err != nil {
		t.Fatalf("could not decode the §25.9 summary response: %v\n%s", err, p.body)
	}

	// The first parameter survives even an unquoted interpolation; it is
	// asserted so a regression that drops every parameter is separable
	// from one that drops only those after the first `&`.
	if doc.GroupBy != auditSummaryGroupBy {
		t.Errorf("summary groupBy = %q, want %q; the first query parameter did not reach the gateway\n%s",
			doc.GroupBy, auditSummaryGroupBy, p.body)
	}
	// since and until follow the `&` separators. An unquoted URL in the
	// probe's shell command line drops both, and the gateway falls back
	// to the §25.9 default 24-hour look-back anchored on its own clock.
	if doc.Since != auditSummarySince {
		t.Errorf("summary since = %q, want %q; the second query parameter did not reach the gateway\n%s",
			doc.Since, auditSummarySince, p.body)
	}
	if doc.Until != auditSummaryUntil {
		t.Errorf("summary until = %q, want %q; the third query parameter did not reach the gateway\n%s",
			doc.Until, auditSummaryUntil, p.body)
	}
}

// spec: 25.9
// diagnosis: The probe helpers' `sh -c` command line no longer passes
// the gateway URL as a single shell word. The probe pod runs curl
// through a shell, so a URL interpolated without quoting is subject to
// shell word splitting and control-operator parsing: the `&` joining
// two §25.9 query parameters starts a background job, curl receives the
// URL truncated at the first parameter, and the remaining parameters
// are executed as separate commands and discarded. This test runs the
// generated command line through a real shell with curl replaced by a
// stub that records its arguments, so it detects the truncation without
// a cluster. A failure means every chaos assertion that reads a decoded
// multi-parameter response is reading the answer to a different query.
func TestGatewayProbeScriptPassesURLAsOneShellWord(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> " + argsFile + "; done\n"
	if err := os.WriteFile(filepath.Join(dir, "curl"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write curl stub: %v", err)
	}

	url := gatewayProbeURL("10.0.0.1", auditSummaryProbePath)
	script := gatewayCurlScript(url,
		"X-Lenny-Tenant-ID: platform", "X-Lenny-Roles: platform-admin")

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("probe script did not run: %v\nscript: %s\n%s", err, script, out)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the probe script did not invoke curl at all: %v\nscript: %s", err, script)
	}
	args := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")

	if got := args[len(args)-1]; got != url {
		t.Errorf("curl received URL %q, want %q; the shell split the URL on a query-parameter separator\nscript: %s\nargs: %q",
			got, url, script, args)
	}
	for _, want := range []string{
		"X-Lenny-Tenant-ID: platform",
		"X-Lenny-Roles: platform-admin",
	} {
		if !containsArg(args, want) {
			t.Errorf("curl did not receive header %q as one argument\nargs: %q", want, args)
		}
	}
}

// containsArg reports whether args holds want as a complete element.
func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
