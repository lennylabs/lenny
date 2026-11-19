// SPDX-License-Identifier: MIT

package recycle

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"testing"
)

// TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults checks that
// RegisterFlags registers each flag under the name and with the default the
// gateway's flag surface gives it, and that --claim-hold-ttl-seconds takes its
// default from LENNY_CLAIM_HOLD_TTL_SECONDS. The flag registration is wiring
// that no requirement states, so this test carries no specification annotation.
func TestRegisterFlagsKeepsTheGatewayFlagNamesAndDefaults(t *testing.T) {
	for _, env := range []string{
		"LENNY_CLAIM_HOLD_TTL_SECONDS",
	} {
		t.Setenv(env, "")
	}
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := RegisterFlags(fs)
	for name, def := range map[string]string{
		"claim-hold-ttl-seconds": "10",
	} {
		fl := fs.Lookup(name)
		if fl == nil {
			t.Errorf("--%s is not registered", name)
			continue
		}
		if fl.DefValue != def {
			t.Errorf("--%s default = %q, want %q", name, fl.DefValue, def)
		}
	}

	t.Setenv("LENNY_CLAIM_HOLD_TTL_SECONDS", "7")
	f = RegisterFlags(flag.NewFlagSet("lenny-gateway", flag.ContinueOnError))
	if got := *f.ClaimHoldTTLSeconds; got != 7 {
		t.Errorf("ClaimHoldTTLSeconds environment default = %v, want 7", got)
	}
}

// TestFlagsHoldTTLWarningAboveTheAdvisoryCeiling checks that the reserved-hold
// TTL warning is returned only for a TTL above HighClaimHoldTTLWarnSeconds,
// and that it names the value and the ceiling.
//
// spec: §4.6.1, §4.6.2
func TestFlagsHoldTTLWarningAboveTheAdvisoryCeiling(t *testing.T) {
	f := RegisterFlags(flag.NewFlagSet("lenny-gateway", flag.ContinueOnError))
	for _, ttl := range []int{10, HighClaimHoldTTLWarnSeconds} {
		*f.ClaimHoldTTLSeconds = ttl
		if msg, warn := f.HoldTTLWarning(); warn || msg != "" {
			t.Errorf("HoldTTLWarning at %ds = %q, %v, want no warning", ttl, msg, warn)
		}
	}
	*f.ClaimHoldTTLSeconds = HighClaimHoldTTLWarnSeconds + 1
	msg, warn := f.HoldTTLWarning()
	if !warn {
		t.Fatalf("HoldTTLWarning at %ds returned no warning", *f.ClaimHoldTTLSeconds)
	}
	want := fmt.Sprintf("WARNING: claimHoldTTLSeconds=%ds exceeds the advisory ceiling of %ds (§4.6.1)", HighClaimHoldTTLWarnSeconds+1, HighClaimHoldTTLWarnSeconds)
	if !strings.HasPrefix(msg, want) {
		t.Errorf("HoldTTLWarning = %q, want the prefix %q", msg, want)
	}
}
