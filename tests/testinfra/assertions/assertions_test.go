// SPDX-License-Identifier: MIT

package assertions_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/assertions"
)

// spec: 17.7 (Equal succeeds on identical values)
// diagnosis: Equal failed on values that should compare equal. The
//
//	reflect.DeepEqual call may have been bypassed.
func TestEqualSucceeds(t *testing.T) {
	t.Parallel()
	assertions.Equal(t, 1, 1)
	assertions.Equal(t, "x", "x")
	assertions.Equal(t, []int{1, 2}, []int{1, 2})
	assertions.Equal(t, map[string]int{"a": 1}, map[string]int{"a": 1})
}

// spec: 17.7 (Equal fails on different values; surfaced via sub-test)
// diagnosis: Equal silently accepted unequal values. The
//
//	DeepEqual branch is wrong.
func TestEqualFailsOnDiff(t *testing.T) {
	t.Parallel()
	tt := &captureT{TB: t}
	assertions.Equal(tt, 1, 2)
	if !tt.failed {
		t.Errorf("Equal should have failed on 1 vs 2")
	}
}

// spec: 17.7 (ErrorIs succeeds on the same sentinel)
// diagnosis: ErrorIs did not match a wrapped sentinel. The
//
//	errors.Is call is wrong.
func TestErrorIsMatches(t *testing.T) {
	t.Parallel()
	target := errors.New("boom")
	wrapped := wrap(target)
	assertions.ErrorIs(t, wrapped, target)
}

// spec: 17.7 (StringContains and StringHasPrefix work)
// diagnosis: One of the string helpers returned an incorrect verdict.
func TestStringHelpers(t *testing.T) {
	t.Parallel()
	assertions.StringContains(t, "hello world", "world")
	assertions.StringHasPrefix(t, "abc/def", "abc/")
}

// spec: 17.7 (JSONEqual ignores field order)
// diagnosis: JSONEqual reported a difference for two equivalent JSON
//
//	documents whose only difference is field order. The
//	round-trip through any was wrong.
func TestJSONEqualIgnoresOrder(t *testing.T) {
	t.Parallel()
	assertions.JSONEqual(t,
		`{"a":1,"b":2}`,
		`{"b":2,"a":1}`)
}

// spec: 17.7 (JSONEqual detects real differences)
// diagnosis: JSONEqual incorrectly accepted documents with different
//
//	values for the same key. The reflect.DeepEqual call after
//	normalize is wrong.
func TestJSONEqualDetectsRealDiff(t *testing.T) {
	t.Parallel()
	tt := &captureT{TB: t}
	assertions.JSONEqual(tt,
		`{"a":1,"b":2}`,
		`{"a":1,"b":3}`)
	if !tt.failed {
		t.Errorf("JSONEqual should have failed on differing values")
	}
}

// captureT is a testing.TB that records whether Errorf was called.
// Used to assert that the helpers fail when they should without
// failing the parent test.
type captureT struct {
	testing.TB
	failed bool
}

func (c *captureT) Errorf(format string, args ...any) {
	c.failed = true
}

func (c *captureT) Fatalf(format string, args ...any) {
	c.failed = true
}

func wrap(err error) error {
	return &wrapped{inner: err}
}

type wrapped struct{ inner error }

func (w *wrapped) Error() string { return "wrapped: " + w.inner.Error() }
func (w *wrapped) Unwrap() error { return w.inner }
