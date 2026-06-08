// SPDX-License-Identifier: MIT

// Package schemas embeds the machine-readable wire contracts the spec
// designates as authoritative (§15.4.1 lines 1425-1426) so a portable
// binary can validate frames without a checkout of the source tree. The
// §15.4.6 conformance harness (cmd/lenny-compliance) and
// `lenny runtime validate` run against third-party runtimes in their own
// repositories, where the schemas/ directory is absent; embedding keeps
// the published artifacts the single source of truth while making them
// reachable from those binaries.
package schemas

import "embed"

// FS holds the published wire-contract artifacts §24.8 line 113 names as
// the source the conformance suite is schema-driven against: the JSON
// Schemas (lenny-adapter-jsonl.schema.json — every adapter↔binary
// stdin/stdout message; outputpart.schema.json — the OutputPart envelope;
// lifecycle-events.schema.json — the lifecycle channel; workspaceplan-v1.json)
// and lenny-adapter.proto (the adapter gRPC contract, whose Error.ErrorCode
// enum is the closed §15.1 error-code catalog the JSONL schema leaves as an
// open string). The on-disk artifacts remain the source of truth; this only
// exposes them as an embed.FS.
//
//go:embed *.json lenny-adapter.proto
var FS embed.FS
