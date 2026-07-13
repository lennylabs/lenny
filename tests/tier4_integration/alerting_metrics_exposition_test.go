// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §25.13 bundled-alerting observability
// gauges are exposed on the real cmd/lenny-gateway /metrics endpoint
// with the values derived from the chart-supplied startup inputs. This
// closes the loop that the unit tests (setter correctness) and the helm
// tests (env-var rendering) leave open: that LENNY_ALERTING_BUNDLE_FORMATS
// and LENNY_ALERTING_OVERRIDE_COUNT flow through gateway startup into a
// real Prometheus text exposition on the endpoint operators scrape.

package tier4_integration_test

import (
	"bufio"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: 25.13 (bundled-alerting metrics), 16.1 (platform metric registry)
// diagnosis: the §25.13 alerting gauges did not appear on the real
// gateway /metrics exposition, or appeared with the wrong values. Either
// the startup wiring registered the gauges on a registry the /metrics
// handler does not serve, or the chart-supplied bundle-format / override
// inputs did not flow through to the exposed gauge series. Operators
// cannot verify their bundling configuration is in effect (§25.13).
func TestAlertingMetricsExposedOnGatewayScrape(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	// §25.13 lines 4833-4836: the chart renders the §16.5 catalog into
	// one or more formats (prometheusrule, configmap) and stamps
	// lenny_alerting_rules_bundled{format}=1 for each rendered format;
	// requesting both formats renders two label series. The operator's
	// override count feeds lenny_alerting_rule_overrides.
	gw := gateway.StartWith(
		t,
		"--alerting-bundle-formats", "prometheusrule,configmap",
		"--alerting-override-count", "2",
	)

	resp, err := http.Get(gw.BaseURL() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want 200; body:\n%s", resp.StatusCode, body)
	}

	samples := parseGaugeSamples(t, string(body))

	// spec: §25.13 line 4836 — "1 if rules are rendered in the given
	// format (prometheusrule, configmap)." Both requested formats render,
	// so both label series must read 1 on the scraped exposition.
	for _, format := range []string{"prometheusrule", "configmap"} {
		name := `lenny_alerting_rules_bundled{format="` + format + `"}`
		got, ok := samples[name]
		if !ok {
			t.Errorf("/metrics did not expose %s (§25.13 line 4836); the gauge is not on the scraped registry", name)
			continue
		}
		if got != 1 {
			t.Errorf("%s = %v, want 1 (rendered) (§25.13 line 4836)", name, got)
		}
	}

	// spec: §25.13 line 4837 — "Count of operator-overridden rules from
	// monitoring.alertOverrides." The startup input was 2.
	if got, ok := samples["lenny_alerting_rule_overrides"]; !ok {
		t.Errorf("/metrics did not expose lenny_alerting_rule_overrides (§25.13 line 4837); the gauge is not on the scraped registry")
	} else if got != 2 {
		t.Errorf("lenny_alerting_rule_overrides = %v, want 2 (§25.13 line 4837)", got)
	}
}

// parseGaugeSamples reads a Prometheus text exposition and returns a map
// from the fully-rendered sample key (metric name plus its sorted label
// set exactly as exposed, e.g. `lenny_alerting_rules_bundled{format="configmap"}`)
// to its float value. Comment (# HELP / # TYPE) lines are skipped.
func parseGaugeSamples(t *testing.T, body string) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// A sample line is "<key> <value>[ <timestamp>]"; the key may
		// itself contain spaces only inside label values, which the
		// alerting gauges do not use, so a split on the last-but-value
		// space is unnecessary: split on the first space after the key.
		idx := strings.LastIndex(line, " ")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val, err := strconv.ParseFloat(strings.TrimSpace(line[idx+1:]), 64)
		if err != nil {
			continue
		}
		out[key] = val
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan /metrics body: %v", err)
	}
	return out
}
