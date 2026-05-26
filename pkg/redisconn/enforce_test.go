// SPDX-License-Identifier: MIT

// Internal-package tests for the §12.4 AUTH-and-TLS enforcement so the
// unexported pinTLSFloor helper can be exercised directly.
package redisconn

import (
	"crypto/tls"
	"errors"
	"testing"
)

// TestNewClientEnforcesAuth_spec_12_4 covers the §12.4 line 197 "Redis
// AUTH (ACLs) ... are required" invariant: with enforcement active
// (AllowInsecure false) a missing AUTH credential fails closed on both
// the direct-URL and Sentinel paths.
func TestNewClientEnforcesAuth_spec_12_4(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"url plaintext no password", Config{URL: "redis://localhost:6379/0"}},
		{"url rediss no password", Config{URL: "rediss://localhost:6379/0"}},
		{"sentinel no password", Config{SentinelAddrs: []string{"s1:26379"}, MasterName: "lenny-master"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewClient(tc.cfg); !errors.Is(err, ErrAuthRequired) {
				t.Fatalf("want ErrAuthRequired, got %v", err)
			}
		})
	}
}

// TestNewClientEnforcesTLS_spec_12_4 covers the §12.4 line 197 "... and
// TLS are required" invariant: a plaintext redis:// URL with a valid
// AUTH credential still fails closed because the scheme is not TLS.
func TestNewClientEnforcesTLS_spec_12_4(t *testing.T) {
	if _, err := NewClient(Config{URL: "redis://:secret@localhost:6379/0"}); !errors.Is(err, ErrTLSRequired) {
		t.Fatalf("plaintext URL with AUTH must fail the TLS check: got %v", err)
	}
}

// TestNewClientURLAuthAndTLS_spec_12_4 confirms a compliant rediss://
// URL with an AUTH credential is accepted and the TLS floor is pinned
// to 1.2 (§12.4). It also confirms the credential may arrive via the
// Password field (the --redis-password flag) rather than the userinfo.
func TestNewClientURLAuthAndTLS_spec_12_4(t *testing.T) {
	c, err := NewClient(Config{URL: "rediss://:secret@localhost:6379/0"})
	if err != nil {
		t.Fatalf("compliant rediss URL rejected: %v", err)
	}
	opt := c.Options()
	if opt.Password != "secret" {
		t.Fatalf("password not applied: %q", opt.Password)
	}
	if opt.TLSConfig == nil || opt.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("rediss:// must carry a TLSConfig pinned to 1.2, got %+v", opt.TLSConfig)
	}

	c2, err := NewClient(Config{URL: "rediss://localhost:6379/0", Password: "fromflag"})
	if err != nil {
		t.Fatalf("Password-field AUTH rejected: %v", err)
	}
	if c2.Options().Password != "fromflag" {
		t.Fatalf("Password field not applied: %q", c2.Options().Password)
	}
}

// TestNewClientSentinelForcesTLS_spec_12_4 covers the §12.4 Sentinel-path
// gap: with enforcement active the FailoverClient is built with a
// non-nil TLSConfig pinned to 1.2 rather than dialing plaintext.
func TestNewClientSentinelForcesTLS_spec_12_4(t *testing.T) {
	c, err := NewClient(Config{
		SentinelAddrs: []string{"s1:26379"},
		MasterName:    "lenny-master",
		Password:      "secret",
	})
	if err != nil {
		t.Fatalf("compliant Sentinel config rejected: %v", err)
	}
	opt := c.Options()
	if opt.TLSConfig == nil || opt.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("Sentinel path must enable TLS pinned to 1.2 under enforcement, got %+v", opt.TLSConfig)
	}
}

// TestNewClientAllowInsecure_spec_12_4 covers the dev opt-out: with
// AllowInsecure set, an unauthenticated plaintext Redis is permitted on
// both paths, while an explicit TLS opt-in is still honored.
func TestNewClientAllowInsecure_spec_12_4(t *testing.T) {
	t.Run("url plaintext no auth", func(t *testing.T) {
		c, err := NewClient(Config{URL: "redis://localhost:6379/0", AllowInsecure: true})
		if err != nil {
			t.Fatalf("AllowInsecure must permit plaintext URL: %v", err)
		}
		if c.Options().TLSConfig != nil {
			t.Fatal("plaintext URL must not carry a TLSConfig")
		}
	})
	t.Run("sentinel no auth no tls", func(t *testing.T) {
		c, err := NewClient(Config{
			SentinelAddrs: []string{"s1:26379"},
			MasterName:    "lenny-master",
			AllowInsecure: true,
		})
		if err != nil {
			t.Fatalf("AllowInsecure must permit plaintext Sentinel: %v", err)
		}
		if c.Options().TLSConfig != nil {
			t.Fatal("plaintext Sentinel must not carry a TLSConfig")
		}
	})
	t.Run("sentinel tls opt-in still honored", func(t *testing.T) {
		c, err := NewClient(Config{
			SentinelAddrs: []string{"s1:26379"},
			MasterName:    "lenny-master",
			AllowInsecure: true,
			TLS:           true,
		})
		if err != nil {
			t.Fatalf("AllowInsecure+TLS rejected: %v", err)
		}
		if c.Options().TLSConfig == nil {
			t.Fatal("explicit TLS opt-in must build a TLSConfig even under AllowInsecure")
		}
	})
}

// TestNewClientErrorOrdering confirms source validation runs before the
// §12.4 AUTH check, so a Sentinel config missing its master name still
// surfaces the more specific ErrMissingMasterName, and a malformed URL
// surfaces a parse error even when a credential is supplied.
func TestNewClientErrorOrdering(t *testing.T) {
	if _, err := NewClient(Config{SentinelAddrs: []string{"s1:26379"}, Password: "x"}); !errors.Is(err, ErrMissingMasterName) {
		t.Fatalf("want ErrMissingMasterName before AUTH, got %v", err)
	}
	if _, err := NewClient(Config{URL: "://bad", Password: "x"}); err == nil {
		t.Fatal("malformed URL must error")
	}
}

// TestPinTLSFloor confirms the floor is raised only when unset, so an
// operator who pinned a higher version is not downgraded, and that a
// nil config is tolerated.
func TestPinTLSFloor(t *testing.T) {
	c := &tls.Config{}
	pinTLSFloor(c)
	if c.MinVersion != tls.VersionTLS12 {
		t.Fatalf("unset floor not raised to 1.2: got %#x", c.MinVersion)
	}
	c = &tls.Config{MinVersion: tls.VersionTLS13}
	pinTLSFloor(c)
	if c.MinVersion != tls.VersionTLS13 {
		t.Fatalf("explicit 1.3 floor downgraded: got %#x", c.MinVersion)
	}
	pinTLSFloor(nil) // must not panic
}
