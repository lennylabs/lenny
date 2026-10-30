// SPDX-License-Identifier: MIT

package supervisorselftest_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/supervisorselftest"
)

const fullReport = "nondumpable pass\nkillReach fail\ninitSigkillImmune pass\ninitSigstopImmune fail\n"

// spec: 4.7.11 (Adapter-Agent Security Boundary, Runtime supervisor, Property self-test)
func TestParseReportReadsEveryProperty_spec_4_7_11(t *testing.T) {
	got, err := supervisorselftest.ParseReport("\n" + fullReport + "\n")
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	want := map[string]bool{
		supervisorselftest.PropertyNondumpable:       true,
		supervisorselftest.PropertyKillReach:         false,
		supervisorselftest.PropertyInitSigkillImmune: true,
		supervisorselftest.PropertyInitSigstopImmune: false,
	}
	if len(got) != len(want) {
		t.Fatalf("results = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

// spec: 4.7.11 (Adapter-Agent Security Boundary, Runtime supervisor, Property self-test)
func TestParseReportRefusesMalformedReports_spec_4_7_11(t *testing.T) {
	cases := map[string]string{
		"empty":             "",
		"missing property":  "nondumpable pass\nkillReach pass\ninitSigkillImmune pass\n",
		"repeated property": fullReport + "killReach pass\n",
		"unknown property":  fullReport + "senderId pass\n",
		"unknown result":    "nondumpable ok\nkillReach pass\ninitSigkillImmune pass\ninitSigstopImmune pass\n",
		"extra field":       "nondumpable pass now\nkillReach pass\ninitSigkillImmune pass\ninitSigstopImmune pass\n",
		"name only":         "nondumpable\nkillReach pass\ninitSigkillImmune pass\ninitSigstopImmune pass\n",
	}
	for name, report := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := supervisorselftest.ParseReport(report); !errors.Is(err, supervisorselftest.ErrMalformedReport) {
				t.Fatalf("ParseReport err = %v, want ErrMalformedReport", err)
			}
		})
	}
}

// spec: 4.7.11 (Adapter-Agent Security Boundary, Runtime supervisor, Property self-test)
func TestGatingPropertiesAreNondumpableAndKillReach_spec_4_7_11(t *testing.T) {
	want := map[string]bool{
		supervisorselftest.PropertyNondumpable:       true,
		supervisorselftest.PropertyKillReach:         true,
		supervisorselftest.PropertyInitSigkillImmune: false,
		supervisorselftest.PropertyInitSigstopImmune: false,
	}
	for _, p := range supervisorselftest.Properties {
		if supervisorselftest.Gates(p) != want[p] {
			t.Errorf("Gates(%s) = %v, want %v", p, !want[p], want[p])
		}
	}
	if len(supervisorselftest.Properties) != len(want) {
		t.Errorf("Properties = %v, want the %d properties of the self-test", supervisorselftest.Properties, len(want))
	}
}
