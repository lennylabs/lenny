// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// spec: §13.2 OTLP-068 — an empty endpoint, or TLS disabled, skips the
// check (plaintext is governed by the chart's acknowledgeOtlpPlaintext
// guard and NOTES banner). F-13.2.9.
func TestOTLPTLSCheck_skips_when_not_applicable(t *testing.T) {
	for _, c := range []OTLPTLSConfig{
		{Endpoint: "", TLSEnabled: true},
		{Endpoint: "http://collector:4318", TLSEnabled: false},
	} {
		if d := (OTLPTLSCheck{Config: c}).Decide(context.Background()); !d.Passed {
			t.Errorf("config %+v should skip and pass, got: %s", c, d.Reason)
		}
	}
}

// spec: §13.2 OTLP-068 — an http:// endpoint while otlpTlsEnabled is true
// fails with OTLP_ENDPOINT_SCHEME_MISMATCH even without a live prober.
// F-13.2.9.
func TestOTLPTLSCheck_http_scheme_fails(t *testing.T) {
	d := (OTLPTLSCheck{Config: OTLPTLSConfig{Endpoint: "http://collector:4318", TLSEnabled: true}}).Decide(context.Background())
	if d.Passed {
		t.Fatal("http:// endpoint with TLS enabled must fail")
	}
	if !strings.HasPrefix(d.Reason, "OTLP_ENDPOINT_SCHEME_MISMATCH:") {
		t.Errorf("missing error code prefix: %s", d.Reason)
	}
}

// spec: §13.2 OTLP-068 — with no prober wired the scheme guard still runs
// but the live handshake is skipped, so an https:// endpoint passes.
// F-13.2.9.
func TestOTLPTLSCheck_https_no_prober_passes(t *testing.T) {
	d := (OTLPTLSCheck{Config: OTLPTLSConfig{Endpoint: "https://collector:4317", TLSEnabled: true}}).Decide(context.Background())
	if !d.Passed {
		t.Fatalf("https endpoint with no prober should pass scheme guard: %s", d.Reason)
	}
}

// spec: §13.2 OTLP-068 — a wired prober runs the live handshake; a
// handshake error fails with OTLP_TLS_HANDSHAKE_FAILED. F-13.2.9.
func TestOTLPTLSCheck_handshake_failure(t *testing.T) {
	check := OTLPTLSCheck{
		Config: OTLPTLSConfig{Endpoint: "https://collector:4317", TLSEnabled: true},
		Prober: OTLPTLSProbeFunc(func(context.Context, string) error {
			return errors.New("x509: certificate signed by unknown authority")
		}),
	}
	d := check.Decide(context.Background())
	if d.Passed {
		t.Fatal("a failing handshake must fail the check")
	}
	if !strings.HasPrefix(d.Reason, "OTLP_TLS_HANDSHAKE_FAILED:") {
		t.Errorf("missing error code prefix: %s", d.Reason)
	}
}

// spec: §13.2 OTLP-068 — a successful handshake against an https://
// endpoint passes. F-13.2.9.
func TestOTLPTLSCheck_handshake_success(t *testing.T) {
	var probed string
	check := OTLPTLSCheck{
		Config: OTLPTLSConfig{Endpoint: "https://collector:4317", TLSEnabled: true},
		Prober: OTLPTLSProbeFunc(func(_ context.Context, endpoint string) error {
			probed = endpoint
			return nil
		}),
	}
	if d := check.Decide(context.Background()); !d.Passed {
		t.Fatalf("successful handshake should pass: %s", d.Reason)
	}
	if probed != "https://collector:4317" {
		t.Errorf("prober received endpoint %q", probed)
	}
}
