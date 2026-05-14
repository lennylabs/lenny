// SPDX-License-Identifier: MIT

// Package assertions is the project-local replacement for testify /
// gomega per §17.7. Each helper is one short function that uses
// t.Helper + t.Fatalf or t.Errorf and nothing else.
//
// Helpers:
//
//   - Equal[T] / NotEqual[T] — typed equality
//   - StringContains / StringHasPrefix
//   - ErrorIs — errors.Is
//   - ErrorContains — substring check on the error's Error()
//   - JSONEqual — semantic JSON comparison (ignoring field order)
//   - True / False
//
// The package does not import testify or gomega; the project keeps
// the assertion surface narrow on purpose.
package assertions

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Equal fails the test when want != got. Uses reflect.DeepEqual to
// support maps, slices, and pointer values.
func Equal[T any](t testing.TB, want, got T, msg ...any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		fail(t, fmt.Sprintf("Equal: want %v, got %v", want, got), msg...)
	}
}

// NotEqual fails when forbidden == got.
func NotEqual[T any](t testing.TB, forbidden, got T, msg ...any) {
	t.Helper()
	if reflect.DeepEqual(forbidden, got) {
		fail(t, fmt.Sprintf("NotEqual: both equal %v", got), msg...)
	}
}

// True fails when got is not true.
func True(t testing.TB, got bool, msg ...any) {
	t.Helper()
	if !got {
		fail(t, "expected true", msg...)
	}
}

// False fails when got is not false.
func False(t testing.TB, got bool, msg ...any) {
	t.Helper()
	if got {
		fail(t, "expected false", msg...)
	}
}

// StringContains asserts substring presence.
func StringContains(t testing.TB, s, substr string, msg ...any) {
	t.Helper()
	if !strings.Contains(s, substr) {
		fail(t, fmt.Sprintf("StringContains: %q not in %q", substr, s), msg...)
	}
}

// StringHasPrefix asserts prefix.
func StringHasPrefix(t testing.TB, s, prefix string, msg ...any) {
	t.Helper()
	if !strings.HasPrefix(s, prefix) {
		fail(t, fmt.Sprintf("StringHasPrefix: %q is not a prefix of %q", prefix, s), msg...)
	}
}

// ErrorIs asserts errors.Is(got, target).
func ErrorIs(t testing.TB, got, target error, msg ...any) {
	t.Helper()
	if !errors.Is(got, target) {
		fail(t, fmt.Sprintf("ErrorIs: got %v, want %v", got, target), msg...)
	}
}

// ErrorContains asserts substring on err.Error().
func ErrorContains(t testing.TB, got error, substr string, msg ...any) {
	t.Helper()
	if got == nil {
		fail(t, fmt.Sprintf("ErrorContains: got nil, want substring %q", substr), msg...)
		return
	}
	if !strings.Contains(got.Error(), substr) {
		fail(t, fmt.Sprintf("ErrorContains: %q not in %q", substr, got.Error()), msg...)
	}
}

// JSONEqual unmarshals both sides into any and DeepEquals them. The
// inputs may be []byte or string. Use this when the wire format does
// not pin field order.
func JSONEqual(t testing.TB, want, got any, msg ...any) {
	t.Helper()
	w := normalize(t, want)
	g := normalize(t, got)
	if !reflect.DeepEqual(w, g) {
		fail(t, fmt.Sprintf("JSONEqual: differ\n  want: %v\n   got: %v", w, g), msg...)
	}
}

// normalize unmarshals []byte or string inputs into any.
func normalize(t testing.TB, v any) any {
	t.Helper()
	switch payload := v.(type) {
	case []byte:
		var out any
		if err := json.Unmarshal(payload, &out); err != nil {
			t.Fatalf("normalize: unmarshal: %v", err)
		}
		return out
	case string:
		var out any
		if err := json.Unmarshal([]byte(payload), &out); err != nil {
			t.Fatalf("normalize: unmarshal: %v", err)
		}
		return out
	default:
		// Round-trip through JSON so map/slice/pointer types
		// match the inputs from []byte and string forms.
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("normalize: marshal: %v", err)
		}
		var out any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("normalize: re-unmarshal: %v", err)
		}
		return out
	}
}

func fail(t testing.TB, base string, msg ...any) {
	t.Helper()
	if len(msg) == 0 {
		t.Errorf("%s", base)
		return
	}
	t.Errorf("%s: %v", base, msg)
}
