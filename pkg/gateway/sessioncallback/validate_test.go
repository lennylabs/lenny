// SPDX-License-Identifier: MIT

package sessioncallback

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

// stubResolver maps a hostname to a fixed set of addresses for the §14
// DNS-pinning tests so the private-range checks run without real DNS.
type stubResolver struct {
	addrs map[string][]netip.Addr
	err   error
}

func (s stubResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if s.err != nil {
		return nil, s.err
	}
	a, ok := s.addrs[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	return a, nil
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("ParseAddr(%q): %v", s, err)
	}
	return a
}

// TestValidateRejectReasons exercises the §14 SSRF mitigation reasons the
// §15.1 INVALID_CALLBACK_URL envelope carries.
// spec: §14 lines 108-112; §15.1 line 1097. F-14.1.11.
func TestValidateRejectReasons_spec_14_108(t *testing.T) {
	resolver := stubResolver{addrs: map[string][]netip.Addr{
		"public.example.com":  {mustAddr(t, "93.184.216.34")},
		"private.example.com": {mustAddr(t, "10.0.0.5")},
		"cgnat.example.com":   {mustAddr(t, "100.64.0.1")},
		"linklocal.example":   {mustAddr(t, "169.254.169.254")},
	}}
	v := NewValidator(nil, resolver)

	cases := []struct {
		name       string
		url        string
		wantReason string
	}{
		{"non-https", "http://public.example.com/hook", ReasonSchemeNotHTTPS},
		{"not-a-url", "://nope", ReasonInvalidURL},
		{"ip-literal", "https://93.184.216.34/hook", ReasonIPLiteral},
		{"localhost", "https://localhost/hook", ReasonMetadataHost},
		{"gcp-metadata", "https://metadata.google.internal/hook", ReasonMetadataHost},
		{"private-resolve", "https://private.example.com/hook", ReasonPrivateIP},
		{"cgnat-resolve", "https://cgnat.example.com/hook", ReasonPrivateIP},
		{"linklocal-resolve", "https://linklocal.example/hook", ReasonPrivateIP},
		{"dns-failure", "https://nope.example/hook", ReasonDNSResolutionFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := v.Validate(context.Background(), c.url)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("Validate(%q) error = %v, want *ValidationError", c.url, err)
			}
			if ve.Reason != c.wantReason {
				t.Errorf("Validate(%q) reason = %q, want %q", c.url, ve.Reason, c.wantReason)
			}
		})
	}
}

// TestValidatePinsPublicIP confirms a public host validates and the first
// resolved IP is pinned. spec: §14 line 110. F-14.1.11.
func TestValidatePinsPublicIP_spec_14_110(t *testing.T) {
	resolver := stubResolver{addrs: map[string][]netip.Addr{
		"public.example.com": {mustAddr(t, "93.184.216.34"), mustAddr(t, "93.184.216.35")},
	}}
	v := NewValidator(nil, resolver)
	res, err := v.Validate(context.Background(), "https://public.example.com/hook")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if res.PinnedIP.String() != "93.184.216.34" {
		t.Errorf("PinnedIP = %s, want 93.184.216.34", res.PinnedIP)
	}
	if res.URL != "https://public.example.com/hook" {
		t.Errorf("URL = %s", res.URL)
	}
}

// TestValidateRejectsMixedPrivate confirms that when a host resolves to
// any private address the callback is rejected even if a public address
// is also returned. spec: §14 line 110. F-14.1.11.
func TestValidateRejectsMixedPrivate_spec_14_110(t *testing.T) {
	resolver := stubResolver{addrs: map[string][]netip.Addr{
		"mixed.example.com": {mustAddr(t, "93.184.216.34"), mustAddr(t, "10.0.0.5")},
	}}
	v := NewValidator(nil, resolver)
	if _, err := v.Validate(context.Background(), "https://mixed.example.com/hook"); err == nil {
		t.Fatal("Validate accepted a host with a private address in its set")
	}
}

// TestValidateDomainAllowlist exercises the §14 line 112
// callbackUrlAllowedDomains exact and wildcard matching. F-14.1.11.
func TestValidateDomainAllowlist_spec_14_112(t *testing.T) {
	resolver := stubResolver{addrs: map[string][]netip.Addr{
		"hooks.acme.com":     {mustAddr(t, "93.184.216.34")},
		"sub.hooks.acme.com": {mustAddr(t, "93.184.216.34")},
		"evil.example.com":   {mustAddr(t, "93.184.216.34")},
	}}
	v := NewValidator([]string{"hooks.acme.com", "*.glob.example"}, resolver)

	if _, err := v.Validate(context.Background(), "https://hooks.acme.com/h"); err != nil {
		t.Errorf("exact allowlist host rejected: %v", err)
	}
	if _, err := v.Validate(context.Background(), "https://evil.example.com/h"); err == nil {
		t.Error("off-allowlist host accepted")
	} else {
		var ve *ValidationError
		if errors.As(err, &ve) && ve.Reason != ReasonDomainNotAllowed {
			t.Errorf("reason = %q, want %q", ve.Reason, ReasonDomainNotAllowed)
		}
	}

	// A *.glob.example wildcard matches a subdomain but not the bare suffix.
	vGlob := NewValidator([]string{"*.glob.example"}, stubResolver{addrs: map[string][]netip.Addr{
		"a.glob.example": {mustAddr(t, "93.184.216.34")},
		"glob.example":   {mustAddr(t, "93.184.216.34")},
	}})
	if _, err := vGlob.Validate(context.Background(), "https://a.glob.example/h"); err != nil {
		t.Errorf("wildcard subdomain rejected: %v", err)
	}
	if _, err := vGlob.Validate(context.Background(), "https://glob.example/h"); err == nil {
		t.Error("wildcard accepted the bare suffix")
	}
}

// TestIsPublicAddr pins the private/reserved-range classification.
// spec: §14 line 110. F-14.1.11.
func TestIsPublicAddr_spec_14_110(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"93.184.216.34", true},
		{"8.8.8.8", true},
		{"2606:2800:220:1:248:1893:25c8:1946", true},
		{"10.0.0.1", false},
		{"172.16.5.4", false},
		{"192.168.1.1", false},
		{"127.0.0.1", false},
		{"169.254.169.254", false},
		{"100.64.0.1", false},
		{"::1", false},
		{"fe80::1", false},
		{"fc00::1", false},
		{"0.0.0.0", false},
	}
	for _, c := range cases {
		if got := isPublicAddr(mustAddr(t, c.ip)); got != c.want {
			t.Errorf("isPublicAddr(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}
