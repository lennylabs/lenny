// SPDX-License-Identifier: MIT

package tlsprobe

import (
	"flag"
	"os"
)

// Flags holds the flags only the startup TLS probe reads: the backend
// listeners it checks and the TLS material it presents.
type Flags struct {
	// RedisAddr is the host:port of the Redis TLS listener.
	RedisAddr *string
	// PgBouncerAddr is the host:port of the PgBouncer TLS listener.
	PgBouncerAddr *string
	// CA is the CA bundle that verifies the server certificates.
	CA *string
	// Cert is the client certificate the probe presents.
	Cert *string
	// Key is the private key for Cert.
	Key *string
}

// RegisterFlags registers the probe's flags on fs and returns their values,
// which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.RedisAddr = fs.String("startup-tls-probe-redis-addr", os.Getenv("LENNY_STARTUP_TLS_PROBE_REDIS_ADDR"),
		"§10.3 host:port of the Redis TLS listener the startup probe checks (TLS handshake must succeed; plaintext must be refused). Empty disables the Redis leg. Override via LENNY_STARTUP_TLS_PROBE_REDIS_ADDR. F-10.3.15.")
	f.PgBouncerAddr = fs.String("startup-tls-probe-pgbouncer-addr", os.Getenv("LENNY_STARTUP_TLS_PROBE_PGBOUNCER_ADDR"),
		"§10.3 host:port of the PgBouncer TLS listener the startup probe checks. Empty disables the PgBouncer leg. Override via LENNY_STARTUP_TLS_PROBE_PGBOUNCER_ADDR. F-10.3.15.")
	f.CA = fs.String("startup-tls-probe-ca", os.Getenv("LENNY_STARTUP_TLS_PROBE_CA"),
		"§10.3 CA bundle that verifies the Redis/PgBouncer server certificates during the startup TLS probe. Empty uses the system trust store. Override via LENNY_STARTUP_TLS_PROBE_CA. F-10.3.15.")
	f.Cert = fs.String("startup-tls-probe-cert", os.Getenv("LENNY_STARTUP_TLS_PROBE_CERT"),
		"§10.3 client certificate presented during the startup TLS probe (Redis tls-auth-clients requires one). Empty presents no client certificate. Override via LENNY_STARTUP_TLS_PROBE_CERT. F-10.3.15.")
	f.Key = fs.String("startup-tls-probe-key", os.Getenv("LENNY_STARTUP_TLS_PROBE_KEY"),
		"§10.3 private key for --startup-tls-probe-cert. Override via LENNY_STARTUP_TLS_PROBE_KEY. F-10.3.15.")
	return f
}
