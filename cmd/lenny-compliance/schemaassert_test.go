// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §24.8 line 113 — the suite is schema-driven; the error-code catalog
// the response assertion checks against is generated from the published
// schemas/lenny-adapter.proto Error.ErrorCode enum, not hand-transcribed.
func TestLoadProtoErrorCatalog_spec_24_8(t *testing.T) {
	catalog, err := loadProtoErrorCatalog()
	if err != nil {
		t.Fatalf("loadProtoErrorCatalog: %v", err)
	}
	if len(catalog) == 0 {
		t.Fatal("catalog is empty; the embedded proto enum was not parsed")
	}
	for _, code := range []string{"SESSION_NOT_FOUND", "RATE_LIMITED", "PLATFORM_DEGRADED", "TOKEN_BUDGET_EXHAUSTED"} {
		if !catalog[code] {
			t.Errorf("catalog is missing the §15.1 code %q", code)
		}
	}
	// The proto's mandatory zero sentinel is not a real code and must be
	// excluded so a runtime cannot pass by emitting it.
	if catalog["UNSPECIFIED"] {
		t.Error("catalog must exclude ERROR_CODE_UNSPECIFIED")
	}
}

// spec: §24.8 line 113 — the published .proto artifact is the single source
// of truth. This drift guard fails if the text parsed from the embedded
// artifact diverges from the compiler-generated descriptor, so a future
// proto edit cannot silently desynchronize the two.
func TestProtoErrorCatalogMatchesDescriptor_spec_24_8(t *testing.T) {
	catalog, err := loadProtoErrorCatalog()
	if err != nil {
		t.Fatalf("loadProtoErrorCatalog: %v", err)
	}
	want := map[string]bool{}
	for name := range adapterv1.Error_ErrorCode_value {
		bare := strings.TrimPrefix(name, errorCodeEnumPrefix)
		if bare == "UNSPECIFIED" {
			continue
		}
		want[bare] = true
	}
	for code := range want {
		if !catalog[code] {
			t.Errorf("parsed catalog is missing %q present in the compiled descriptor", code)
		}
	}
	for code := range catalog {
		if !want[code] {
			t.Errorf("parsed catalog has %q absent from the compiled descriptor", code)
		}
	}
}

// spec: §24.8 line 113 — a response carrying an out-of-catalog error code is
// non-conformant, and the validation report cites the specific schema
// assertion that failed.
func TestValidateResponseErrorCode_spec_24_8(t *testing.T) {
	cases := []struct {
		name      string
		frame     string
		ok        bool
		wantCited string // substring the failure detail must cite
	}{
		{"in-catalog code", `{"type":"response","error":{"code":"RATE_LIMITED","message":"slow down"}}`, true, ""},
		{"no error frame", `{"type":"response","output":[{"schemaVersion":1,"type":"text","inline":"hi"}]}`, true, ""},
		{"text shorthand", `{"type":"response","text":"hi"}`, true, ""},
		{"empty error code", `{"type":"response","error":{"message":"x"}}`, true, ""},
		{"out-of-catalog code", `{"type":"response","error":{"code":"NOT_A_REAL_CODE","message":"x"}}`, false, errorCodeAssertionID},
		{"lowercase miss", `{"type":"response","error":{"code":"rate_limited","message":"x"}}`, false, adapterProtoFile},
		{"not json", `{`, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateResponseErrorCode([]byte(tc.frame))
			if tc.ok && err != nil {
				t.Fatalf("validateResponseErrorCode(%s) = %v, want nil", tc.frame, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("validateResponseErrorCode(%s) = nil, want an assertion error", tc.frame)
			}
			if tc.wantCited != "" && !strings.Contains(err.Error(), tc.wantCited) {
				t.Errorf("failure detail %q does not cite %q", err.Error(), tc.wantCited)
			}
		})
	}
}
