// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the §5.2 `vm-restart` scrub profile
// reconciliation to retire-and-reprovision (proposal 0034, F-5.2.32). The
// reconciled §5.2 step 7 states that a `vm-restart` pool is retired at the
// occupancy-zero recycle boundary and the gateway provisions a fresh
// replacement pod, which is a fresh guest VM, from the warm pool. There is no
// in-guest guest restart: host sharing is forbidden, egress is default-deny,
// and a full guest restart would destroy the driving process.
//
// Three reader-facing doc locations previously asserted an in-guest guest
// restart for `vm-restart`, which contradicts the reconciled spec:
//   - docs/reference/configuration.md scrubProfile row ("restarts the guest VM
//     between tenants"),
//   - docs/operator-guide/security-principles.md ("cross-tenant microvm reuse
//     without a guest restart"),
//   - docs/operator-guide/multi-tenancy.md ("where the guest VM is restarted
//     between tenants").
//
// This test pins the corrected retire-and-reprovision framing at each site and
// asserts the pre-fix in-guest-restart phrasings are absent, so a later edit
// cannot silently reintroduce the contradiction. It also confirms the YAML
// scrubProfile comments and the scrubPolicy value enumerations keep the
// `vm-restart` value name, which the reconciliation leaves unchanged.
//
// The test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 5.2 (vm-restart fresh-guest reprovision; no in-guest restart), 13.1
// (cross-tenant residual-state boundary).

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// prohibitedInGuestRestartPhrases are the pre-fix prose strings that asserted an
// in-guest or in-place guest restart for `vm-restart`. The reconciled §5.2
// forecloses the in-guest restart, so none of these may appear in reader-facing
// docs. Each is the exact phrase the fix removed. spec: §5.2, §13.1.
var prohibitedInGuestRestartPhrases = []string{
	"restarts the guest VM between tenants",
	"where the guest VM is restarted between tenants",
	"cross-tenant microvm reuse without a guest restart",
}

// vmRestartDocPages are the reader-facing pages the reconciliation touches for
// the `vm-restart` framing. The prohibited in-guest-restart phrasings must be
// absent from every one.
var vmRestartDocPages = []string{
	"docs/reference/configuration.md",
	"docs/operator-guide/security-principles.md",
	"docs/operator-guide/multi-tenancy.md",
}

// spec: 5.2, 13.1
// diagnosis: A reader-facing doc page asserts an in-guest guest restart for the
//
//	`vm-restart` scrub profile, which the reconciled §5.2 (proposal 0034,
//	F-5.2.32) forecloses. Step 7 is retire-and-reprovision: the pod is retired
//	at the recycle boundary and the gateway provisions a fresh replacement pod
//	(a fresh guest VM) from the warm pool; the in-guest adapter neither performs
//	nor requests a restart. A failure here means a doc page reintroduced the
//	pre-fix "restarts the guest VM" / "guest restart" framing, telling an
//	operator a mechanism the platform does not implement.
func TestVMRestartDocsNoInGuestRestart_F5232(t *testing.T) {
	root := repoRoot(t)
	for _, page := range vmRestartDocPages {
		body := readDocPage(t, filepath.Join(root, page))
		for _, phrase := range prohibitedInGuestRestartPhrases {
			if strings.Contains(body, phrase) {
				t.Errorf("%s contains the foreclosed in-guest-restart phrase %q; the reconciled §5.2 retires the pod and reprovisions a fresh guest from the warm pool rather than restarting the guest in place", page, phrase)
			}
		}
	}
}

// spec: 5.2, 13.1
// diagnosis: A reader-facing doc page describes the `vm-restart` scrub profile
//
//	but omits the retire-and-reprovision mechanism the reconciled §5.2 defines.
//	Each site that describes `vm-restart` behavior must state that the pod is
//	retired and a fresh replacement pod (a fresh guest) is provisioned from the
//	warm pool. A failure here means a doc page dropped the corrected framing,
//	leaving the operator without the actual mechanism after the pre-fix
//	in-guest-restart wording was removed.
func TestVMRestartDocsStateRetireAndReprovision_F5232(t *testing.T) {
	root := repoRoot(t)

	// Each site is checked on the single line/row that describes `vm-restart`
	// behavior, so a match elsewhere on the page does not mask a drift in the
	// line itself. Every required substring must appear on that line.
	sites := []struct {
		page    string
		anchor  string
		require []string
	}{
		{
			// The scrubProfile configuration-reference row.
			page:   "docs/reference/configuration.md",
			anchor: "`sessionPolicy.recycle.scrubProfile`",
			require: []string{
				"`vm-restart` retires the pod",
				"fresh replacement pod",
				"warm pool",
				// The value enumeration is unchanged.
				"One of `standard`, `vm-restart`, `in-place`.",
			},
		},
		{
			// The security-principles in-place acknowledgment bullet, which now
			// also states the vm-restart contrast.
			page:   "docs/operator-guide/security-principles.md",
			anchor: "`recycle.scrubProfile: in-place`",
			require: []string{
				"reuses the continuing microvm guest",
				"`recycle.acknowledgeMicrovmResidualState: true`",
				"`vm-restart`",
				"retires the pod",
				"fresh replacement pod",
			},
		},
		{
			// The multi-tenancy cross-tenant-reuse paragraph.
			page:   "docs/operator-guide/multi-tenancy.md",
			anchor: "Cross-tenant reuse is permitted only on the sequential-reuse path",
			require: []string{
				"`vm-restart`",
				"retired at the recycle boundary",
				"fresh replacement pod",
				"warm pool",
			},
		},
	}

	for _, site := range sites {
		body := readDocPage(t, filepath.Join(root, site.page))
		line := lineContaining(body, site.anchor)
		if line == "" {
			t.Errorf("%s: no line contains anchor %q (renamed or removed?)", site.page, site.anchor)
			continue
		}
		for _, want := range site.require {
			if !strings.Contains(line, want) {
				t.Errorf("%s: the %q line does not state %q; the reconciled §5.2 retire-and-reprovision framing must be present at this site", site.page, site.anchor, want)
			}
		}
	}
}

// spec: 5.2
// diagnosis: The `vm-restart` value name was dropped from a YAML scrubProfile
//
//	comment or a scrubPolicy value enumeration. Proposal 0034 reconciles the
//	§5.2 mechanism but keeps the `vm-restart` value name in the config schema
//	and the scrubPolicy API-field enumerations. A failure here means the
//	reconciliation removed the value name where it must persist, breaking the
//	config vocabulary an operator or client depends on.
func TestVMRestartValueNamePersists_F5232(t *testing.T) {
	root := repoRoot(t)

	// Sites where the `vm-restart` value name must remain: YAML scrubProfile
	// comments and scrubPolicy value enumerations. The reconciliation renames the
	// mechanism, not the value.
	valueNameSites := []struct {
		page   string
		anchor string
	}{
		{"docs/reference/execution-modes.md", "scrubProfile: standard"},
		{"docs/operator-guide/configuration.md", "scrubProfile: standard"},
		{"docs/api/rest.md", "`scrubPolicy`"},
		{"docs/client-guide/session-lifecycle.md", "`scrubPolicy`"},
	}
	for _, site := range valueNameSites {
		body := readDocPage(t, filepath.Join(root, site.page))
		line := lineContaining(body, site.anchor)
		if line == "" {
			t.Errorf("%s: no line contains anchor %q (renamed or removed?)", site.page, site.anchor)
			continue
		}
		if !strings.Contains(line, "vm-restart") {
			t.Errorf("%s: the %q line dropped the `vm-restart` value name; the reconciliation keeps the value name in the config schema and scrubPolicy enumerations", site.page, site.anchor)
		}
	}
}
