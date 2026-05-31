// SPDX-License-Identifier: MIT

package workspace

import (
	"errors"
	"fmt"
)

// MaxKnownSchemaVersion is the highest §14.1 WorkspacePlan schemaVersion
// this adapter understands. v1 ships with 1 as the only valid value. The
// constant tracks the gateway-side wire identifier
// (pkg/workspaceplan.SchemaVersion); a drift guard in the package test
// asserts the two agree so a future bump cannot land on only one side.
//
// spec: §14.1 line 320 — `schemaVersion` is the wire-compat identifier;
// the only currently-valid value is 1.
const MaxKnownSchemaVersion = 1

// ErrSchemaVersionUnsupported reports a WorkspacePlan whose schemaVersion
// is higher than this adapter understands. spec: §14.1 line 326.
var ErrSchemaVersionUnsupported = errors.New("workspace plan schemaVersion is unsupported")

// CheckSchemaVersion enforces the §14.1 line 326 live-consumer rule at
// the adapter's materialization boundary: a plan whose schemaVersion
// exceeds MaxKnownSchemaVersion MUST be rejected before any filesystem
// write, because a stale adapter could misinterpret fields a newer
// gateway wrote. The returned error wraps ErrSchemaVersionUnsupported and
// names both versions so the gateway can surface
// `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` with the version pair.
//
// schemaVersion 0 (the proto3 default for an unset field) is treated as
// "legacy / unstamped" and allowed through: the gateway stamps a positive
// value on every plan it emits (pkg/gateway/podsession.WorkspacePlanToProto),
// so a 0 reaching the adapter means an empty or pre-versioning plan, not a
// future one. Only schemaVersion > known is a forward-incompatibility.
//
// spec: §14.1 line 326. F-14.1.3.
func CheckSchemaVersion(schemaVersion int) error {
	if schemaVersion > MaxKnownSchemaVersion {
		return fmt.Errorf("%w: plan declares schemaVersion %d, adapter knows %d",
			ErrSchemaVersionUnsupported, schemaVersion, MaxKnownSchemaVersion)
	}
	return nil
}
