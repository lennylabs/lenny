// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// policyFixture holds the three admin inventories the §24.14 audit joins.
type policyFixture struct {
	policies string
	pools    string
	runtimes string
}

// runPolicyAudit spins a fake gateway serving the three admin endpoints
// the §24.14 join reads, runs `policy audit-isolation`, and returns the
// exit code plus the parsed JSON report.
func runPolicyAudit(t *testing.T, f policyFixture, args ...string) (int, map[string]any, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/admin/delegation-policies":
			_, _ = w.Write([]byte(f.policies))
		case "/v1/admin/pools":
			_, _ = w.Write([]byte(f.pools))
		case "/v1/admin/runtimes":
			_, _ = w.Write([]byte(f.runtimes))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	full := append([]string{"--api-url", srv.URL, "policy", "audit-isolation"}, args...)
	var stdout, stderr bytes.Buffer
	code := run(full, &stdout, &stderr)
	var report map[string]any
	if stdout.Len() > 0 {
		_ = json.Unmarshal(stdout.Bytes(), &report)
	}
	return code, report, stderr.String()
}

// TestPolicyAuditIsolation_ReportsViolation_spec_24_14 drives the full
// join: a rule matching an agent-type runtime in both a microvm pool
// (parent) and a runc pool (target) is a monotonicity violation.
func TestPolicyAuditIsolation_ReportsViolation_spec_24_14(t *testing.T) {
	f := policyFixture{
		policies: `{"items":[{"name":"team","tenantId":"acme",
			"rules":[{"target":{"types":["agent"]},"allow":true}]}]}`,
		pools: `{"items":[
			{"name":"kata-pool","runtimeRef":"coder","isolationProfile":"microvm"},
			{"name":"runc-pool","runtimeRef":"chat","isolationProfile":"standard"}]}`,
		runtimes: `{"items":[
			{"name":"coder","type":"agent","labels":{}},
			{"name":"chat","type":"agent","labels":{}}]}`,
	}
	code, report, stderrOut := runPolicyAudit(t, f)
	if code != 0 {
		t.Fatalf("exit code %d, stderr=%s", code, stderrOut)
	}
	if got := report["violationCount"]; got != float64(1) {
		t.Fatalf("violationCount: got %v, want 1 (report=%+v)", got, report)
	}
	vs, _ := report["violations"].([]any)
	if len(vs) != 1 {
		t.Fatalf("want 1 violation row, got %d", len(vs))
	}
	v := vs[0].(map[string]any)
	if v["sourcePool"] != "kata-pool" || v["targetPool"] != "runc-pool" {
		t.Errorf("pools: source=%v target=%v", v["sourcePool"], v["targetPool"])
	}
	if v["sourceProfile"] != "microvm" || v["targetProfile"] != "standard" {
		t.Errorf("profiles: source=%v target=%v", v["sourceProfile"], v["targetProfile"])
	}
}

// TestPolicyAuditIsolation_Clean_spec_24_14 verifies an empty report
// when every matched pool is at least as restrictive as its peers.
func TestPolicyAuditIsolation_Clean_spec_24_14(t *testing.T) {
	f := policyFixture{
		policies: `{"items":[{"name":"team",
			"rules":[{"target":{"types":["agent"]},"allow":true}]}]}`,
		pools: `{"items":[
			{"name":"kata-a","runtimeRef":"coder","isolationProfile":"microvm"},
			{"name":"kata-b","runtimeRef":"chat","isolationProfile":"microvm"}]}`,
		runtimes: `{"items":[
			{"name":"coder","type":"agent"},{"name":"chat","type":"agent"}]}`,
	}
	code, report, stderrOut := runPolicyAudit(t, f)
	if code != 0 {
		t.Fatalf("exit code %d, stderr=%s", code, stderrOut)
	}
	if got := report["violationCount"]; got != float64(0) {
		t.Fatalf("violationCount: got %v, want 0", got)
	}
}

// TestPolicyAuditIsolation_LabelMatchAcrossRuntimes_spec_24_14 verifies
// the pool→runtime→label resolution: a label-scoped rule only matches
// pools whose runtime carries the label.
func TestPolicyAuditIsolation_LabelMatchAcrossRuntimes_spec_24_14(t *testing.T) {
	f := policyFixture{
		policies: `{"items":[{"name":"plat",
			"rules":[{"target":{"matchLabels":{"team":"platform"}},"allow":true}]}]}`,
		pools: `{"items":[
			{"name":"kata","runtimeRef":"coder","isolationProfile":"microvm"},
			{"name":"runc-in","runtimeRef":"chat","isolationProfile":"standard"},
			{"name":"runc-out","runtimeRef":"ext","isolationProfile":"standard"}]}`,
		runtimes: `{"items":[
			{"name":"coder","type":"agent","labels":{"team":"platform"}},
			{"name":"chat","type":"agent","labels":{"team":"platform"}},
			{"name":"ext","type":"agent","labels":{"team":"support"}}]}`,
	}
	code, report, _ := runPolicyAudit(t, f)
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	vs, _ := report["violations"].([]any)
	if len(vs) != 1 {
		t.Fatalf("want 1 violation (only platform-labelled pools), got %d: %+v", len(vs), report)
	}
	if vs[0].(map[string]any)["targetPool"] != "runc-in" {
		t.Errorf("target: %v, want runc-in (runc-out is support-labelled)", vs[0].(map[string]any)["targetPool"])
	}
}

// TestPolicyAuditIsolation_RejectsArgs_spec_24_14 verifies the
// no-argument contract.
func TestPolicyAuditIsolation_RejectsArgs_spec_24_14(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "http://127.0.0.1:0", "policy", "audit-isolation", "extra"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code: got %d, want 2", code)
	}
}

// TestPolicyUnknownSubcommand_spec_24_14 verifies the group rejects an
// unknown subcommand.
func TestPolicyUnknownSubcommand_spec_24_14(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "http://127.0.0.1:0", "policy", "frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code: got %d, want 2", code)
	}
}

// TestPolicyAuditIsolation_GatewayError_spec_24_14 verifies a fetch
// failure surfaces a non-zero exit.
func TestPolicyAuditIsolation_GatewayError_spec_24_14(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL_ERROR","message":"boom"}}`))
	}))
	defer srv.Close()
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", srv.URL, "policy", "audit-isolation"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code: got %d, want 1", code)
	}
}
