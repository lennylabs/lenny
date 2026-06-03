// SPDX-License-Identifier: MIT

package values

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"sigs.k8s.io/yaml"
)

// ValidateYAML validates a Helm values document (YAML) against schemaJSON
// (a values.schema.json). The returned error describes every violation
// when the document does not conform; it is nil on success. The document
// may be a full values.yaml or a fragment (every top-level key is
// optional in the schema). spec: §17.6 line 666 (lenny-ctl values
// validate), §17.9.2 line 1374 (answer-file CI lint).
func ValidateYAML(schemaJSON, valuesYAML []byte) error {
	jsonDoc, err := yaml.YAMLToJSON(valuesYAML)
	if err != nil {
		return fmt.Errorf("parse values YAML: %w", err)
	}
	var doc any
	if err := json.Unmarshal(jsonDoc, &doc); err != nil {
		return fmt.Errorf("decode values: %w", err)
	}
	sch, err := compile(schemaJSON)
	if err != nil {
		return err
	}
	return sch.Validate(doc)
}

// compile parses schemaJSON into a reusable validator. The compiler is
// configured for the schema's declared draft (2020-12) via auto-detection
// from the $schema keyword.
func compile(schemaJSON []byte) (*jsonschema.Schema, error) {
	const url = "values.schema.json"
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, bytes.NewReader(schemaJSON)); err != nil {
		return nil, fmt.Errorf("load schema: %w", err)
	}
	sch, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return sch, nil
}
