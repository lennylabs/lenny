// SPDX-License-Identifier: MIT

// Package capabilityinference implements the §5.1 capability inference
// from MCP ToolAnnotations. At connector or type:mcp runtime
// registration the gateway reads tools/list and infers each tool's
// capability set from its annotations, so a runtime author does not
// re-annotate tools that already carry MCP hints.
package capabilityinference

// Capability is a §5.3 tool capability. A pool must grant a capability
// for a tool that requires it to be callable.
type Capability string

const (
	CapRead    Capability = "read"
	CapWrite   Capability = "write"
	CapDelete  Capability = "delete"
	CapNetwork Capability = "network"
	CapExecute Capability = "execute"
	CapAdmin   Capability = "admin"
)

// capabilityOrder fixes the output order so an inferred set is stable.
var capabilityOrder = []Capability{
	CapRead, CapWrite, CapDelete, CapNetwork, CapExecute, CapAdmin,
}

// Mode is the §5.1 capabilityInferenceMode. It sets the default
// capability for a tool that carries no MCP annotations.
type Mode string

const (
	// ModeStrict infers an unannotated tool as admin and raises the
	// §5.1 warning signal. It is the default.
	ModeStrict Mode = "strict"

	// ModePermissive infers an unannotated tool as write and raises no
	// warning.
	ModePermissive Mode = "permissive"
)

// DefaultMode is the §5.1 default capabilityInferenceMode.
const DefaultMode = ModeStrict

// AllModes returns the closed enum.
func AllModes() []Mode { return []Mode{ModeStrict, ModePermissive} }

// IsValid reports whether m is a known inference mode.
func (m Mode) IsValid() bool {
	for _, v := range AllModes() {
		if m == v {
			return true
		}
	}
	return false
}

// ToolAnnotations is the subset of MCP tool annotations the §5.1
// inference table consults. A field is nil when the annotation is
// absent; a tool with every field nil is unannotated.
type ToolAnnotations struct {
	ReadOnlyHint    *bool
	DestructiveHint *bool
	OpenWorldHint   *bool
}

// Empty reports whether the tool carries none of the MCP annotations
// the §5.1 inference table consults.
func (a ToolAnnotations) Empty() bool {
	return a.ReadOnlyHint == nil && a.DestructiveHint == nil && a.OpenWorldHint == nil
}

// Result is the outcome of inferring one tool's capabilities.
type Result struct {
	// Capabilities is the inferred capability set, deduplicated and in
	// the fixed capabilityOrder.
	Capabilities []Capability

	// InferredAdminUnannotated reports that the tool carried no MCP
	// annotations and was inferred as admin under strict mode. §5.1
	// requires the gateway to emit a WARN log in this case.
	InferredAdminUnannotated bool
}

// boolEq reports whether the optional annotation p is present and equal
// to want.
func boolEq(p *bool, want bool) bool { return p != nil && *p == want }

// Infer applies the §5.1 capability-inference table to one tool's MCP
// annotations under the given mode. A read-only tool infers read; a
// tool that is not read-only infers write; a destructive tool infers
// write and delete; an open-world tool additionally infers network.
// When no annotation produces a capability — including the fully
// unannotated case — the mode default applies: strict infers admin,
// permissive infers write. The execute capability has no MCP equivalent
// and is never inferred; it is set through toolCapabilityOverrides.
func Infer(a ToolAnnotations, mode Mode) Result {
	if !mode.IsValid() {
		mode = DefaultMode
	}
	have := map[Capability]bool{}
	if boolEq(a.ReadOnlyHint, true) {
		have[CapRead] = true
	}
	if boolEq(a.ReadOnlyHint, false) {
		have[CapWrite] = true
	}
	if boolEq(a.DestructiveHint, true) {
		have[CapWrite] = true
		have[CapDelete] = true
	}
	if boolEq(a.OpenWorldHint, true) {
		have[CapNetwork] = true
	}

	var res Result
	if len(have) == 0 {
		// No annotation produced a capability. Apply the mode default;
		// the §5.1 warning fires only for a fully unannotated tool.
		if mode == ModePermissive {
			have[CapWrite] = true
		} else {
			have[CapAdmin] = true
			res.InferredAdminUnannotated = a.Empty()
		}
	}
	for _, c := range capabilityOrder {
		if have[c] {
			res.Capabilities = append(res.Capabilities, c)
		}
	}
	return res
}

// WarnMessage returns the §5.1 registration-time WARN log message for a
// tool whose capability was inferred as admin because it carries no MCP
// annotations. id identifies the connector or runtime the tool is on.
func WarnMessage(tool, id string) string {
	return "Tool '" + tool + "' on connector/runtime '" + id +
		"' has no MCP ToolAnnotations; capability inferred as 'admin' " +
		"(conservative default). Use toolCapabilityOverrides or add " +
		"ToolAnnotations to suppress this warning."
}
