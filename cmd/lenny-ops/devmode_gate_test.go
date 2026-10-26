// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// TestBuildAuthConfig_DevModeGatesRelaxations pins §17.4 "Dev mode guard
// rails" item 3 on lenny-ops: --dev-mode (LENNY_DEV_MODE) is the single
// gate for the dev bearer-trust hook, the X-Lenny-* identity headers, the
// dev roles, and the unauthenticated fallback. --production does not
// widen anything: without dev mode every posture authenticates through
// the §10.2 key set and ignores the identity headers, and --production
// keeps its refusal of the trust hook even with dev mode on.
//
// spec: 17.4 (Dev mode guard rails), 25.4 (The lenny-ops Service: Authentication)
func TestBuildAuthConfig_DevModeGatesRelaxations(t *testing.T) {
	keySet, _ := testKeySet(t, 0x31)
	keyFile := writeHMACKeyFile(t, "dev-gate-secret")

	type want struct {
		err        error // non-nil: buildAuthConfig refuses with this error
		open       bool  // nil config: the unauthenticated dev surface
		devHeaders bool  // AllowDevHeaders and AllowDevRoles
		devTrust   bool  // the verifier includes the dev trust member
	}
	cases := []struct {
		name       string
		devMode    bool
		production bool
		withKey    bool
		want       want
	}{
		{name: "no dev mode, no production, no key", want: want{}},
		{name: "no dev mode, no production, key", withKey: true, want: want{err: errDevTrustWithoutDevMode}},
		{name: "no dev mode, production, no key", production: true, want: want{}},
		{name: "no dev mode, production, key", production: true, withKey: true, want: want{err: errDevTrustInProduction}},
		{name: "dev mode, no production, no key", devMode: true, want: want{open: true}},
		{name: "dev mode, no production, key", devMode: true, withKey: true, want: want{devHeaders: true, devTrust: true}},
		{name: "dev mode, production, no key", devMode: true, production: true, want: want{}},
		{name: "dev mode, production, key", devMode: true, production: true, withKey: true, want: want{err: errDevTrustInProduction}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hmac := ""
			if tc.withKey {
				hmac = keyFile
			}
			cfg, err := buildAuthConfig(authInputsFor(keySet, hmac, "", tc.devMode, tc.production))
			if tc.want.err != nil {
				if !errors.Is(err, tc.want.err) {
					t.Fatalf("err = %v, want %v", err, tc.want.err)
				}
				if cfg != nil {
					t.Fatalf("cfg = %v, want nil on error", cfg)
				}
				// checkDevTrustPosture is the startup check runOps applies
				// before the key set loads; it must refuse the same posture.
				if perr := checkDevTrustPosture(tc.devMode, tc.production, hmac); !errors.Is(perr, tc.want.err) {
					t.Errorf("checkDevTrustPosture = %v, want %v", perr, tc.want.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildAuthConfig: %v", err)
			}
			if perr := checkDevTrustPosture(tc.devMode, tc.production, hmac); perr != nil {
				t.Errorf("checkDevTrustPosture = %v, want nil", perr)
			}
			if tc.want.open {
				if cfg != nil {
					t.Fatalf("cfg = %+v, want nil (unauthenticated dev surface)", cfg)
				}
				return
			}
			if cfg == nil {
				t.Fatal("cfg = nil: the surface is unauthenticated outside dev mode")
			}
			if cfg.Options.AllowDevHeaders != tc.want.devHeaders {
				t.Errorf("AllowDevHeaders = %v, want %v", cfg.Options.AllowDevHeaders, tc.want.devHeaders)
			}
			if cfg.Options.AllowDevRoles != tc.want.devHeaders {
				t.Errorf("AllowDevRoles = %v, want %v", cfg.Options.AllowDevRoles, tc.want.devHeaders)
			}
			gotTrust := cfg.Options.Verifier != keySet
			if gotTrust != tc.want.devTrust {
				t.Errorf("dev trust member present = %v, want %v (verifier %T)", gotTrust, tc.want.devTrust, cfg.Options.Verifier)
			}
		})
	}
}

// TestOpsSurfaceRefusesDevIdentityHeadersWithoutDevMode is the F-17.4.35
// regression: with dev mode off and --production false, the default
// posture of a chart install that leaves ops.production unset, a request
// that names a tenant, a user, and the platform-admin role through the
// X-Lenny-* headers and carries no bearer is refused with 401 rather than
// served.
//
// spec: 17.4 (Dev mode guard rails), 25.4 (The lenny-ops Service: Authentication)
func TestOpsSurfaceRefusesDevIdentityHeadersWithoutDevMode(t *testing.T) {
	keySet, _ := testKeySet(t, 0x31)
	cfg, err := buildAuthConfig(authInputsFor(keySet, "", "", false, false))
	if err != nil {
		t.Fatalf("buildAuthConfig: %v", err)
	}
	srv := opsserver.New(opsserver.Options{Locks: coordination.NewMemStore(), Auth: cfg})

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/remediation-locks", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

// TestEnvDevModeMatchesGatewayParsing pins that lenny-ops reads
// LENNY_DEV_MODE as the gateway does, so one value of the variable gates
// the relaxations of both binaries the same way.
//
// spec: 17.4 (Dev mode guard rails)
func TestEnvDevModeMatchesGatewayParsing(t *testing.T) {
	cases := map[string]bool{
		"": false, "true": true, "TRUE": true, " on ": true, "1": true, "yes": true,
		"false": false, "0": false, "off": false, "no": false, "garbage": false,
	}
	for v, want := range cases {
		t.Setenv("LENNY_DEV_MODE", v)
		if got := envDevMode(); got != want {
			t.Errorf("LENNY_DEV_MODE=%q: envDevMode() = %v, want %v", v, got, want)
		}
	}
}
