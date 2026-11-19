// SPDX-License-Identifier: MIT

package playground

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// TestNewMetricsRegistersThroughTheLabelValidator_spec_27_8 builds the
// playground metric set through the real label validator and checks
// that lenny_playground_page_views_total carries the auth_mode label
// §27.8 names. A label the validator refuses makes NewMetrics fail,
// which the gateway treats as fatal at startup, so the nil-error
// assertion and the gathered label name together pin the §27.8 cell.
//
// spec: §27.8 (Metrics); §16.1.1 (Attribute Naming)
func TestNewMetricsRegistersThroughTheLabelValidator_spec_27_8(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewMetrics(reg)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	m.pageView(AuthModeDev)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	const name = "lenny_playground_page_views_total"
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		if got := len(mf.GetMetric()); got != 1 {
			t.Fatalf("%s: got %d series, want 1", name, got)
		}
		labels := mf.GetMetric()[0].GetLabel()
		if len(labels) != 1 {
			t.Fatalf("%s: got %d label pairs, want 1: %v", name, len(labels), labels)
		}
		if labels[0].GetName() != "auth_mode" || labels[0].GetValue() != string(AuthModeDev) {
			t.Fatalf("%s: got label %s=%s, want auth_mode=%s",
				name, labels[0].GetName(), labels[0].GetValue(), AuthModeDev)
		}
		return
	}
	t.Fatalf("%s not found in the gathered families", name)
}
