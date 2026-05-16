// SPDX-License-Identifier: MIT

// Package tracing validates and merges the §8.3 delegation
// tracingContext: the opaque map<string,string> of tracing
// identifiers a runtime registers via lenny/set_tracing_context for
// propagation through the delegation tree. The rules are pure so they
// are unit-testable without the MCP transport or the session store.
package tracing

import (
	"encoding/json"
	"fmt"
	"strings"
)

// §8.3 gateway-enforced tracingContext limits.
const (
	// MaxSerializedBytes caps the JSON-encoded size of the context.
	MaxSerializedBytes = 4 << 10
	// MaxKeyBytes caps the byte length of a single key.
	MaxKeyBytes = 128
	// MaxValueBytes caps the byte length of a single value.
	MaxValueBytes = 256
	// MaxEntries caps the number of key-value pairs.
	MaxEntries = 32
)

// sensitiveKeyPatterns are the case-insensitive substrings a
// tracingContext key must not contain (§8.3 key-name blocklist). A
// match means the key could carry a credential rather than an opaque
// identifier.
var sensitiveKeyPatterns = []string{
	"secret", "token", "password", "key", "credential", "authorization",
}

// ErrorCode is the §8.3 / §15.1 error code a validation failure maps
// to.
type ErrorCode string

const (
	// CodeTooLarge is returned when the context exceeds a size limit.
	CodeTooLarge ErrorCode = "TRACING_CONTEXT_TOO_LARGE"
	// CodeSensitiveKey is returned when a key matches the blocklist.
	CodeSensitiveKey ErrorCode = "TRACING_CONTEXT_SENSITIVE_KEY"
	// CodeURLNotAllowed is returned when a value is an http/https URL.
	CodeURLNotAllowed ErrorCode = "TRACING_CONTEXT_URL_NOT_ALLOWED"
)

// ValidationError reports a §8.3 tracingContext rule violation. Code
// is the stable error code; Detail is a human-readable explanation.
type ValidationError struct {
	Code   ErrorCode
	Detail string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("tracing: %s: %s", e.Code, e.Detail)
}

// Validate applies the §8.3 gateway-enforced tracingContext rules. A
// nil or empty map is valid.
func Validate(tc map[string]string) error {
	if len(tc) > MaxEntries {
		return &ValidationError{
			Code:   CodeTooLarge,
			Detail: fmt.Sprintf("%d entries exceeds the %d-entry limit", len(tc), MaxEntries),
		}
	}
	for k, v := range tc {
		if len(k) > MaxKeyBytes {
			return &ValidationError{
				Code:   CodeTooLarge,
				Detail: fmt.Sprintf("key %q is %d bytes, over the %d-byte limit", k, len(k), MaxKeyBytes),
			}
		}
		if len(v) > MaxValueBytes {
			return &ValidationError{
				Code:   CodeTooLarge,
				Detail: fmt.Sprintf("value for key %q is %d bytes, over the %d-byte limit", k, len(v), MaxValueBytes),
			}
		}
		if pat := sensitiveKeyPattern(k); pat != "" {
			return &ValidationError{
				Code:   CodeSensitiveKey,
				Detail: fmt.Sprintf("key %q matches the blocked pattern %q", k, pat),
			}
		}
		if isURL(v) {
			return &ValidationError{
				Code:   CodeURLNotAllowed,
				Detail: fmt.Sprintf("value for key %q is a URL; only identifiers may be propagated", k),
			}
		}
	}
	if n := serializedSize(tc); n > MaxSerializedBytes {
		return &ValidationError{
			Code:   CodeTooLarge,
			Detail: fmt.Sprintf("serialized size %d bytes exceeds the %d-byte limit", n, MaxSerializedBytes),
		}
	}
	return nil
}

// Merge returns existing extended with the entries of incoming whose
// keys are not already present. Per §8.3 a child runtime's entries
// merge with the inherited parent entries but cannot overwrite or
// remove them, so on a key collision the existing value is kept. The
// result is a fresh map; neither argument is mutated. The result is
// nil when both inputs are empty.
func Merge(existing, incoming map[string]string) map[string]string {
	if len(existing) == 0 && len(incoming) == 0 {
		return nil
	}
	out := make(map[string]string, len(existing)+len(incoming))
	for k, v := range existing {
		out[k] = v
	}
	for k, v := range incoming {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	return out
}

// sensitiveKeyPattern returns the blocked pattern key matches, or ""
// when key is clean.
func sensitiveKeyPattern(key string) string {
	lower := strings.ToLower(key)
	for _, p := range sensitiveKeyPatterns {
		if strings.Contains(lower, p) {
			return p
		}
	}
	return ""
}

// isURL reports whether v is an http or https URL. §8.3 forbids URL
// values so a parent cannot redirect a child's tracing to an
// attacker-controlled endpoint.
func isURL(v string) bool {
	return strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://")
}

// serializedSize returns the byte length of the JSON encoding of tc.
func serializedSize(tc map[string]string) int {
	b, err := json.Marshal(tc)
	if err != nil {
		return 0
	}
	return len(b)
}
