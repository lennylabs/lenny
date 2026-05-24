// SPDX-License-Identifier: MIT

package interceptor

import (
	"encoding/json"
	"reflect"
	"strings"
)

// phaseImmutableFields returns the dot-separated JSON paths into a
// phase's content payload that an external interceptor MODIFY must not
// alter, per the §4.8 phase payload table (spec: §4.8 lines 1042-1060)
// and the "Immutable field enforcement on MODIFY" paragraph (spec: §4.8
// line 1060). A phase whose entire content payload is mutable (the
// delegation/message/agent-output/LLM phases) returns nil, which
// disables structural enforcement for that phase.
//
// PreExportMaterialization is deliberately absent: its file_size must
// equal the post-MODIFY byte length and its sha256 is recomputed rather
// than compared, semantics the generic comparator cannot express.
// scanExportFile/applyExportModify (export.go) own that phase's
// file_path/delegation_context immutability with the per-file size and
// digest rules, so the generic check must not also fire for it.
func phaseImmutableFields(phase Phase) []string {
	switch phase {
	case PhasePostAuth:
		// Serialized request envelope; authenticated identity sits in the
		// metadata object (spec: §4.8 line 1047 PostAuth row).
		return []string{"metadata.user_id", "metadata.tenant_id"}
	case PhasePreRoute:
		// Serialized TaskSpec; identity fields are top-level (spec: §4.8
		// line 1048 PreRoute row).
		return []string{"tenant_id", "user_id"}
	case PhasePostRoute:
		// Resolved runtime and credential assignment (spec: §4.8 line 1052
		// PostRoute row, line 1060 enforcement paragraph).
		return []string{"resolved_runtime_name", "credential_pool_id"}
	case PhasePreToolResult:
		// Tool-result correlation id (spec: §4.8 line 1053 PreToolResult row).
		return []string{"id"}
	case PhasePreConnectorRequest, PhasePostConnectorResponse:
		// Connector routing identity (spec: §4.8 lines 1057-1058).
		return []string{"tool_name", "connector_id"}
	default:
		return nil
	}
}

// checkModifyImmutability reports the immutable fields a MODIFY altered
// between the pre-modification payload and the interceptor's modified
// payload for phase. It returns a non-empty slice of violated field
// paths when the MODIFY changed, removed, or introduced an immutable
// field; the chain then rejects the MODIFY with
// CodeInterceptorImmutableFieldViolation and preserves the original
// payload (spec: §4.8 line 1060).
//
// Enforcement applies only when the phase declares immutable fields and
// the pre-modification payload is a JSON object. A phase whose content
// is a JSON array or scalar (TaskSpec.input at PreDelegation, the
// message body at PreMessageDelivery) declares no immutable fields and
// is never inspected here. When the pre-modification payload parses as
// an object but the modified payload does not, every immutable field is
// reported violated because the MODIFY destroyed the structure that
// carried them.
func checkModifyImmutability(phase Phase, pre, post []byte) []string {
	fields := phaseImmutableFields(phase)
	if len(fields) == 0 {
		return nil
	}
	var preObj map[string]any
	if err := json.Unmarshal(pre, &preObj); err != nil || preObj == nil {
		// The pre-modification payload is not a JSON object; the phase has
		// no object structure to snapshot, so there is nothing to enforce.
		return nil
	}
	var postObj map[string]any
	if err := json.Unmarshal(post, &postObj); err != nil || postObj == nil {
		return fields
	}
	var violations []string
	for _, path := range fields {
		preVal, preOK := lookupPath(preObj, path)
		postVal, postOK := lookupPath(postObj, path)
		if preOK != postOK || !reflect.DeepEqual(preVal, postVal) {
			violations = append(violations, path)
		}
	}
	return violations
}

// lookupPath walks obj following a dot-separated path and returns the
// value at the leaf. ok is false when any path segment is missing or
// traverses a non-object node.
func lookupPath(obj map[string]any, path string) (val any, ok bool) {
	segments := strings.Split(path, ".")
	var cur any = obj
	for _, seg := range segments {
		m, isMap := cur.(map[string]any)
		if !isMap {
			return nil, false
		}
		next, present := m[seg]
		if !present {
			return nil, false
		}
		cur = next
	}
	return cur, true
}
