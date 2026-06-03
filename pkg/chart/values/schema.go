// SPDX-License-Identifier: MIT

package values

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

// SchemaID is the published, versioned URL the chart's values.schema.json
// advertises for IDE integration (the yaml-language-server $schema
// reference). spec: §17.6 line 660.
const SchemaID = "https://schemas.lenny.dev/helm/values/v1.json"

// MetaSchema is the JSON Schema dialect the generated document declares.
// spec: §17.6 line 653 (Draft 2020-12).
const MetaSchema = "https://json-schema.org/draft/2020-12/schema"

// Generate returns the canonical values.schema.json bytes for the chart,
// reflected from Root. Output is deterministic — json.Marshal sorts
// object keys, and constraint slices are built in tag order — so the
// build-time drift check (cmd/lenny-chart-schema-gen -check) is stable.
// spec: §17.6 line 655.
func Generate() ([]byte, error) {
	schema := schemaForStruct(reflect.TypeOf(Root{}))
	schema["$schema"] = MetaSchema
	schema["$id"] = SchemaID
	schema["title"] = "Lenny Helm chart values"
	schema["description"] = "Canonical JSON Schema (Draft 2020-12) for charts/lenny/values.yaml, generated from pkg/chart/values. spec: §17.6."
	out, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// schemaForStruct builds an object schema from a struct type. A struct is
// strict (additionalProperties:false) so unknown keys are rejected; the
// permissive Object map type is handled by schemaForType instead.
func schemaForStruct(t reflect.Type) map[string]any {
	props := map[string]any{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, ok := jsonFieldName(f)
		if !ok {
			continue
		}
		props[name] = schemaForType(f.Type, f.Tag)
	}
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
}

// schemaForType maps a Go type plus its field tags to a JSON Schema node.
func schemaForType(t reflect.Type, tag reflect.StructTag) map[string]any {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	var s map[string]any
	switch t.Kind() {
	case reflect.String:
		s = map[string]any{"type": "string"}
		applyStringConstraints(s, tag)
	case reflect.Bool:
		s = map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s = map[string]any{"type": "integer"}
		applyNumberConstraints(s, tag)
	case reflect.Float32, reflect.Float64:
		s = map[string]any{"type": "number"}
		applyNumberConstraints(s, tag)
	case reflect.Slice:
		s = map[string]any{"type": "array", "items": schemaForType(t.Elem(), "")}
	case reflect.Map:
		// A map type is a permissive object. When the value type is
		// concrete (e.g. map[string]string) constrain additionalProperties
		// to it; map[string]any (the Object type) stays fully open.
		s = map[string]any{"type": "object"}
		if t.Elem().Kind() != reflect.Interface {
			s["additionalProperties"] = schemaForType(t.Elem(), "")
		}
	case reflect.Struct:
		s = schemaForStruct(t)
	default:
		s = map[string]any{}
	}
	if d := tag.Get("desc"); d != "" {
		s["description"] = d
	}
	return s
}

// jsonFieldName returns the schema property name for a struct field from
// its json tag, and false for json:"-" (skipped) fields.
func jsonFieldName(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		name = f.Name
	}
	return name, true
}

// applyStringConstraints copies the enum / pattern / maxLength /
// minLength tags onto a string schema node.
func applyStringConstraints(s map[string]any, tag reflect.StructTag) {
	if e := tag.Get("enum"); e != "" {
		parts := strings.Split(e, "|")
		arr := make([]any, len(parts))
		for i, p := range parts {
			arr[i] = p
		}
		s["enum"] = arr
	}
	if p := tag.Get("pattern"); p != "" {
		s["pattern"] = p
	}
	if v := tag.Get("maxLength"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			s["maxLength"] = n
		}
	}
	if v := tag.Get("minLength"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			s["minLength"] = n
		}
	}
}

// applyNumberConstraints copies the min / max tags onto a numeric schema
// node as the JSON Schema minimum / maximum keywords.
func applyNumberConstraints(s map[string]any, tag reflect.StructTag) {
	if v := tag.Get("min"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			s["minimum"] = json.Number(strconv.FormatFloat(n, 'f', -1, 64))
		}
	}
	if v := tag.Get("max"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			s["maximum"] = json.Number(strconv.FormatFloat(n, 'f', -1, 64))
		}
	}
}
