// SPDX-License-Identifier: MIT

// Package sessioncallback implements the §14 session-completion webhook
// callback: the SSRF validation of the client-supplied callbackUrl, the
// pinned-IP HTTP transport, the CloudEvents v1.0.2 delivery envelope, the
// HMAC-SHA256 signing, and the bounded-retry delivery worker. The
// callbackSecret is KMS-envelope-encrypted by the caller; this package
// receives an opener that recovers the plaintext at delivery time.
//
// spec: §14 lines 73-74, 108-152 (callbackUrl, callbackSecret, Webhook
// Delivery Model); §15.1 line 1097 (INVALID_CALLBACK_URL).
package sessioncallback

import (
	"context"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// ValidationError reports a callbackUrl that failed §14 SSRF validation.
// Reason is the machine-readable cause the §15.1 INVALID_CALLBACK_URL
// envelope carries in details.reason. spec: §15.1 line 1097.
type ValidationError struct {
	Reason  string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// reason codes carried on a ValidationError. The §15.1 line 1097 list is
// non-exhaustive ("e.g."), so the IP-literal / DNS-failure cases extend
// the named set with descriptive codes.
const (
	ReasonInvalidURL          = "invalid_url"
	ReasonSchemeNotHTTPS      = "scheme_not_https"
	ReasonIPLiteral           = "ip_literal"
	ReasonMetadataHost        = "metadata_host"
	ReasonPrivateIP           = "private_ip"
	ReasonDNSResolutionFailed = "dns_resolution_failed"
	ReasonNoPublicIP          = "no_public_ip"
	ReasonDomainNotAllowed    = "domain_not_allowlisted"
)

// metadataHosts are the §14 line 109 well-known cloud metadata
// hostnames rejected regardless of their resolved IP. Comparison is
// case-insensitive and tolerant of a trailing dot.
var metadataHosts = map[string]bool{
	"metadata.google.internal": true,
	"instance-data":            true,
}

// Resolver is the DNS seam the §14 line 110 DNS-pinning step uses. The
// production validator uses net.DefaultResolver; tests inject a stub so
// the private-range checks run without real DNS.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Validator enforces the §14 callbackUrl SSRF mitigations: HTTPS-only,
// IP-literal / localhost / loopback / link-local rejection, cloud
// metadata-host rejection, DNS resolution with private-range rejection
// and IP pinning, and the optional deployer domain allowlist.
type Validator struct {
	// AllowedDomains is the §14 line 112 callbackUrlAllowedDomains. When
	// non-empty, a callback host must match an entry exactly or as a
	// `*.suffix` wildcard. When empty the public-DNS validation applies.
	AllowedDomains []string
	resolver       Resolver
}

// Result is a validated callbackUrl: the normalized URL and the IP the
// §14 line 110 DNS pin resolved, which the delivery transport dials
// directly to defeat DNS rebinding.
type Result struct {
	URL      string
	PinnedIP netip.Addr
}

// NewValidator returns a Validator using resolver for DNS lookups. A nil
// resolver falls back to net.DefaultResolver.
func NewValidator(allowedDomains []string, resolver Resolver) *Validator {
	return &Validator{AllowedDomains: allowedDomains, resolver: resolver}
}

// Validate applies the §14 SSRF mitigations to raw and returns the
// pinned IP on success. On failure it returns a *ValidationError whose
// Reason is the §15.1 INVALID_CALLBACK_URL details.reason. spec: §14
// lines 108-112.
func (v *Validator) Validate(ctx context.Context, raw string) (Result, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Host == "" {
		return Result{}, &ValidationError{Reason: ReasonInvalidURL, Message: "callbackUrl is not a valid URL"}
	}
	// spec: §14 line 109 — HTTPS only; no http, no non-HTTP schemes.
	if !strings.EqualFold(u.Scheme, "https") {
		return Result{}, &ValidationError{Reason: ReasonSchemeNotHTTPS, Message: "callbackUrl must use the https scheme"}
	}
	host := u.Hostname()
	if host == "" {
		return Result{}, &ValidationError{Reason: ReasonInvalidURL, Message: "callbackUrl has no host"}
	}

	// spec: §14 line 109 — IP literals, localhost, loopback, and
	// link-local hosts are rejected at submission time; the callback must
	// target a public DNS hostname.
	if _, perr := netip.ParseAddr(host); perr == nil {
		return Result{}, &ValidationError{Reason: ReasonIPLiteral, Message: "callbackUrl must be a DNS hostname, not an IP literal"}
	}
	lowHost := strings.ToLower(strings.TrimSuffix(host, "."))
	if lowHost == "localhost" {
		return Result{}, &ValidationError{Reason: ReasonMetadataHost, Message: "callbackUrl host localhost is not allowed"}
	}
	// spec: §14 line 109 — cloud metadata hostnames are rejected
	// regardless of resolved IP, as defense in depth.
	if metadataHosts[lowHost] {
		return Result{}, &ValidationError{Reason: ReasonMetadataHost, Message: "callbackUrl host is a cloud metadata endpoint"}
	}

	// spec: §14 line 112 — when the deployer allowlist is non-empty, the
	// host must match an entry; otherwise the public-DNS check applies.
	if len(v.AllowedDomains) > 0 && !matchesAllowlist(lowHost, v.AllowedDomains) {
		return Result{}, &ValidationError{Reason: ReasonDomainNotAllowed, Message: "callbackUrl host is not in callbackUrlAllowedDomains"}
	}

	// spec: §14 line 110 — resolve the hostname and reject when any
	// resolved IP falls in a private or reserved range; pin the first
	// public IP so the transport dials it directly.
	addrs, err := v.resolve(ctx, host)
	if err != nil {
		return Result{}, &ValidationError{Reason: ReasonDNSResolutionFailed, Message: "callbackUrl host did not resolve: " + err.Error()}
	}
	var pinned netip.Addr
	for _, a := range addrs {
		if !isPublicAddr(a) {
			return Result{}, &ValidationError{Reason: ReasonPrivateIP, Message: fmt.Sprintf("callbackUrl resolved to a private or reserved address %s", a)}
		}
		if !pinned.IsValid() {
			pinned = a
		}
	}
	if !pinned.IsValid() {
		return Result{}, &ValidationError{Reason: ReasonNoPublicIP, Message: "callbackUrl host resolved to no usable address"}
	}
	return Result{URL: raw, PinnedIP: pinned}, nil
}

func (v *Validator) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	r := v.resolver
	if r == nil {
		r = defaultResolver{}
	}
	return r.LookupNetIP(ctx, "ip", host)
}

// matchesAllowlist reports whether host matches an allowlist entry. An
// entry is either an exact hostname or a `*.suffix` wildcard matching
// any subdomain of suffix (but not the bare suffix). spec: §14 line 112.
func matchesAllowlist(host string, entries []string) bool {
	for _, raw := range entries {
		e := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(raw, ".")))
		if e == "" {
			continue
		}
		if strings.HasPrefix(e, "*.") {
			suffix := e[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
			continue
		}
		if host == e {
			return true
		}
	}
	return false
}

// isPublicAddr reports whether a is a globally routable unicast address.
// It rejects RFC 1918 / RFC 4193 private ranges, loopback, link-local,
// unspecified, multicast, the RFC 6598 carrier-grade-NAT range, and the
// IPv4-mapped form of any of those. spec: §14 line 110.
func isPublicAddr(a netip.Addr) bool {
	if a.Is4In6() {
		a = a.Unmap()
	}
	if !a.IsValid() {
		return false
	}
	if a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() ||
		a.IsPrivate() {
		return false
	}
	// RFC 6598 carrier-grade-NAT 100.64.0.0/10 is not covered by
	// netip.Addr.IsPrivate; reject it explicitly.
	if a.Is4() {
		b := a.As4()
		if b[0] == 100 && b[1] >= 64 && b[1] <= 127 {
			return false
		}
	}
	return true
}
