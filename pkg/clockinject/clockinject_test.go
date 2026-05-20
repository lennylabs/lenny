// SPDX-License-Identifier: MIT

package clockinject

import (
	"errors"
	"testing"
	"time"
)

// spec: §12.8 (the package defaults to a pure time.Now passthrough)
func TestNowDefaultsToTimeNow(t *testing.T) {
	t.Cleanup(ResetForTest)
	off, src := Offset()
	if off != 0 || src != "" {
		t.Errorf("default Offset = (%v, %q), want (0, \"\")", off, src)
	}
	before := time.Now().Add(-50 * time.Millisecond)
	got := Now()
	after := time.Now().Add(50 * time.Millisecond)
	if got.Before(before) || got.After(after) {
		t.Errorf("Now = %v, want within +-50ms of time.Now", got)
	}
}

// spec: §12.8 (SetOffsetForTest applies a manual skew)
func TestSetOffsetForTestSkewsNow(t *testing.T) {
	t.Cleanup(ResetForTest)
	SetOffsetForTest(2 * time.Hour)
	off, src := Offset()
	if off != 2*time.Hour || src != "test" {
		t.Errorf("Offset = (%v, %q), want (2h, test)", off, src)
	}
	delta := Now().Sub(time.Now())
	if delta < 2*time.Hour-time.Second || delta > 2*time.Hour+time.Second {
		t.Errorf("Now skew = %v, want about 2h", delta)
	}
}

// spec: §12.8 (FromEnv reads LENNY_CLOCK_OFFSET_SECONDS)
func TestFromEnvParsesOffset(t *testing.T) {
	t.Cleanup(ResetForTest)
	t.Setenv("LENNY_CLOCK_OFFSET_SECONDS", "3600")
	if err := FromEnv(); err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	off, src := Offset()
	if off != time.Hour || src != "LENNY_CLOCK_OFFSET_SECONDS" {
		t.Errorf("Offset = (%v, %q), want (1h, env)", off, src)
	}
}

// spec: §12.8 (FromEnv rejects a non-numeric value)
func TestFromEnvRejectsGarbage(t *testing.T) {
	t.Cleanup(ResetForTest)
	t.Setenv("LENNY_CLOCK_OFFSET_SECONDS", "not-an-int")
	if err := FromEnv(); err == nil {
		t.Error("FromEnv on a non-numeric value should fail")
	}
}

// spec: §12.8 (Wrap applies the installed offset to any clock source)
func TestWrapAppliesOffsetToBase(t *testing.T) {
	t.Cleanup(ResetForTest)
	SetOffsetForTest(-30 * time.Minute)
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	c := Wrap(func() time.Time { return base })
	if got := c(); !got.Equal(base.Add(-30 * time.Minute)) {
		t.Errorf("Wrap = %v, want base + (-30m)", got)
	}
}

// spec: §12.8 (Wrap with nil base falls back to time.Now)
func TestWrapNilBaseUsesTimeNow(t *testing.T) {
	t.Cleanup(ResetForTest)
	SetOffsetForTest(1 * time.Hour)
	c := Wrap(nil)
	delta := c().Sub(time.Now())
	if delta < time.Hour-time.Second || delta > time.Hour+time.Second {
		t.Errorf("Wrap(nil) skew = %v, want about 1h", delta)
	}
}

// spec: §17.6 (preflight rejects a non-zero offset in production)
func TestAssertProductionDefaultRejectsNonZero(t *testing.T) {
	t.Cleanup(ResetForTest)
	t.Setenv("LENNY_CLOCK_OFFSET_SECONDS", "60")
	err := AssertProductionDefault()
	if !errors.Is(err, ErrProductionNonZero) {
		t.Errorf("err = %v, want ErrProductionNonZero", err)
	}
}

// spec: §17.6 (preflight accepts the production default of unset or 0)
func TestAssertProductionDefaultAcceptsZeroAndUnset(t *testing.T) {
	t.Cleanup(ResetForTest)
	t.Setenv("LENNY_CLOCK_OFFSET_SECONDS", "0")
	if err := AssertProductionDefault(); err != nil {
		t.Errorf("unexpected err for 0: %v", err)
	}
	// Unset case: t.Setenv ensures cleanup, so this Unsetenv works for
	// the body of this test only.
	if err := unsetForTest(t, "LENNY_CLOCK_OFFSET_SECONDS", AssertProductionDefault); err != nil {
		t.Errorf("unexpected err when env unset: %v", err)
	}
}

// unsetForTest unsets name for the duration of fn, restoring the
// prior value on exit. It is a tiny helper so a single test can
// exercise both the set-to-zero and unset cases for the production
// gate.
func unsetForTest(t *testing.T, name string, fn func() error) error {
	t.Helper()
	prev, hadPrev := lookupEnvForTest(name)
	if err := unsetEnvForTest(name); err != nil {
		t.Fatalf("Unsetenv %s: %v", name, err)
	}
	defer func() {
		if hadPrev {
			_ = setEnvForTest(name, prev)
		}
	}()
	return fn()
}
