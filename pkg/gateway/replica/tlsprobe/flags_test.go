// SPDX-License-Identifier: MIT

package tlsprobe

import (
	"flag"
	"io"
	"testing"
)

// The flag registration is wiring that no requirement states, so this test
// carries no specification annotation.
func TestRegisterFlagsReadTheEnvironmentAndTheCommandLine(t *testing.T) {
	t.Setenv("LENNY_STARTUP_TLS_PROBE_REDIS_ADDR", "redis:6380")
	t.Setenv("LENNY_STARTUP_TLS_PROBE_PGBOUNCER_ADDR", "")
	t.Setenv("LENNY_STARTUP_TLS_PROBE_CERT", "")
	t.Setenv("LENNY_STARTUP_TLS_PROBE_KEY", "")
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := RegisterFlags(fs)
	if err := fs.Parse([]string{"--startup-tls-probe-ca=/etc/ca.pem"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if *f.RedisAddr != "redis:6380" || *f.PgBouncerAddr != "" || *f.CA != "/etc/ca.pem" || *f.Cert != "" || *f.Key != "" {
		t.Errorf("flags = redis %q, pgbouncer %q, ca %q, cert %q, key %q", *f.RedisAddr, *f.PgBouncerAddr, *f.CA, *f.Cert, *f.Key)
	}
}
