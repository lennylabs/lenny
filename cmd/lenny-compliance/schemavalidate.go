// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/lennylabs/lenny/schemas"
)

// The §15.4.6 conformance categories at spec lines 2405 and 2408 require
// the harness to validate the runtime's emitted frames against the two
// authoritative wire schemas:
//
//   - line 2405: a `response` frame "matches schemas/lenny-adapter-jsonl.schema.json".
//   - line 2408: "Every MessagePart produced by the runtime validates
//     against schemas/messagepart.schema.json".
//
// The schemas are embedded (github.com/lennylabs/lenny/schemas) so this
// check runs against a third-party runtime in its own repository, where
// the schemas/ tree is absent. They are compiled once, lazily, and reused
// across every check in a battery.

const (
	jsonlSchemaID       = "https://schemas.lenny.dev/adapter-jsonl/v1.json"
	jsonlSchemaFile     = "lenny-adapter-jsonl.schema.json"
	messagePartSchemaID = "https://schemas.lenny.dev/messagepart/v1.json"
	messagePartFile     = "messagepart.schema.json"
)

var (
	schemaOnce        sync.Once
	jsonlSchema       *jsonschema.Schema
	messagePartSchema *jsonschema.Schema
	schemaErr         error
)

// loadSchemas compiles the JSONL and MessagePart schemas from the embedded
// FS. The MessagePart schema is registered under its $id first so the
// JSONL schema's `response.output` $ref resolves to it.
func loadSchemas() error {
	schemaOnce.Do(func() {
		c := jsonschema.NewCompiler()
		c.Draft = jsonschema.Draft2020
		for id, file := range map[string]string{
			messagePartSchemaID: messagePartFile,
			jsonlSchemaID:       jsonlSchemaFile,
		} {
			data, err := schemas.FS.ReadFile(file)
			if err != nil {
				schemaErr = fmt.Errorf("read embedded schema %s: %w", file, err)
				return
			}
			if err := c.AddResource(id, bytes.NewReader(data)); err != nil {
				schemaErr = fmt.Errorf("add schema resource %s: %w", id, err)
				return
			}
		}
		if jsonlSchema, schemaErr = c.Compile(jsonlSchemaID); schemaErr != nil {
			schemaErr = fmt.Errorf("compile %s: %w", jsonlSchemaFile, schemaErr)
			return
		}
		if messagePartSchema, schemaErr = c.Compile(messagePartSchemaID); schemaErr != nil {
			schemaErr = fmt.Errorf("compile %s: %w", messagePartFile, schemaErr)
			return
		}
	})
	return schemaErr
}

// validateJSONLFrame validates one raw §15.4.1 stdin/stdout frame against
// schemas/lenny-adapter-jsonl.schema.json. spec: §15.4.6 line 2405.
func validateJSONLFrame(raw []byte) error {
	if err := loadSchemas(); err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("frame not JSON: %w", err)
	}
	if err := jsonlSchema.Validate(v); err != nil {
		return fmt.Errorf("frame does not match %s: %w", jsonlSchemaFile, err)
	}
	return nil
}

// validateMessagePart validates one MessagePart against
// schemas/messagepart.schema.json. spec: §15.4.6 line 2408.
func validateMessagePart(raw json.RawMessage) error {
	if err := loadSchemas(); err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("output part not JSON: %w", err)
	}
	if err := messagePartSchema.Validate(v); err != nil {
		return fmt.Errorf("output part does not match %s: %w", messagePartFile, err)
	}
	return nil
}

// validateResponseFrame validates a `response` frame against the JSONL
// schema and, when it carries an `output` array, every MessagePart against
// the MessagePart schema. It is the combined §15.4.6 line 2405 + 2408
// assertion the response-producing checks run. spec: §15.4.6 lines 2405, 2408.
func validateResponseFrame(raw []byte) error {
	if err := validateJSONLFrame(raw); err != nil {
		return err
	}
	var resp struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("response not JSON: %w", err)
	}
	for i, part := range resp.Output {
		if err := validateMessagePart(part); err != nil {
			return fmt.Errorf("output[%d]: %w", i, err)
		}
	}
	return nil
}
