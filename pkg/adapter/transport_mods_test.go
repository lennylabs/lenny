// SPDX-License-Identifier: MIT

package adapter_test

import (
	"crypto/tls"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
)

func TestWithServerNamePinsServerName_spec_10_3_322(t *testing.T) {
	cfg := &tls.Config{}
	adapter.WithServerName("lenny-gateway.lenny-system.svc")(cfg)
	if cfg.ServerName != "lenny-gateway.lenny-system.svc" {
		t.Errorf("ServerName = %q, want the pinned gateway DNS SAN", cfg.ServerName)
	}
}

func TestTLSServerOptionAppliesMods_spec_10_3_321(t *testing.T) {
	certFile, keyFile := writeTestKeypair(t)
	ran := false
	opt, err := adapter.TLSServerOption(certFile, keyFile, "", func(c *tls.Config) {
		ran = true
		// A real mod installs the §10.3 SPIFFE VerifyPeerCertificate hook;
		// here we just confirm the mod observes the assembled config. The
		// leaf is served via the §10.3 line 338 filesystem-watching
		// GetCertificate callback rather than a static Certificates slice.
		if c.GetCertificate == nil {
			t.Error("mod ran before the base GetCertificate callback was assembled")
		}
	})
	if err != nil {
		t.Fatalf("TLSServerOption: %v", err)
	}
	if opt == nil {
		t.Fatal("TLSServerOption returned a nil option for a valid keypair")
	}
	if !ran {
		t.Error("TLSServerOption did not apply the supplied mod")
	}
}

func TestTLSServerOptionSkipsModsOnPlaintext_spec_10_3_321(t *testing.T) {
	ran := false
	opt, err := adapter.TLSServerOption("", "", "", func(*tls.Config) { ran = true })
	if err != nil {
		t.Fatalf("TLSServerOption: %v", err)
	}
	if opt != nil {
		t.Error("TLSServerOption should return a nil option on the plaintext path")
	}
	if ran {
		t.Error("mods must not run on the plaintext path where there is no tls.Config")
	}
}

func TestTLSClientOptionAppliesServerNameMod_spec_10_3_322(t *testing.T) {
	certFile, keyFile := writeTestKeypair(t)
	ran := false
	opt, err := adapter.TLSClientOption(certFile, keyFile, certFile,
		adapter.WithServerName("lenny-gateway.lenny-system.svc"),
		func(c *tls.Config) {
			ran = true
			if c.ServerName != "lenny-gateway.lenny-system.svc" {
				t.Errorf("ServerName not pinned before trailing mod: %q", c.ServerName)
			}
		},
	)
	if err != nil {
		t.Fatalf("TLSClientOption: %v", err)
	}
	if opt == nil {
		t.Fatal("TLSClientOption returned a nil option for a valid mTLS configuration")
	}
	if !ran {
		t.Error("TLSClientOption did not apply the supplied mods")
	}
}
