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
	// chaos, security) may take per invocation. Production CI
	// configures the surrounding workflow timeout to a larger
	// value so a hung tier surfaces as a tier failure rather than
	// a workflow timeout. Override: LENNY_TEST_LONG_TIMEOUT.
	tierLongTimeout = tierTimeout("LENNY_TEST_LONG_TIMEOUT", 600*time.Second)

	// tierUnitTimeout caps each package in the unit tier. Without it Go's
	// own ten-minute per-package default applies, and
	// pkg/gateway/podlifecycle/podsession exceeds it: the package starts a
	// fresh envtest control plane per test, 138 times, at roughly seven
	// seconds each, so it passes in about fourteen minutes and is aborted
	// at ten. The budget is what keeps a passing package from being
	// reported as a failure; the per-test control plane is the thing that
	// wants fixing, and a shared environment there would bring this well
	// back under the default. Override: LENNY_TEST_UNIT_TIMEOUT.
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
