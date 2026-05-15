// SPDX-License-Identifier: MIT

package main

import "time"

// Default timeouts and budgets the harness uses.
//
// Tier executors share these so a project-wide tune (slower CI,
// faster local) lands in one place. Helper packages outside
// cmd/lenny-test keep their own constants because their callers
// usually compose them with operation-specific budgets.

const (
	// tierLongTimeout caps how long a long-running tier (load,
	// chaos, security) may take per invocation. Production CI
	// configures the surrounding workflow timeout to a larger
	// value so a hung tier surfaces as a tier failure rather than
	// a workflow timeout.
	tierLongTimeout = 600 * time.Second

	// tierComponentTimeout caps the component tier. Component
	// tests can stand up testcontainers and run several minutes
	// of integration work; this matches what the harness uses
	// when invoking `go test -timeout=...`.
	tierComponentTimeout = 180 * time.Second

	// tierIntegrationTimeout caps the integration tier. Matches
	// the prior inline value.
	tierIntegrationTimeout = 180 * time.Second

	// tierE2EKindTimeout caps the e2e_kind tier.
	tierE2EKindTimeout = 600 * time.Second

	// tierE2ECloudTimeout caps the e2e_cloud tier.
	tierE2ECloudTimeout = 1800 * time.Second

	// tierDocsTimeout caps the docs tier.
	tierDocsTimeout = 60 * time.Second

	// verdictRotationDepth bounds the number of rotated
	// verdict-<id>.json files on disk. The latest.json file is
	// always preserved.
	verdictRotationDepth = 20

	// commentDetailMaxChars truncates the per-tier "detail" cell
	// in renderComment so PR comments stay readable even when a
	// test emits a multi-kilobyte stack trace.
	commentDetailMaxChars = 120
)
