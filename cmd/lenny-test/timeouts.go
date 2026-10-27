// SPDX-License-Identifier: MIT

package main

import (
	"log/slog"
	"os"
	"time"
)

// Default timeouts and budgets the harness uses.
//
// Tier executors share these so a project-wide tune (slower CI,
// faster local) lands in one place. Helper packages outside
// cmd/lenny-test keep their own constants because their callers
// usually compose them with operation-specific budgets.
//
// The per-tier `go test -timeout` budgets are operator-tunable. Each
// reads an environment override at startup so a slower or faster host
// can move the budget without a code change; the default applies when
// the override is unset or unparseable. The defaults sit above the
// observed wall-clock runtime of each tier's suite with margin, because
// `go test -timeout` aborts the whole package binary (a panic with zero
// per-test failures) the moment the budget is exceeded, which the
// harness then reports as a tier failure even though every test passed.

const (
	// verdictRotationDepth bounds the number of rotated
	// verdict-<id>.json files on disk. The latest.json file is
	// always preserved.
	verdictRotationDepth = 20

	// commentDetailMaxChars truncates the per-tier "detail" cell
	// in renderComment so PR comments stay readable even when a
	// test emits a multi-kilobyte stack trace.
	commentDetailMaxChars = 120
)

var (
	// tierLongTimeout caps how long a long-running tier (load,
	// chaos) may take per invocation. Production CI
	// configures the surrounding workflow timeout to a larger
	// value so a hung tier surfaces as a tier failure rather than
	// a workflow timeout. Override: LENNY_TEST_LONG_TIMEOUT.
	tierLongTimeout = tierTimeout("LENNY_TEST_LONG_TIMEOUT", 600*time.Second)

	// tierSecurityTimeout caps the security tier. The tier-9 package is one
	// binary of roughly two hundred top-level tests, several of which wait
	// up to 120 seconds each for a Ready agent workload on the Kind
	// cluster, so its runtime tracks cluster load rather than test count.
	// Recorded whole-package runs on the development host range from about
	// 70 seconds on an idle cluster to 1634 seconds under load (passing
	// runs of 1616, 1631, and 1634 seconds), and runs under the shared
	// 600-second long-tier budget were aborted at 600 seconds with no
	// per-test failure. The default is a little over twice the slowest
	// recorded passing run. Override: LENNY_TEST_SECURITY_TIMEOUT.
	tierSecurityTimeout = securityTierTimeout()

	// tierUnitTimeout caps each package in the unit tier. Without it Go's
	// own ten-minute per-package default applies. The budget was raised to
	// 1800 seconds because pkg/gateway/podlifecycle/podsession started a
	// fresh envtest control plane per test and ran for 1700 to 1960 seconds
	// under the race detector on a loaded host. That package, together with
	// pkg/gateway/podlifecycle/podclaim, pkg/controller/warmpool, and
	// pkg/controller/sandbox, now shares one control plane per test binary
	// (TEST-GAPS T-17.4.22 and T-17.4.26); podsession passes in under a
	// minute and sandbox in about 20 seconds. The slowest package measured
	// since is pkg/gateway/session/recycle, which still starts a control
	// plane per test (TEST-GAPS T-17.4.28) and took 251 seconds inside a
	// scoped unit-tier run. The budget stays at 1800 seconds because a whole
	// `./...` run under load has not been measured since the change.
	// Override: LENNY_TEST_UNIT_TIMEOUT.
	tierUnitTimeout = tierTimeout("LENNY_TEST_UNIT_TIMEOUT", 1800*time.Second)

	// tierComponentTimeout caps the component tier. Component tests
	// stand up testcontainers (Postgres, Redis, MinIO) and run
	// several minutes of integration work, so the budget tracks the
	// long-tier cap rather than a tight per-package value. Override:
	// LENNY_TEST_COMPONENT_TIMEOUT.
	tierComponentTimeout = tierTimeout("LENNY_TEST_COMPONENT_TIMEOUT", 600*time.Second)

	// tierIntegrationTimeout caps the integration tier. The suite boots
	// cmd/lenny-gateway as a subprocess per test against the compose stack
	// and runs end-to-end over HTTP.
	//
	// The earlier four-to-five-minute figure was measured while most of the
	// suite was refused at session creation and returned in milliseconds.
	// With those tests reaching the gateway the battery runs about thirteen
	// minutes on a developer host, so a budget that was comfortable then
	// aborts a passing suite now. Override:
	// LENNY_TEST_INTEGRATION_TIMEOUT.
	tierIntegrationTimeout = tierTimeout("LENNY_TEST_INTEGRATION_TIMEOUT", 1800*time.Second)

	// tierE2EKindTimeout caps the e2e_kind tier. The suite drives a live
	// Kind cluster over kubectl and in-cluster probe pods, and one case
	// stands up and tears down a second Kind cluster of its own to
	// exercise etcd encryption at rest, so a whole-package run is tens of
	// minutes. The ten-minute figure predated that case and aborted the
	// package part-way through the first lifecycle test. Override:
	// LENNY_TEST_E2E_KIND_TIMEOUT.
	tierE2EKindTimeout = tierTimeout("LENNY_TEST_E2E_KIND_TIMEOUT", 3600*time.Second)

	// tierE2ECloudTimeout caps the e2e_cloud tier. Override:
	// LENNY_TEST_E2E_CLOUD_TIMEOUT.
	tierE2ECloudTimeout = tierTimeout("LENNY_TEST_E2E_CLOUD_TIMEOUT", 1800*time.Second)

	// tierDocsTimeout caps the docs tier. Override:
	// LENNY_TEST_DOCS_TIMEOUT.
	tierDocsTimeout = tierTimeout("LENNY_TEST_DOCS_TIMEOUT", 60*time.Second)
)

// securityTimeoutEnv names the environment override for the security
// tier's go-test budget, and defaultSecurityTimeout is the budget that
// applies when the override is unset or unparseable.
const (
	securityTimeoutEnv     = "LENNY_TEST_SECURITY_TIMEOUT"
	defaultSecurityTimeout = 3600 * time.Second
)

// securityTierTimeout resolves the security tier's go-test budget from
// securityTimeoutEnv, falling back to defaultSecurityTimeout.
func securityTierTimeout() time.Duration {
	return tierTimeout(securityTimeoutEnv, defaultSecurityTimeout)
}

// tierTimeout resolves a per-tier go-test budget. It returns the
// duration parsed from the named environment variable when that value
// is set and parses as a Go duration (for example "8m" or "450s"); it
// returns def otherwise. An unparseable override is logged and ignored
// so a typo degrades to the default rather than to a zero timeout that
// would abort the tier immediately.
func tierTimeout(envVar string, def time.Duration) time.Duration {
	raw := os.Getenv(envVar)
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		slog.Warn("ignoring unparseable tier timeout override; using default",
			"env", envVar, "value", raw, "default", def, "error", err)
		return def
	}
	return d
}
