// SPDX-License-Identifier: MIT

package zap_test

import (
	"os"
	"path/filepath"
	"testing"

	zaphelper "github.com/lennylabs/lenny/tests/testinfra/security/zap"
)

// fixture is a minimal OWASPZAPReport XML body with two High, one
// Medium, and one Low alert, exercising every riskcode bucket the
// parser tallies.
const fixture = `<?xml version="1.0" encoding="UTF-8"?>
<OWASPZAPReport>
  <site host="http://example.test">
    <alerts>
      <alertitem><alert>SQL Injection</alert><riskcode>3</riskcode></alertitem>
      <alertitem><alert>Path Traversal</alert><riskcode>3</riskcode></alertitem>
      <alertitem><alert>X-Content-Type-Options Missing</alert><riskcode>2</riskcode></alertitem>
      <alertitem><alert>Cookie No HttpOnly</alert><riskcode>1</riskcode></alertitem>
      <alertitem><alert>Informational</alert><riskcode>0</riskcode></alertitem>
    </alerts>
  </site>
</OWASPZAPReport>`

func TestParseReportCountsByRiskCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.xml")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	res, err := zaphelper.ParseReport(path)
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	if res.HighAlerts != 2 {
		t.Errorf("HighAlerts = %d, want 2", res.HighAlerts)
	}
	if res.MediumAlerts != 1 {
		t.Errorf("MediumAlerts = %d, want 1", res.MediumAlerts)
	}
	if res.LowAlerts != 1 {
		t.Errorf("LowAlerts = %d, want 1", res.LowAlerts)
	}
	if res.ReportPath != path {
		t.Errorf("ReportPath = %q, want %q", res.ReportPath, path)
	}
}

func TestParseReportEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.xml")
	body := `<?xml version="1.0"?><OWASPZAPReport></OWASPZAPReport>`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	res, err := zaphelper.ParseReport(path)
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	if res.HighAlerts != 0 || res.MediumAlerts != 0 || res.LowAlerts != 0 {
		t.Errorf("an empty report should produce zero alerts: %+v", res)
	}
}

func TestParseReportMissingFile(t *testing.T) {
	_, err := zaphelper.ParseReport(filepath.Join(t.TempDir(), "absent.xml"))
	if err == nil {
		t.Error("ParseReport against a missing file should error")
	}
}

func TestParseReportMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.xml")
	if err := os.WriteFile(path, []byte("not xml"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, err := zaphelper.ParseReport(path)
	if err == nil {
		t.Error("ParseReport against a non-XML file should error")
	}
}
