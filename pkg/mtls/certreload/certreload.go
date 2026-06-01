// SPDX-License-Identifier: MIT

// Package certreload provides a filesystem-watching TLS certificate
// source. The §10.3 line 338 mTLS posture requires both the gateway and
// the runtime adapter to use `tls.Config` with `GetCertificate` /
// `GetClientCertificate` callbacks that re-read the keypair from the
// cert-manager-managed projected volume, so a renewed leaf certificate
// is picked up transparently without restarting the pod or dropping the
// gRPC connection. A one-shot `tls.LoadX509KeyPair` at startup keeps the
// expired key material in use until the process restarts, which couples
// session duration to certificate lifetime — exactly what the spec
// decouples.
//
// The reloader caches the parsed certificate and re-reads only when the
// cert or key file's modification time advances, so the handshake hot
// path is a pair of stat calls rather than a parse. A read or parse
// failure during reload keeps the last good certificate, so an
// in-progress cert-manager write (the volume is updated non-atomically
// in the worst case) never serves a half-written keypair.
package certreload

import (
	"crypto/tls"
	"fmt"
	"os"
	"sync"
	"time"
)

// Reloader holds a TLS keypair re-read from disk on modification. Its
// GetCertificate / GetClientCertificate methods are installed on a
// tls.Config so renewed certificates are served without a restart.
// spec: §10.3 line 338.
type Reloader struct {
	certFile string
	keyFile  string

	mu      sync.RWMutex
	cached  *tls.Certificate
	certMod time.Time
	keyMod  time.Time
}

// New builds a Reloader and performs the initial load. It returns an
// error when the keypair cannot be read or parsed at startup, so a
// misconfigured path fails fast rather than at the first handshake.
func New(certFile, keyFile string) (*Reloader, error) {
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("certreload: cert and key files are both required")
	}
	r := &Reloader{certFile: certFile, keyFile: keyFile}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// reload reads and parses the keypair and replaces the cached value.
func (r *Reloader) reload() error {
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("certreload: load keypair: %w", err)
	}
	certMod, keyMod := r.modTimes()
	r.mu.Lock()
	r.cached = &cert
	r.certMod = certMod
	r.keyMod = keyMod
	r.mu.Unlock()
	return nil
}

// modTimes returns the current modification times of the cert and key
// files. A stat error yields the zero time, which never advances past a
// previously recorded time, so a transient stat failure does not force a
// reload of an unchanged file.
func (r *Reloader) modTimes() (certMod, keyMod time.Time) {
	if fi, err := os.Stat(r.certFile); err == nil {
		certMod = fi.ModTime()
	}
	if fi, err := os.Stat(r.keyFile); err == nil {
		keyMod = fi.ModTime()
	}
	return certMod, keyMod
}

// maybeReload re-reads the keypair when either file's modification time
// has advanced since the last successful load. On a reload error it
// keeps the last good certificate: a cert-manager renewal that writes
// the cert and key non-atomically can briefly leave a mismatched pair,
// and serving the previous valid keypair until both files settle is
// safer than serving a parse error.
func (r *Reloader) maybeReload() {
	certMod, keyMod := r.modTimes()
	r.mu.RLock()
	changed := certMod.After(r.certMod) || keyMod.After(r.keyMod)
	r.mu.RUnlock()
	if changed {
		_ = r.reload()
	}
}

// GetCertificate satisfies tls.Config.GetCertificate for a server. It is
// invoked once per inbound handshake.
func (r *Reloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.maybeReload()
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cached, nil
}

// GetClientCertificate satisfies tls.Config.GetClientCertificate for a
// client. It is invoked once per outbound handshake when the server
// requests a client certificate.
func (r *Reloader) GetClientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	r.maybeReload()
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cached, nil
}
