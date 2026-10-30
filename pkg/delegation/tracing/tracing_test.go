// SPDX-License-Identifier: MIT

package tracing_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/delegation/tracing"
)

// wantCode asserts err is a *ValidationError carrying code.
func wantCode(t *testing.T, err error, code tracing.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a %s error, got nil", code)
	}
	var ve *tracing.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error %v is not a *ValidationError", err)
	}
	if ve.Code != code {
		t.Errorf("error code = %q, want %q (%v)", ve.Code, code, ve)
	}
}

func TestValidateAcceptsCleanContext(t *testing.T) {
	clean := map[string]string{
		"langsmith_run_id": "run_abc123",
		"trace-id":         "0af7651916cd43dd8448eb211c80319c",
		"span_id":          "b7ad6b7169203331",
	}
	if err := tracing.Validate(clean); err != nil {
		t.Errorf("clean context rejected: %v", err)
	}
}

func TestValidateAcceptsNilAndEmpty(t *testing.T) {
	if err := tracing.Validate(nil); err != nil {
		t.Errorf("nil context rejected: %v", err)
	}
	if err := tracing.Validate(map[string]string{}); err != nil {
		t.Errorf("empty context rejected: %v", err)
	}
}

func TestValidateRejectsTooManyEntries(t *testing.T) {
	tc := map[string]string{}
	for i := 0; i < tracing.MaxEntries+1; i++ {
		tc[padKey(i, 8)] = "v"
	}
	wantCode(t, tracing.Validate(tc), tracing.CodeTooLarge)
}

func TestValidateRejectsLongKey(t *testing.T) {
	tc := map[string]string{strings.Repeat("a", tracing.MaxKeyBytes+1): "v"}
	wantCode(t, tracing.Validate(tc), tracing.CodeTooLarge)
}

func TestValidateRejectsLongValue(t *testing.T) {
	tc := map[string]string{"trace-id": strings.Repeat("x", tracing.MaxValueBytes+1)}
	wantCode(t, tracing.Validate(tc), tracing.CodeTooLarge)
}

func TestValidateRejectsSensitiveKey(t *testing.T) {
	// Each key carries a blocklisted substring, in mixed case to
	// exercise the case-insensitive match.
	for _, key := range []string{
		"my-Secret", "api_TOKEN", "Password1", "signing-KEY",
		"aws_Credential", "Authorization-hdr",
	} {
		err := tracing.Validate(map[string]string{key: "v"})
		wantCode(t, err, tracing.CodeSensitiveKey)
	}
}

func TestValidateRejectsURLValue(t *testing.T) {
	for _, val := range []string{
		"http://evil.example.com/collect",
		"https://attacker.test/traces",
	} {
		err := tracing.Validate(map[string]string{"trace-endpoint": val})
		wantCode(t, err, tracing.CodeURLNotAllowed)
	}
}

func TestValidateRejectsOversizedSerialized(t *testing.T) {
	// 32 entries, each key and value within the per-field limits, but
	// the serialized whole exceeds the 4 KB limit.
	tc := map[string]string{}
	for i := 0; i < tracing.MaxEntries; i++ {
		tc[padKey(i, 100)] = strings.Repeat("v", 200)
	}
	if len(tc) > tracing.MaxEntries {
		t.Fatalf("test built %d entries, want <= %d", len(tc), tracing.MaxEntries)
	}
	wantCode(t, tracing.Validate(tc), tracing.CodeTooLarge)
}

func TestMergeKeepsExistingOnCollision(t *testing.T) {
	existing := map[string]string{"trace-id": "parent", "span_id": "p1"}
	incoming := map[string]string{"trace-id": "child-override", "run_id": "c1"}
	got := tracing.Merge(existing, incoming)

	if got["trace-id"] != "parent" {
		t.Errorf("trace-id = %q, want parent — child must not overwrite", got["trace-id"])
	}
	if got["span_id"] != "p1" {
		t.Errorf("span_id = %q, want p1", got["span_id"])
	}
	if got["run_id"] != "c1" {
		t.Errorf("run_id = %q, want c1 — non-colliding child entry should be added", got["run_id"])
	}
	// The inputs are not mutated.
	if _, ok := existing["run_id"]; ok {
		t.Error("Merge mutated the existing map")
	}
}

func TestMergeNilInputs(t *testing.T) {
	if got := tracing.Merge(nil, nil); got != nil {
		t.Errorf("Merge(nil, nil) = %v, want nil", got)
	}
	got := tracing.Merge(nil, map[string]string{"run_id": "c1"})
	if got["run_id"] != "c1" {
		t.Errorf("Merge(nil, incoming) dropped the incoming entry: %v", got)
	}
}

// padKey returns a clean, unique key of the given byte length. The
// prefix avoids every §8.3 blocklisted substring.
func padKey(i, length int) string {
	base := "trace-id-"
	for len(base) < length {
		base += "x"
	}
	suffix := string(rune('a' + i%26))
	if i >= 26 {
		suffix += string(rune('a' + i/26))
	}
	return base[:length-len(suffix)] + suffix
}
