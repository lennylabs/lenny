// SPDX-License-Identifier: MIT

package experiment

// ResolveExternalVariant implements the §10.7 OpenFeature
// external-targeting variant resolution. A `mode: external` experiment
// is assigned by an OpenFeature provider, which returns an
// EvaluationDetails carrying an optional Variant string and a Value.
// The candidate variant is taken from Variant when it is set,
// otherwise from Value when Value is a string, otherwise from
// Value["variant_id"] when Value is an object that carries that key.
//
// known is true only when the resolved candidate is ControlVariantID
// or one of the experiment's registered variant IDs. Otherwise the
// provider response is unresolvable: variantID is ControlVariantID and
// known is false, and the §10.7 caller emits an
// experiment.unknown_variant_from_provider event and runs the session
// on control.
func ResolveExternalVariant(variant string, value any, registered []string) (variantID string, known bool) {
	candidate := extractProviderVariant(variant, value)
	if candidate == "" {
		return ControlVariantID, false
	}
	if candidate == ControlVariantID {
		return ControlVariantID, true
	}
	for _, r := range registered {
		if candidate == r {
			return candidate, true
		}
	}
	return ControlVariantID, false
}

// extractProviderVariant applies the §10.7 Variant/Value extraction
// precedence to an OpenFeature evaluation result. It returns "" when
// the result carries no usable variant identifier.
func extractProviderVariant(variant string, value any) string {
	if variant != "" {
		return variant
	}
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		if id, ok := v["variant_id"].(string); ok {
			return id
		}
	}
	return ""
}
