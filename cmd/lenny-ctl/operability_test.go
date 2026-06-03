// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

// spec: §24.15 lenny-ctl operability command groups — me, audit, events,
// and upgrade — plus the §24.20 answer-file replay.
// F-24.15.1, F-24.15.3, F-24.15.4, F-24.20.2.

// --- F-24.15.1: me -----------------------------------------------------

func TestMeTargetsGateway_spec_24_15_1(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"subject":"alice"}`, "me")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/me" {
		t.Errorf("request: %s %s, want GET /v1/admin/me", got.method, got.path)
	}
}

func TestMeToolsAndOperations_spec_24_15_1(t *testing.T) {
	for _, c := range []struct {
		args []string
		path string
	}{
		{[]string{"me", "tools"}, "/v1/admin/me/authorized-tools"},
		{[]string{"me", "operations"}, "/v1/admin/me/operations"},
	} {
		code, got := runAgainstGateway(t, http.StatusOK, `{}`, c.args...)
		if code != 0 {
			t.Fatalf("%v: exit code %d, want 0", c.args, code)
		}
		if got.path != c.path {
			t.Errorf("%v: path %q, want %q", c.args, got.path, c.path)
		}
	}
}

func TestMeUnknownSubcommand_spec_24_15_1(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "http://gw:8080", "me", "bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("me bogus: exit code %d, want 2", code)
	}
}

// --- F-24.15.5: audit --------------------------------------------------

func TestAuditQuerySendsFilters_spec_24_15_5(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"items":[]}`,
		"audit", "query", "--since", "2026-06-01T00:00:00Z", "--event-type", "session.created", "--limit", "50")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.path != "/v1/admin/audit-events" {
		t.Errorf("path: %q", got.path)
	}
	if !strings.Contains(got.query, "since=") || !strings.Contains(got.query, "eventType=session.created") ||
		!strings.Contains(got.query, "limit=50") {
		t.Errorf("query: %q, want since/eventType/limit", got.query)
	}
}

func TestAuditGetAndSummary_spec_24_15_5(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"seq":42}`, "audit", "get", "42")
	if code != 0 || got.path != "/v1/admin/audit-events/42" {
		t.Errorf("audit get: code %d path %q", code, got.path)
	}
	code, got = runAgainstGateway(t, http.StatusOK, `{}`, "audit", "summary", "--since", "2026-06-01T00:00:00Z", "--group-by", "actorId")
	if code != 0 || got.path != "/v1/admin/audit-events/summary" {
		t.Errorf("audit summary: code %d path %q", code, got.path)
	}
	if !strings.Contains(got.query, "groupBy=actorId") {
		t.Errorf("summary query: %q", got.query)
	}
}

func TestAuditRetranslateAndRepublish_spec_24_15_5(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{}`,
		"audit", "retranslate", "42", "--translator-version", "1.4.0")
	if code != 0 || got.method != http.MethodPost || got.path != "/v1/admin/audit-events/42/retranslate" {
		t.Errorf("retranslate: code %d %s %s", code, got.method, got.path)
	}
	if got.body["translatorVersion"] != "1.4.0" {
		t.Errorf("translatorVersion: %v", got.body["translatorVersion"])
	}
	code, got = runAgainstGateway(t, http.StatusOK, `{}`, "audit", "republish", "42")
	if code != 0 || got.method != http.MethodPost || got.path != "/v1/admin/audit-events/42/republish" {
		t.Errorf("republish: code %d %s %s", code, got.method, got.path)
	}
}

func TestAuditDropPartitionRequiresBothFlags_spec_24_15_5(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "http://gw:8080", "audit", "drop-partition", "p1", "--force"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("drop-partition without --acknowledge-data-loss: exit %d, want 2", code)
	}
}

func TestAuditDropPartitionSendsForceAndAck_spec_24_15_5(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{}`,
		"audit", "drop-partition", "audit_2026_01", "--force", "--acknowledge-data-loss")
	if code != 0 || got.method != http.MethodPost || got.path != "/v1/admin/audit-partitions/audit_2026_01/drop" {
		t.Errorf("drop-partition: code %d %s %s", code, got.method, got.path)
	}
	if got.query != "force=true" {
		t.Errorf("query: %q, want force=true", got.query)
	}
	if got.body["acknowledgeDataLoss"] != true || got.body["partition"] != "audit_2026_01" {
		t.Errorf("body: %+v", got.body)
	}
}

// --- F-24.15.3: events -------------------------------------------------

func TestEventsListTargetsOps_spec_24_15_3(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"items":[]}`,
		"events", "list", "--since", "2026-06-01T00:00:00Z", "--type", "ops.health_status_changed", "--limit", "10")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if !strings.HasPrefix(got.path, "/v1/admin/events?") {
		t.Errorf("path: %q, want /v1/admin/events?...", got.path)
	}
	if !strings.Contains(got.path, "eventType=ops.health_status_changed") || !strings.Contains(got.path, "limit=10") {
		t.Errorf("path query: %q", got.path)
	}
}

func TestEventsBufferTargetsGateway_spec_24_15_3(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"events":[]}`, "events", "buffer")
	if code != 0 || got.path != "/v1/admin/events/buffer" {
		t.Errorf("events buffer: code %d path %q", code, got.path)
	}
}

func TestEventsSubscriptionsCRUD_spec_24_15_3(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"subscriptions":[]}`, "events", "subscriptions", "list")
	if code != 0 || got.path != "/v1/admin/event-subscriptions" {
		t.Errorf("subs list: code %d path %q", code, got.path)
	}
	code, got = runAgainstOps(t, http.StatusCreated, `{"id":"sub-1"}`,
		"events", "subscriptions", "create", "--url", "https://hooks.acme.com/lenny", "--types", "ops.escalation_created,ops.health_status_changed")
	if code != 0 || got.method != http.MethodPost {
		t.Errorf("subs create: code %d method %s", code, got.method)
	}
	if got.body["url"] != "https://hooks.acme.com/lenny" {
		t.Errorf("url: %v", got.body["url"])
	}
	types, _ := got.body["eventTypes"].([]any)
	if len(types) != 2 || types[0] != "ops.escalation_created" {
		t.Errorf("eventTypes: %+v", got.body["eventTypes"])
	}
	code, got = runAgainstOps(t, http.StatusOK, `{}`, "events", "subscriptions", "delete", "sub-1")
	if code != 0 || got.method != http.MethodDelete || got.path != "/v1/admin/event-subscriptions/sub-1" {
		t.Errorf("subs delete: code %d %s %s", code, got.method, got.path)
	}
}

// --- F-24.15.4: upgrade (platform state machine) -----------------------

func TestUpgradeCheckAndStatus_spec_24_15_4(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"available":false}`, "upgrade", "check")
	if code != 0 || got.path != "/v1/admin/platform/upgrade-check" {
		t.Errorf("upgrade check: code %d path %q", code, got.path)
	}
	code, got = runAgainstOps(t, http.StatusOK, `{"phase":"idle"}`, "upgrade", "status")
	if code != 0 || got.path != "/v1/admin/platform/upgrade/status" {
		t.Errorf("upgrade status: code %d path %q", code, got.path)
	}
}

func TestUpgradeStartSendsVersionAndConfirm_spec_24_15_4(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusAccepted, `{"phase":"preflight"}`,
		"upgrade", "start", "--version", "1.5.0", "--confirm")
	if code != 0 || got.method != http.MethodPost {
		t.Fatalf("upgrade start: code %d method %s", code, got.method)
	}
	if !strings.HasPrefix(got.path, "/v1/admin/platform/upgrade/start") || !strings.Contains(got.path, "confirm=true") {
		t.Errorf("path: %q", got.path)
	}
	if got.body["version"] != "1.5.0" {
		t.Errorf("version: %v", got.body["version"])
	}
}

func TestUpgradeStartRequiresVersion_spec_24_15_4(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "upgrade", "start"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("upgrade start without --version: exit %d, want 2", code)
	}
}

func TestUpgradeRollbackSendsReason_spec_24_15_4(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"phase":"rolling_back"}`,
		"upgrade", "rollback", "--confirm", "--reason", "verify failed")
	if code != 0 || got.method != http.MethodPost {
		t.Fatalf("rollback: code %d method %s", code, got.method)
	}
	if !strings.Contains(got.path, "confirm=true") {
		t.Errorf("path: %q", got.path)
	}
	if got.body["reason"] != "verify failed" {
		t.Errorf("reason: %v", got.body["reason"])
	}
}

func TestUpgradeUnknownSubcommand_spec_24_15_4(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "upgrade", "bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("upgrade bogus: exit %d, want 2", code)
	}
}

// --- F-24.20.2: upgrade --answers (chart replay) -----------------------

func TestUpgradeAnswersDryRunComposesWithoutHelm_spec_24_20_2(t *testing.T) {
	file := writeAnswerFile(t, "answers.yaml", "environment: local\ntier: tier1\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"upgrade", "--answers", file, "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("upgrade --answers --dry-run: exit %d, want 0; stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "# Composed Helm values:") || !strings.Contains(out, "helm upgrade lenny") {
		t.Errorf("dry-run output missing composed values or helm-upgrade command:\n%s", out)
	}
	if !strings.Contains(stderr.String(), "--dry-run set; not invoking helm") {
		t.Errorf("dry-run should not invoke helm: stderr=%s", stderr.String())
	}
}

func TestUpgradeAnswersRequiresFile_spec_24_20_2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"upgrade", "--answers", "/no/such/file.yaml", "--dry-run"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("missing answer file: exit %d, want 1", code)
	}
}

func TestHelmUpgradeArgsLayersPresetThenValues_spec_24_20_2(t *testing.T) {
	a := installAnswers{}
	applyAnswerDefaults(&a)
	args := helmUpgradeArgs(a, "charts/lenny", "charts/lenny/presets/values-tier1.yaml", "/tmp/v.yaml", false)
	joined := strings.Join(args, " ")
	if !strings.HasPrefix(joined, "upgrade lenny charts/lenny") {
		t.Errorf("args: %q", joined)
	}
	// preset must precede the composed values so per-question values win.
	pi := strings.Index(joined, "presets/values-tier1.yaml")
	vi := strings.Index(joined, "/tmp/v.yaml")
	if pi < 0 || vi < 0 || pi > vi {
		t.Errorf("preset must precede values: %q", joined)
	}
	if strings.Contains(joined, "--dry-run") {
		t.Errorf("non-dry-run args should not carry --dry-run: %q", joined)
	}
	dry := helmUpgradeArgs(a, "charts/lenny", "p.yaml", "/tmp/v.yaml", true)
	if !strings.Contains(strings.Join(dry, " "), "--dry-run") {
		t.Errorf("dry-run args missing --dry-run: %v", dry)
	}
}
