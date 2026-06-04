// SPDX-License-Identifier: MIT

// Package schemas embeds the machine-readable JSON Schemas the spec
// designates as authoritative wire contracts (§15.4.1 lines 1425-1426)
// so a portable binary can validate frames without a checkout of the
// source tree. The §15.4.6 conformance harness (cmd/lenny-compliance) and
// `lenny runtime validate` run against third-party runtimes in their own
// repositories, where the schemas/ directory is absent; embedding keeps
// the .json files the single source of truth while making them reachable
// from those binaries.
package schemas

import "embed"

// FS holds the top-level JSON Schema files: lenny-adapter-jsonl.schema.json
// (every adapter↔binary stdin/stdout message), outputpart.schema.json (the
// OutputPart envelope), lifecycle-events.schema.json (the lifecycle
// channel), and workspaceplan-v1.json. The .json files remain the source
// of truth; this only exposes them as an embed.FS.
//
//go:embed *.json
var FS embed.FS
