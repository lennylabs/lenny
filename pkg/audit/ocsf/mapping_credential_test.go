// SPDX-License-Identifier: MIT

package ocsf

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
)

// spec: §4.9.2 — every credential audit event type declared in
// pkg/credential must resolve to an OCSF class via LookupClass, or the
// §11.7 translator dead-letters the row as class_mapping_missing. This
// pins the typed §4.9.2 catalog to the OCSF mapping table bidirectionally.
func TestCredentialAuditEventsAllMap_F491(t *testing.T) {
	// The three security-salient events map to Application Security
	// Finding (2004); the rest are credential lifecycle events that map
	// to Authentication (3002).
	finding := map[string]bool{
		"credential.rotation_ceiling_hit":               true,
		"credential.lease_spiffe_mismatch":              true,
		"credential.proxy_mode_spiffe_binding_disabled": true,
	}
	for _, et := range credential.AllCredentialAuditEventTypes() {
		m, ok := LookupClass(et.String())
		if !ok {
			t.Errorf("credential audit event %q has no OCSF class mapping", et)
			continue
		}
		want := ClassAuthentication
		if finding[et.String()] {
			want = ClassAppSecurityFinding
		}
		if m.ClassUID != want {
			t.Errorf("%q mapped to class %d, want %d", et, m.ClassUID, want)
		}
	}
}

// spec: §4.9.2 — the pre-§4.9.2 OCSF misnames are gone. There is no
// `credential.` prefix in the fallback table, so a misnamed credential
// event resolves to nothing (the translator dead-letters it), which is
// the intended signal that an emitter used a non-catalog name.
func TestRetiredCredentialNamesDoNotResolve_F491(t *testing.T) {
	for _, retired := range []string{
		"credential.lease_revoked",
		"credential.lease_renewed",
		"credential.pool_exhausted",
	} {
		if _, ok := LookupClass(retired); ok {
			t.Errorf("retired credential event name %q still resolves; it must not", retired)
		}
	}
}

// The §4.9.2 events that F-4.9.2 (revoked/re_enabled) and F-4.7.7
// (rotation_ceiling_hit) already emit must resolve, since otherwise
// those live emissions dead-letter at translation time.
func TestAlreadyEmittedCredentialEventsResolve_F491(t *testing.T) {
	for _, et := range []string{
		"credential.revoked",
		"credential.re_enabled",
		"credential.rotation_ceiling_hit",
	} {
		if _, ok := LookupClass(et); !ok {
			t.Fatalf("emitted credential event %q does not resolve to an OCSF class", et)
		}
	}
}
