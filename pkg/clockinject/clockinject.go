// SPDX-License-Identifier: MIT

// Package clockinject is the §12.8 clock-injection harness for the
// tier-8 chaos tests TestGatewayClockDrift,
// TestCertificateExpiryAdvance, and TestT3T4SLABreach. Production
// code reads time through clockinject.Now (or an injected func
// returning time.Time) instead of time.Now directly; the chaos
// tests set a fixed offset via FromEnv at test-pod startup so the
// gateway under test sees a skewed clock without any libfaketime
// LD_PRELOAD or kernel-time meddling.
//
// The offset is read once at process start from the
// LENNY_CLOCK_OFFSET_SECONDS environment variable. A negative
// value rewinds the clock; a positive value advances it; an unset
// or zero value is the production default and behaves as a pure
// time.Now passthrough. The offset is per-process: chaos tests
// run the gateway under test in a separate pod whose env carries
// the offset, leaving the chaos driver's own clock untouched.
//
// The harness is TEST-ONLY: production code must never set
// LENNY_CLOCK_OFFSET_SECONDS, and the §17.6 preflight Job fails
// startup if the variable is non-zero in a production install.
package clockinject

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// envVar is the §12.8 offset-injection environment variable.
const envVar = "LENNY_CLOCK_OFFSET_SECONDS"

// state is the package-local offset record. The zero value (no
// offset, no source) is the production default.
type state struct {
	offset time.Duration
	source string
}

var (
	mu      sync.RWMutex
	current state
	loaded  bool
)

// FromEnv reads the §12.8 LENNY_CLOCK_OFFSET_SECONDS environment
// variable and installs the parsed offset as the package-local
// clock skew. It returns an error when the variable is set to a
// non-numeric value so a misconfigured chaos-test pod fails loudly
// rather than silently behaving as production.
//
// Safe to call multiple times: the last successful parse wins and
// the loaded flag stays set so a subsequent FromEnv call is a no-op
// when the env var is unset.
func FromEnv() error {
	raw, ok := os.LookupEnv(envVar)
	if !ok {
		mu.Lock()
		loaded = true
		mu.Unlock()
		return nil
	}
	s, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("clockinject: %s=%q must be an integer second count: %w", envVar, raw, err)
	}
	mu.Lock()
	current = state{offset: time.Duration(s) * time.Second, source: envVar}
	loaded = true
	mu.Unlock()
	return nil
}

// SetOffsetForTest installs a manual offset. Tests use it to
// exercise the package without setting the environment variable;
// production code never calls this.
func SetOffsetForTest(d time.Duration) {
	mu.Lock()
	current = state{offset: d, source: "test"}
	loaded = true
	mu.Unlock()
}

// ResetForTest clears any installed offset. t.Cleanup callers use
// it so an offset installed by one test does not leak into another.
func ResetForTest() {
	mu.Lock()
	current = state{}
	loaded = false
	mu.Unlock()
}

// Offset returns the currently installed offset and the source
// label (LENNY_CLOCK_OFFSET_SECONDS or test).
func Offset() (time.Duration, string) {
	mu.RLock()
	defer mu.RUnlock()
	return current.offset, current.source
}

// Now returns the current wall-clock time plus the installed
// offset. Code that wants to participate in chaos-test clock
// injection reads time through clockinject.Now rather than
// time.Now.
func Now() time.Time { return Wrap(time.Now)() }

// Wrap returns a clock function that applies the installed offset
// to the supplied time source. The result satisfies the
// `func() time.Time` shape every Lenny package already uses for
// injected clocks.
func Wrap(base func() time.Time) func() time.Time {
	if base == nil {
		base = time.Now
	}
	return func() time.Time {
		mu.RLock()
		off := current.offset
		mu.RUnlock()
		return base().Add(off)
	}
}

// ErrProductionNonZero is returned by AssertProductionDefault when
// the env var is set to a non-zero value. The §17.6 preflight Job
// surfaces it as a fatal install error.
var ErrProductionNonZero = errors.New("clockinject: LENNY_CLOCK_OFFSET_SECONDS is non-zero in a production install")

// AssertProductionDefault reports ErrProductionNonZero when the
// env var is set to a non-zero value, otherwise nil. The §17.6
// `lenny-preflight` Job calls it at install time so a stray test
// offset cannot reach a production gateway. Tests pass.
func AssertProductionDefault() error {
	raw, ok := os.LookupEnv(envVar)
	if !ok {
		return nil
	}
	s, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("clockinject: %s=%q must be an integer second count: %w", envVar, raw, err)
	}
	if s != 0 {
		return fmt.Errorf("%w: offset=%ds", ErrProductionNonZero, s)
	}
	return nil
}
