// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for proposal 0021 finding F-COV-2: the
// audit-events query API is gateway-resident, so the hand-authored
// operator-guide pages must not attribute the audit surface to
// `lenny-ops`. Before this fix, agent-operability.md listed the audit
// query API among the surfaces `lenny-ops` exposes and asserted audit
// works during a gateway outage; ingress-and-tls.md listed "audit query"
// among the surfaces the `lenny-ops` Ingress serves; and lenny-ctl.md
// routed the `lenny-ctl audit` family to `lenny-ops`. Those claims are
// false once audit is gateway-resident (served at /v1/admin/audit-events,
// routed by pkg/ctlcli/operability.go). Each test below asserts the
// reconciled outcome and fails against the pre-fix text.
//
// These tests are NOT under a build tag: they read the repository state
// directly and need no external infrastructure.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agentOperabilityPath is the published agent-operability operator-guide page.
func agentOperabilityPath(root string) string {
	return filepath.Join(root, "docs", "operator-guide", "agent-operability.md")
}

// ingressTLSPath is the published Ingress-and-TLS operator-guide page.
func ingressTLSPath(root string) string {
	return filepath.Join(root, "docs", "operator-guide", "ingress-and-tls.md")
}

// lennyCtlPath is the published lenny-ctl reference operator-guide page.
func lennyCtlPath(root string) string {
	return filepath.Join(root, "docs", "operator-guide", "lenny-ctl.md")
}

// readDoc reads a markdown page or fails the test.
func readDoc(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// section returns the slice of body between the heading whose text contains
// `heading` and the next heading at the same-or-shallower level, so a claim
// can be asserted within a specific section rather than the whole page.
func section(body, heading string) string {
	lines := strings.Split(body, "\n")
	start := -1
	var startLevel int
	for i, ln := range lines {
		if start < 0 {
			if strings.HasPrefix(ln, "#") && strings.Contains(ln, heading) {
				start = i
				startLevel = headingLevel(ln)
			}
			continue
		}
		if strings.HasPrefix(ln, "#") && headingLevel(ln) <= startLevel {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start < 0 {
		return ""
	}
	return strings.Join(lines[start:], "\n")
}

// headingLevel counts the leading '#' run of an ATX heading line.
func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	return n
}

// spec: 15.1 (audit-events host), 25.9 (audit query API on the gateway),
// 25.4 (lenny-ops surface prose). F-COV-2.
//
// The "surface `lenny-ops` exposes" table on agent-operability.md must not
// list the audit query API, because that API is gateway-resident. This
// fails against the pre-fix row `| Audit log query API | ... |
// /v1/admin/audit-events |` under that heading.
func TestAgentOperabilitySurfaceTableDropsAuditFromLennyOps(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, agentOperabilityPath(root))
	sec := section(body, "The surface `lenny-ops` exposes")
	if sec == "" {
		t.Fatal("agent-operability.md: could not find the lenny-ops surface-table section")
	}
	if strings.Contains(sec, "Audit log query API") {
		t.Errorf("agent-operability.md: the lenny-ops surface table still lists the " +
			"audit log query API, but audit is gateway-resident (F-COV-2)")
	}
}

// spec: 25.9 (gateway-resident audit), 25.15 (failure-mode analysis). F-COV-2.
//
// The "Gateway down, `lenny-ops` up" degradation row must state that audit
// queries fail (audit is gateway-resident), not that audit still works.
// Fails against the pre-fix row that lists "audit" among the surfaces that
// "all work" during a gateway outage.
func TestAgentOperabilityGatewayDownRowMarksAuditUnavailable(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, agentOperabilityPath(root))
	row := degradationRow(body, "Gateway down")
	if row == "" {
		t.Fatal("agent-operability.md: could not find the 'Gateway down' degradation row")
	}
	if !strings.Contains(row, "gateway-resident") {
		t.Errorf("agent-operability.md: the 'Gateway down' row does not mark audit as "+
			"gateway-resident/unavailable during a gateway outage (F-COV-2); row: %q", row)
	}
	// The pre-fix row asserted audit works during a gateway outage by listing
	// "audit" in the working set. After the fix, audit is called out as failing.
	if strings.Contains(row, "audit, drift") {
		t.Errorf("agent-operability.md: the 'Gateway down' row still lists audit among "+
			"the surfaces that work during a gateway outage (F-COV-2); row: %q", row)
	}
}

// spec: 25.5 (dependencies), 25.15 (failure-mode analysis). F-COV-2.
//
// The two lenny-ops-Postgres degradation rows ("Postgres down" and
// "Postgres + Redis both down") must not attribute audit-query
// unavailability to lenny-ops's Postgres. Fails against the pre-fix rows
// that listed "Audit queries ... unavailable" / "Unavailable: audit".
func TestAgentOperabilityPostgresRowsDoNotOwnAudit(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, agentOperabilityPath(root))
	for _, key := range []string{"Postgres down", "Postgres + Redis both down"} {
		row := degradationRow(body, key)
		if row == "" {
			t.Fatalf("agent-operability.md: could not find the %q degradation row", key)
		}
		low := strings.ToLower(row)
		// The reconciled rows may still mention audit to say it tracks the
		// gateway's Postgres; the defect is asserting lenny-ops's Postgres
		// makes audit unavailable.
		if strings.Contains(low, "audit queries, backup") || strings.Contains(low, "unavailable: audit") {
			t.Errorf("agent-operability.md: the %q row still attributes audit-query "+
				"unavailability to lenny-ops's store (F-COV-2); row: %q", key, row)
		}
	}
}

// spec: 15.1 (audit-events host), 17.x (Ingress topology). F-COV-2.
//
// The `lenny-ops` Ingress "What it serves" list must not include "audit
// query". Fails against the pre-fix list that named "audit query" among the
// lenny-ops Ingress surfaces.
func TestIngressLennyOpsDoesNotServeAudit(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, ingressTLSPath(root))
	sec := section(body, "`lenny-ops` Ingress")
	if sec == "" {
		t.Fatal("ingress-and-tls.md: could not find the lenny-ops Ingress section")
	}
	// "What it serves." is bold prose, not a heading. Isolate the served-
	// surface list: the sentence introduced by "**What it serves.**" up to its
	// first sentence-terminating period. The reconciled page may still mention
	// audit in a following sentence to say it is gateway-resident; the defect
	// is naming "audit query" in the served-surface list itself.
	marker := "**What it serves.**"
	idx := strings.Index(sec, marker)
	if idx < 0 {
		t.Fatal("ingress-and-tls.md: could not find the lenny-ops 'What it serves' prose")
	}
	rest := sec[idx+len(marker):]
	servedList := rest
	if end := strings.Index(rest, "."); end >= 0 {
		servedList = rest[:end]
	}
	if strings.Contains(servedList, "audit query") {
		t.Errorf("ingress-and-tls.md: the lenny-ops Ingress 'What it serves' list still "+
			"names 'audit query', but audit is gateway-resident (F-COV-2); list: %q", servedList)
	}
}

// spec: 25.14 (lenny-ctl routing), 25.9 (gateway-resident audit). F-COV-2.
//
// The server-discovery routing bullet must not list `audit` among the
// lenny-ops-targeted operability commands. Fails against the pre-fix bullet
// that listed `audit` in the lenny-ops command set.
func TestLennyCtlRoutingBulletMovesAuditToGateway(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, lennyCtlPath(root))
	sec := section(body, "Server discovery and routing")
	if sec == "" {
		t.Fatal("lenny-ctl.md: could not find the server-discovery section")
	}
	opsBullet := ""
	for _, ln := range strings.Split(sec, "\n") {
		if strings.Contains(ln, "**`lenny-ops`**") && strings.Contains(ln, "Section 25 operability") {
			opsBullet = ln
			break
		}
	}
	if opsBullet == "" {
		t.Fatal("lenny-ctl.md: could not find the lenny-ops routing bullet")
	}
	if strings.Contains(opsBullet, "`audit`") {
		t.Errorf("lenny-ctl.md: the lenny-ops routing bullet still lists `audit`, but the "+
			"audit family targets the gateway (F-COV-2); bullet: %q", opsBullet)
	}
}

// spec: 25.14 (lenny-ctl routing), 25.9 (gateway-resident audit). F-COV-2.
//
// The `### Audit (lenny-ctl audit)` command-reference subsection sits under
// the "## Agent-Operability Commands" heading whose intro says the commands
// target `lenny-ops`. After the fix the audit subsection must carry a
// gateway-targeting exception so the page does not misclassify audit. Fails
// against the pre-fix page, which had no such annotation.
func TestLennyCtlAuditSubsectionAnnotatedGatewayTargeted(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, lennyCtlPath(root))
	sec := section(body, "Audit (`lenny-ctl audit`)")
	if sec == "" {
		t.Fatal("lenny-ctl.md: could not find the Audit command-reference subsection")
	}
	low := strings.ToLower(sec)
	if !strings.Contains(low, "gateway") {
		t.Errorf("lenny-ctl.md: the Audit subsection under the 'target lenny-ops' heading " +
			"is not annotated as gateway-targeted (F-COV-2)")
	}
}

// spec: 25.14 (lenny-ctl routing). F-COV-2.
//
// The in-page link the reconciled Agent-Operability intro adds
// ([Audit](#audit-lenny-ctl-audit)) must resolve to a live heading on the
// same page, so the exception pointer does not 404. Reuses the slug
// derivation from embedded_mode_anchors_test.go.
func TestLennyCtlAuditExceptionAnchorResolves(t *testing.T) {
	root := repoRoot(t)
	path := lennyCtlPath(root)
	slugs, err := headingSlugs(path)
	if err != nil {
		t.Fatalf("read heading slugs from %s: %v", path, err)
	}
	const anchor = "audit-lenny-ctl-audit"
	if !slugs[anchor] {
		t.Errorf("lenny-ctl.md: the Agent-Operability intro links to #%s, but no heading "+
			"produces that slug (F-COV-2 in-page anchor)", anchor)
	}
}

// degradationRow returns the "Failure modes in brief" table row whose first
// cell contains key, or "" if none matches.
func degradationRow(body, key string) string {
	for _, ln := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(ln), "|") {
			continue
		}
		cells := strings.SplitN(ln, "|", 3)
		if len(cells) < 3 {
			continue
		}
		if strings.Contains(cells[1], key) {
			return ln
		}
	}
	return ""
}
