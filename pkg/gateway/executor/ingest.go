// SPDX-License-Identifier: MIT

package executor

import (
	"github.com/lennylabs/lenny/pkg/degradation"
	"github.com/lennylabs/lenny/pkg/outputtype"
)

// ingestResponse converts a runtime `response` frame into the gateway's
// OutputParts and the §15.4.1 envelope-level degradation annotations a
// live consumer must surface. The gateway is the live consumer at this
// boundary: it forward-reads the runtime's parts, so it MUST signal when
// a part carries a schemaVersion ahead of what it understands and it
// applies the canonical type-registry fallback per part.
//
// Returned envelope annotations carry `schema_version_ahead` when any
// part is stamped above outputtype.MaxKnownSchemaVersion (with the
// highest encountered version, since one envelope annotation covers the
// frame). Part-scoped `unregistered_platform_type` warnings ride on each
// part's own Annotations.
//
// spec: §15.4.1 lines 1499-1522.
func ingestResponse(env responseEnvelope) ([]OutputPart, map[string]any) {
	parts := make([]OutputPart, 0, len(env.Output))
	if env.Text != "" && len(env.Output) == 0 {
		// Basic-level `{type:"response", text:"..."}` shorthand: a single
		// v1 text part.
		parts = append(parts, OutputPart{
			Type:          "text",
			Text:          env.Text,
			SchemaVersion: outputtype.MaxKnownSchemaVersion,
		})
		return parts, nil
	}

	var envAnn map[string]any
	maxAhead := 0
	for _, p := range env.Output {
		op := ingestPart(p)
		if op.SchemaVersion > outputtype.MaxKnownSchemaVersion && op.SchemaVersion > maxAhead {
			maxAhead = op.SchemaVersion
		}
		parts = append(parts, op)
	}
	if maxAhead > 0 {
		envAnn = degradation.Stamp(envAnn, degradation.AnnotationSchemaVersionAhead,
			degradation.SchemaVersionAhead(outputtype.MaxKnownSchemaVersion, maxAhead))
	}
	return parts, envAnn
}

// mergeAnnotations folds src into dst, allocating dst on first use. It
// merges the envelope-level degradation annotations a multi-message Send
// accumulates across its per-message `response` frames.
func mergeAnnotations(dst, src map[string]any) map[string]any {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]any, len(src))
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// ingestPart projects one wire OutputPart onto the gateway model and
// applies the §15.4.1 canonical type-registry rule. A non-canonical type
// (vendor-namespaced or unregistered) collapses to `text` with the
// original preserved in `annotations.originalType`; an unregistered
// unprefixed type additionally earns the `unregistered_platform_type`
// warning so a gateway that pre-dates a newly registered type still
// passes it through visibly. A missing schemaVersion defaults to 1.
//
// spec: §15.4.1 lines 1503, 1522.
func ingestPart(p wireOutputPart) OutputPart {
	sv := p.SchemaVersion
	if sv <= 0 {
		sv = 1
	}
	op := OutputPart{
		Type:          p.Type,
		Ref:           p.Ref,
		SchemaVersion: sv,
		Annotations:   p.Annotations,
	}
	if p.Type == "text" {
		op.Text = p.Inline
	}
	if p.Type != "" && !outputtype.IsCanonical(p.Type) {
		// Custom-type fallback: collapse to text, preserve the original
		// type, and carry the inline payload as the text body.
		if op.Annotations == nil {
			op.Annotations = map[string]any{}
		}
		op.Annotations["originalType"] = p.Type
		if outputtype.Unregistered(p.Type) {
			op.Annotations = degradation.Stamp(op.Annotations,
				degradation.AnnotationUnregisteredPlatformType,
				degradation.UnregisteredPlatformType(p.Type))
		}
		op.Type = "text"
		if op.Text == "" {
			op.Text = p.Inline
		}
	}
	return op
}
