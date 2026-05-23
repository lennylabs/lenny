// SPDX-License-Identifier: MIT

//go:build load_cloud

// Tier-7 cloud-load suite. The PR-cadence smoke (tier-7 with the
// `load` tag) runs scenarios against a Kind cluster sized for a
// modest, deterministic baseline; the §12.7 production targets it
// gates against (100 concurrent claims, 50-child fan-out, 500
// streaming sessions) exceed the smoke pool's capacity, so the
// scenarios that exercise the §4.6 claim path on those rates are
// phase-gated and re-run here against a cloud cluster whose warm
// pools are sized for the spec.
//
// The cloud-load suite is opt-in. LENNY_LOAD_CLOUD_PROVIDERS names
// the cloud providers the operator drives the run against, the same
// way LENNY_CLOUD_PROVIDERS gates tier-6. Without that env the suite
// is a no-op — the run.sh wrapper in scripts/cloud/<provider>/ sets
// the env and the per-provider Terraform shape before invoking
// `lenny-test --tier load_cloud`.
//
// LENNY_LOAD_SCALE selects one of three deliberate sizing profiles:
//
//   - small      (the default): 10-50 concurrent sessions per
//                scenario, 5-10 child fan-out, 25-50 streaming
//                sessions. Targets a cloud-small cluster (2-4
//                t3.medium nodes on AWS, equivalent elsewhere).
//                Realistic for a single-tenant install. ~5-10 min
//                per scenario.
//   - medium     50-200 concurrent sessions, 10-25 child fan-out,
//                100-250 streaming sessions. Targets cloud-medium
//                (3-6 m5.large nodes). Realistic for a multi-
//                tenant install. ~15 min per scenario.
//   - production 200-1000+ concurrent sessions, 50 child fan-out,
//                500 streaming sessions. Targets cloud-large
//                (5-10 m5.xlarge nodes). Matches the §12.7 spec
//                targets verbatim. ~30 min per scenario.
//
// Each test reads the resolved profile through loadScale(t) and
// passes the matching k6 environment to the scenario.

package tier12_load_cloud_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/cloud"
	"github.com/lennylabs/lenny/tests/testinfra/load"
)

// loadTenant is the tenant every cloud-load scenario drives. The
// cloud bring-up (scripts/cloud/<provider>/run-load.sh) bootstraps
// it before invoking the suite.
const loadTenant = "acme"

// requireCloudLoad is the cloud-load admission gate. It t.Skips
// when LENNY_LOAD_CLOUD_PROVIDERS is unset (the PR-cadence run
// without the opt-in env) and t.Fatals when the env names a
// provider whose CLI is missing or unauthenticated. The contract
// mirrors tier-6's requireCloud: a configured-but-unreachable
// provider is a test failure, an unconfigured suite is a skip.
func requireCloudLoad(t *testing.T) cloud.Provider {
	t.Helper()
	if strings.TrimSpace(os.Getenv("LENNY_LOAD_CLOUD_PROVIDERS")) == "" {
		t.Skip("tier-7 cloud-load is opt-in. Export LENNY_LOAD_CLOUD_PROVIDERS=aws (or gcp / azure / a comma-separated subset) and run scripts/cloud/<provider>/run-load.sh to provision the cloud cluster + load runtime workload + run this suite.")
	}
	p := cloud.FromEnv()
	cloud.SkipUnlessAvailable(t, p)
	return p
}

// LoadScale enumerates the three sizing profiles. The active scale
// is read from LENNY_LOAD_SCALE; an empty or unrecognized value
// resolves to small.
type LoadScale string

const (
	LoadScaleSmall      LoadScale = "small"
	LoadScaleMedium     LoadScale = "medium"
	LoadScaleProduction LoadScale = "production"
)

// scaleConfig is the per-profile knob set every scenario reads.
// VUs is the k6 virtual-user count; Rate is the constant-arrival
// rate; Fanout sizes the delegation tree; StreamingSessions sizes
// the concurrent streaming budget; Duration bounds the run.
type scaleConfig struct {
	Scale             LoadScale
	VUs               int
	Rate              int
	Fanout            int
	StreamingSessions int
	Duration          time.Duration
}

// loadScale returns the resolved sizing profile for the active run.
// The profile's numbers are deliberately set to a realistic envelope
// rather than the §12.7 spec maximums; the production profile maps
// to the spec, the smaller two trade scale for run cost.
func loadScale(t *testing.T) scaleConfig {
	t.Helper()
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("LENNY_LOAD_SCALE")))
	switch LoadScale(raw) {
	case LoadScaleMedium:
		return scaleConfig{
			Scale:             LoadScaleMedium,
			VUs:               50,
			Rate:              20,
			Fanout:            15,
			StreamingSessions: 150,
			Duration:          5 * time.Minute,
		}
	case LoadScaleProduction:
		return scaleConfig{
			Scale:             LoadScaleProduction,
			VUs:               200,
			Rate:              100,
			Fanout:            50,
			StreamingSessions: 500,
			Duration:          15 * time.Minute,
		}
	default:
		// small is the default, matching what the cloud-small
		// Terraform shape can absorb.
		return scaleConfig{
			Scale:             LoadScaleSmall,
			VUs:               20,
			Rate:              5,
			Fanout:            10,
			StreamingSessions: 25,
			Duration:          1 * time.Minute,
		}
	}
}

// scenarioEnv translates a scaleConfig into the LENNY_RATE,
// LENNY_FANOUT, LENNY_STREAMING, LENNY_DURATION env the k6 scripts
// read. The map is merged onto the base smoke env (tenant, roles,
// user) by smokeOptions, mirroring tier-7's pattern.
func scenarioEnv(s scaleConfig, extra map[string]string) map[string]string {
	env := map[string]string{
		"LENNY_TENANT":     loadTenant,
		"LENNY_ROLES":      "tenant-admin",
		"LENNY_USER":       "alice",
		"LENNY_RATE":       fmt.Sprintf("%d", s.Rate),
		"LENNY_FANOUT":     fmt.Sprintf("%d", s.Fanout),
		"LENNY_STREAMING":  fmt.Sprintf("%d", s.StreamingSessions),
		"LENNY_DURATION":   s.Duration.String(),
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// cloudLoadOptions returns the load.Options every cloud-load test
// passes to load.RunScenario. The k6 invocation reads --duration
// and --vus from Options; the rate-bounded scenarios in the suite
// read LENNY_RATE through ExtraEnv and ignore --vus. The
// ScenarioRoot points at the cloud-load scenarios directory; the
// load.RunScenario default targets the smoke directory.
func cloudLoadOptions(s scaleConfig, baseURL string, extra map[string]string) load.Options {
	return load.Options{
		Duration:     s.Duration,
		VUs:          s.VUs,
		BaseURL:      baseURL,
		ExtraEnv:     scenarioEnv(s, extra),
		ScenarioRoot: "tests/tier12_load_cloud/scenarios",
	}
}

// gatewayBaseURL is the gateway address the suite drives. The
// cloud-load run-script (scripts/cloud/<provider>/run-load.sh)
// stands up a `kubectl port-forward svc/lenny-gateway` and exports
// LENNY_GATEWAY_BASE_URL pointing at the loopback port; the suite
// itself does not manage the port-forward, so a failure here is a
// missing precondition the operator owns.
func gatewayBaseURL(t *testing.T) string {
	t.Helper()
	u := strings.TrimSpace(os.Getenv("LENNY_GATEWAY_BASE_URL"))
	if u == "" {
		t.Fatalf("tier-7 cloud-load: LENNY_GATEWAY_BASE_URL is required; the cloud-load run script port-forwards svc/lenny-gateway and exports it. Run scripts/cloud/<provider>/run-load.sh instead of `go test` directly.")
	}
	return u
}

// assertScenarioRan is the per-scenario gate every cloud-load test
// closes with. It bounds the request error rate to a generous 10%
// — the cloud-load profile assumes a cluster sized for the §12.7
// target, so anything past 10% is a real regression rather than a
// smoke-fixture shortfall. The recorded baseline lives next to the
// scenario JSON; LENNY_UPDATE_BASELINE=1 reseeds it.
func assertScenarioRan(t *testing.T, scenario string, res load.Result) {
	t.Helper()
	const maxErrorRate = 0.10
	if res.ErrorRate > maxErrorRate {
		t.Errorf("§12.7 %s: request error rate %.1f%% exceeds the %.0f%% bound; "+
			"the cloud-load scenario did not exercise a healthy gateway",
			scenario, res.ErrorRate*100, maxErrorRate*100)
	}
	t.Logf("§12.7 %s: error rate %.2f%%, p99 %.0fms (baseline captured)",
		scenario, res.ErrorRate*100, res.MetricMS["p99"])
}

// TestMain iterates the cloud-load suite once per provider in
// LENNY_LOAD_CLOUD_PROVIDERS, mirroring the tier-6 pattern. Each
// iteration sets LENNY_CLOUD_PROVIDER so cloud.FromEnv resolves
// correctly inside the tests. When the env is unset, the suite
// runs once with the empty provider; the requireCloudLoad gate
// skips every test cleanly so the run is a no-op.
func TestMain(m *testing.M) {
	providers := cloud.ConfiguredLoadProviders()
	if len(providers) == 0 {
		os.Exit(m.Run())
	}
	overallCode := 0
	for _, p := range providers {
		_ = os.Setenv("LENNY_CLOUD_PROVIDER", string(p))
		fmt.Fprintf(os.Stderr, "tier-7 cloud-load: running suite against LENNY_CLOUD_PROVIDER=%s (out of %d configured)\n", p, len(providers))
		if code := m.Run(); code != 0 {
			overallCode = code
		}
	}
	os.Exit(overallCode)
}
