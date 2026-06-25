// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/lennylabs/lenny/schemas"
)

// §24.8 line 113 requires the conformance suite to be schema-driven:
// "assertions are generated from the published schemas/lenny-adapter.proto,
// schemas/lenny-adapter-jsonl.schema.json, and schemas/messagepart.schema.json
// artifacts ... rather than hand-coded against prose" and the validation
// report "cites the specific schema assertion that failed".
//
// The two JSON Schemas are consumed in schemavalidate.go. This file covers
// the third artifact, schemas/lenny-adapter.proto. The §15.4.1 JSONL schema
// deliberately models a response's `error.code` as an open string
// (`{"type":"string"}`), because the closed catalog lives on the gRPC side:
// the proto `Error.ErrorCode` enum is the authoritative §15.1 error-code set
// ("The catalog below mirrors spec §15.1"). A runtime that emits an
// error code outside that catalog is non-conformant, but no JSON-Schema
// assertion can catch it. The assertion here is generated directly from the
// published proto artifact so the catalog is never hand-transcribed.

const (
	// adapterProtoFile is the published artifact the error-code catalog
	// assertion is generated from. spec: §24.8 line 113.
	adapterProtoFile = "lenny-adapter.proto"

	// errorCodeAssertionID is the stable identifier the validation report
	// cites when a runtime emits an out-of-catalog error code. spec: §24.8
	// line 113 ("validation report cites the specific schema assertion").
	errorCodeAssertionID = "response_error_code_in_proto_catalog"

	// errorCodeEnumPrefix is the proto enum value prefix the §15.1 error
	// code is the suffix of: ERROR_CODE_RATE_LIMITED → RATE_LIMITED.
	errorCodeEnumPrefix = "ERROR_CODE_"
)

var (
	errorCatalogOnce sync.Once
	errorCatalog     map[string]bool
	errorCatalogErr  error

	// errorCodeEnumBlock isolates the `enum ErrorCode { ... }` body inside
	// the proto so a sibling enum (Category) is never scanned. (?s) lets `.`
	// span newlines.
	errorCodeEnumBlock = regexp.MustCompile(`(?s)enum\s+ErrorCode\s*\{(.*?)\}`)
	// errorCodeMember matches one `ERROR_CODE_NAME = N;` member line.
	errorCodeMember = regexp.MustCompile(`(ERROR_CODE_[A-Z0-9_]+)\s*=\s*\d+\s*;`)
)

// loadProtoErrorCatalog parses the closed §15.1 error-code catalog from the
// embedded schemas/lenny-adapter.proto Error.ErrorCode enum. It strips the
// ERROR_CODE_ prefix to recover the bare §15.1 code names a runtime emits in
// a response `error.code` field, and drops the zero-value ERROR_CODE_UNSPECIFIED
// (proto's mandatory unset sentinel, never a real code). Compiled once.
//
// spec: §24.8 line 113 (schema-driven assertion generation); §15.1 (error
// code catalog).
func loadProtoErrorCatalog() (map[string]bool, error) {
	errorCatalogOnce.Do(func() {
		data, err := schemas.FS.ReadFile(adapterProtoFile)
		if err != nil {
			errorCatalogErr = fmt.Errorf("read embedded %s: %w", adapterProtoFile, err)
			return
		}
		block := errorCodeEnumBlock.FindSubmatch(data)
		if block == nil {
			errorCatalogErr = fmt.Errorf("%s: no Error.ErrorCode enum found", adapterProtoFile)
			return
		}
		catalog := map[string]bool{}
		for _, m := range errorCodeMember.FindAllSubmatch(block[1], -1) {
			name := strings.TrimPrefix(string(m[1]), errorCodeEnumPrefix)
			if name == "UNSPECIFIED" {
				continue
			}
			catalog[name] = true
		}
		if len(catalog) == 0 {
			errorCatalogErr = fmt.Errorf("%s: Error.ErrorCode enum is empty", adapterProtoFile)
			return
		}
		errorCatalog = catalog
	})
	return errorCatalog, errorCatalogErr
}

// validateResponseErrorCode runs the proto-generated error-code assertion
// against a `response` frame. When the response carries an `error.code` the
// code MUST be a member of the published lenny-adapter.proto Error.ErrorCode
// catalog. A successful response with no `error` carries nothing to assert
// and passes. The returned error cites the specific schema assertion that
// failed so it reaches the validation report verbatim.
//
// spec: §24.8 line 113; §15.1 error-code catalog.
func validateResponseErrorCode(raw []byte) error {
	catalog, err := loadProtoErrorCatalog()
	if err != nil {
		return err
	}
	var frame struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		return fmt.Errorf("response not JSON: %w", err)
	}
	if frame.Error == nil || frame.Error.Code == "" {
		return nil
	}
	if !catalog[frame.Error.Code] {
		return fmt.Errorf("assertion %q (%s Error.ErrorCode): error code %q is not in the published §15.1 catalog %s",
			errorCodeAssertionID, adapterProtoFile, frame.Error.Code, sortedCatalog(catalog))
	}
	return nil
}

// sortedCatalog renders the catalog as a stable, comma-separated list for the
// failure detail so the report names the codes the runtime could have used.
func sortedCatalog(catalog map[string]bool) string {
	codes := make([]string, 0, len(catalog))
	for c := range catalog {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	return "[" + strings.Join(codes, ", ") + "]"
}
