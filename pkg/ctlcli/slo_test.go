// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"strings"
	"testing"
)

// TestCmdSLOExportRendersOpenSLO confirms `slo export` prints OpenSLO v1
// documents for the requested tier offline. spec: §16.10 lines 732-736.
func TestCmdSLOExportRendersOpenSLO(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := cmdSLO([]string{"export", "--tier", "tier3"}, &out, &errBuf); code != 0 {
		t.Fatalf("slo export exit = %d, stderr=%q", code, errBuf.String())
	}
	s := out.String()
	for _, want := range []string{"apiVersion: openslo/v1", "kind: SLO", "kind: AlertPolicy", "kind: AlertNotificationTarget", `deployment_tier: tier3`} {
		if !strings.Contains(s, want) {
			t.Errorf("slo export output missing %q", want)
		}
	}
	if strings.Contains(s, "__DEPLOYMENT_TIER__") {
		t.Error("slo export left the tier placeholder unsubstituted")
	}
}

// TestCmdSLOExportEmitsConcreteNotificationTarget confirms the CLI threads
// the concrete OpenSLODefaultNotificationTarget through the renderer so the
// offline export carries a schema-valid AlertNotificationTarget with no
// placeholder target name. spec: §16.10 lines 732-736.
func TestCmdSLOExportEmitsConcreteNotificationTarget(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := cmdSLO([]string{"export"}, &out, &errBuf); code != 0 {
		t.Fatalf("slo export exit = %d, stderr=%q", code, errBuf.String())
	}
	s := out.String()
	if !strings.Contains(s, "kind: AlertNotificationTarget") {
		t.Error("slo export omitted the shared AlertNotificationTarget document")
	}
	// The concrete default target name must appear so downstream AlertPolicy
	// references resolve; an empty or placeholder name would break the round-trip.
	if !strings.Contains(s, "webhook") {
		t.Errorf("slo export missing the concrete notification target name, output=%q", s)
	}
}

// TestCmdSLOExportDefaultsTier1 confirms the default tier is tier1.
func TestCmdSLOExportDefaultsTier1(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := cmdSLO([]string{"export"}, &out, &errBuf); code != 0 {
		t.Fatalf("slo export exit = %d", code)
	}
	if !strings.Contains(out.String(), "deployment_tier: tier1") {
		t.Error("default tier export is not tier1")
	}
}

func TestCmdSLORejectsUnknownSubcommandAndFormat(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := cmdSLO(nil, &out, &errBuf); code != 2 {
		t.Errorf("bare slo exit = %d, want 2", code)
	}
	out.Reset()
	errBuf.Reset()
	if code := cmdSLO([]string{"validate"}, &out, &errBuf); code != 2 {
		t.Errorf("unknown subcommand exit = %d, want 2", code)
	}
	out.Reset()
	errBuf.Reset()
	if code := cmdSLO([]string{"export", "--format", "json"}, &out, &errBuf); code != 2 {
		t.Errorf("unsupported format exit = %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Errorf("unsupported format wrote to stdout: %q", out.String())
	}
}
