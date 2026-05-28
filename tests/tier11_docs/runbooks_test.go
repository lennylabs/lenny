// SPDX-License-Identifier: MIT

// Tier-11 §12.11 #5: every runbook has the documented step format
// and parseable metadata. The conventional runbook layout is the
// Trigger → Diagnosis → Remediation structure documented in
// docs/runbooks/index.md:
//
//   ---
//   layout: default
//   title: "<short-name>"
//   parent: "Runbooks"
//   triggers:
//     - alert: <PromQL alert name>
//       severity: <critical | warning | info>
//   components: [<component-name>, ...]
//   ---
//
//   # <title>
//
//   ## Trigger
//   ...
//
//   ## Diagnosis
//   ...
//
//   ## Remediation
//   ...
//
// This test walks docs/runbooks/, parses each .md file's YAML front
// matter, and asserts the front matter + required sections exist.
// Issues fail the test (§17.7 / Phase 13.5+ promotion).

package tier11_docs_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"gopkg.in/yaml.v3"
)

type runbookTrigger struct {
	Alert    string `yaml:"alert"`
	Severity string `yaml:"severity"`
}

type runbookMetadata struct {
	Title    string           `yaml:"title"`
	Triggers []runbookTrigger `yaml:"triggers"`
}

func parseRunbook(path string) (runbookMetadata, []string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return runbookMetadata{}, nil, err
	}
	front, rest, ok := splitFrontMatter(body)
	if !ok {
		return runbookMetadata{}, nil, errors.New("missing or malformed --- front matter")
	}
	var meta runbookMetadata
	if err := yaml.Unmarshal(front, &meta); err != nil {
		return runbookMetadata{}, nil, err
	}
	sections := []string{}
	for _, line := range strings.Split(string(rest), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") {
			sections = append(sections, strings.TrimSpace(strings.TrimPrefix(t, "## ")))
		}
	}
	return meta, sections, nil
}

// splitFrontMatter parses ---YAML--- front matter at the top of a
// markdown file. Returns (frontMatter, rest, ok).
func splitFrontMatter(body []byte) ([]byte, []byte, bool) {
	const marker = "---\n"
	if !strings.HasPrefix(string(body), marker) {
		return nil, nil, false
	}
	rest := body[len(marker):]
	end := strings.Index(string(rest), "\n---\n")
	if end < 0 {
		return nil, nil, false
	}
	return rest[:end], rest[end+len("\n---\n"):], true
}

// spec: 12.11 #5 (every runbook has the documented format)
// diagnosis: A runbook lacked front matter, required metadata, or
//
//	one of the canonical Trigger / Diagnosis / Remediation
//	sections, or carried a triggers entry without a severity. The
//	catalog is the on-call playbook; missing structure breaks
//	operator runbooks.
func TestRunbookStructure(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "docs", "runbooks")
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		t.Skip("docs/runbooks/ does not exist; nothing to validate (runbooks ship per §17.7)")
	}

	required := []string{"Trigger", "Diagnosis", "Remediation"}

	count := 0
	degraded := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		base := filepath.Base(path)
		if base == "index.md" || base == "README.md" {
			return nil
		}
		count++
		meta, sections, perr := parseRunbook(path)
		if perr != nil {
			t.Errorf("%s: %v", path, perr)
			degraded++
			return nil
		}
		issues := 0
		if meta.Title == "" {
			t.Errorf("%s: missing title in front matter", path)
			issues++
		}
		// triggers: [] is the documented marker for scheduled or
		// procedural runbooks (key rotations, tier promotion). When
		// the list is present and non-empty, each entry must name an
		// alert and a severity per the docs/runbooks/index.md
		// "Alert → runbook map" contract.
		for i, tr := range meta.Triggers {
			if tr.Alert == "" {
				t.Errorf("%s: triggers[%d] missing alert", path, i)
				issues++
			}
			if tr.Severity == "" {
				t.Errorf("%s: triggers[%d] (%s) missing severity", path, i, tr.Alert)
				issues++
			}
		}
		for _, want := range required {
			present := false
			for _, got := range sections {
				if strings.EqualFold(got, want) {
					present = true
					break
				}
			}
			if !present {
				t.Errorf("%s: missing required section %q", path, want)
				issues++
			}
		}
		if issues > 0 {
			degraded++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk runbooks: %v", err)
	}
	if degraded > 0 {
		t.Errorf("validated %d runbook(s); %d carry structural defects", count, degraded)
		return
	}
	if count == 0 {
		t.Errorf("found no runbooks under %s; expected docs/runbooks to be populated", dir)
	}
	t.Logf("validated %d runbook(s); all structurally conformant", count)
}

// knownBrokenRunbookSlugs is the baseline of alert→runbook slug
// mismatches catalogued under F-17.7.4 (and adjacent §17.7 audit
// rows). Each entry is the alert name; the test only fails when a
// new entry — outside this allowlist — appears. The allowlist
// shrinks as F-17.7.4 is remediated.
//
// spec: §17.7 line 850 ("Each runbook references the relevant
// alerts") — the reverse mapping (alert → runbook filename) must
// resolve to an on-disk file under docs/runbooks/. F-17.7.10.
var knownBrokenRunbookSlugs = map[string]bool{
	// F-17.7.4: 27 broken slugs from §17.7 audit + later catalog
	// growth. When F-17.7.4 closes by renaming runbook files or
	// alert slugs, the rows here drop out one-for-one.
	"PostgresReplicationLagHigh":               true,
	"GatewayNoHealthyReplicas":                 true,
	"SessionStoreUnavailable":                  true,
	"T4KmsKeyUnusable":                         true,
	"EtcdUnavailable":                          true,
	"CredentialPoolExhausted":                  true,
	"CredentialCompromised":                    true,
	"ControllerLeaderElectionFailed":           true,
	"DedicatedDNSUnavailable":                  true,
	"NetworkPolicyCIDRDrift":                   true,
	"SandboxClaimGuardUnavailable":             true,
	"DataResidencyWebhookUnavailable":          true,
	"ArtifactReplicationResidencyViolation":    true,
	"LegalHoldEscrowResidencyViolation":        true,
	"PlatformAuditResidencyViolation":          true,
	"PgBouncerAllReplicasDown":                 true,
	"SessionEvictionTotalLoss":                 true,
	"DelegationBudgetKeysExpired":              true,
	"BillingStreamEntryAgeHigh":                true,
	"OTLPPlaintextEgressDetected":              true,
	"OpsAdminAPIPlaintextDetected":             true,
	"AuditRedactionReceiptMissing":             true,
	"LLMUpstreamEgressAnomaly":                 true,
	"BackupReconcileBlocked":                   true,
	"MinIOArtifactReplicationLagCritical":      true,
	"LenniOpsLockSplitBrainDetected":           true,
	"SessionCreationSuccessRateBurnRate":       true,
	"SessionCreationLatencyBurnRate":           true,
	"SessionAvailabilityBurnRate":              true,
	"GatewayAvailabilityBurnRate":              true,
	"StartupLatencyBurnRate":                   true,
	"StartupLatencyGVisorBurnRate":             true,
	"TTFTBurnRate":                             true,
	"CheckpointDurationBurnRate":               true,
}

// TestAlertCatalogRunbookSlugsResolveToDocs walks the §16.5 alert
// catalog and asserts every RunbookURL slug maps to an existing file
// under docs/runbooks/. The §17.7 contract says runbooks are
// "version-controlled alongside the platform code in docs/runbooks/";
// without an automated cross-check, a new alert can ship a
// runbook_url that points at a non-existent file, leaving operators
// with a broken link.
//
// The current baseline of broken slugs is catalogued in
// knownBrokenRunbookSlugs (tracked under F-17.7.4). The test fails
// only when a new alert outside the baseline ships a broken slug,
// or when an alert listed in the baseline silently regains
// (the baseline row should then be removed alongside the fix).
//
// spec: §17.7 line 850 (runbooks in docs/runbooks/);
// pkg/alerting/rules/rules.go runbook() returns
// "https://docs.lenny.dev/runbooks/<slug>". F-17.7.10.
func TestAlertCatalogRunbookSlugsResolveToDocs(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "docs", "runbooks")
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		t.Skip("docs/runbooks/ does not exist; nothing to cross-check (runbooks ship per §17.7)")
	}
	const runbookHostPrefix = "https://docs.lenny.dev/runbooks/"
	newBroken := []string{}
	staleAllowlist := []string{}
	verified := 0
	for _, r := range rules.Catalog() {
		if r.RunbookURL == "" {
			continue
		}
		if !strings.HasPrefix(r.RunbookURL, runbookHostPrefix) {
			t.Errorf("alert %q: runbook URL %q does not use the canonical %s prefix", r.Name, r.RunbookURL, runbookHostPrefix)
			continue
		}
		slug := strings.TrimPrefix(r.RunbookURL, runbookHostPrefix)
		path := filepath.Join(dir, slug+".md")
		_, err := os.Stat(path)
		exists := err == nil
		switch {
		case exists && knownBrokenRunbookSlugs[r.Name]:
			// The allowlist entry is stale — fail so the developer
			// who fixed the slug also drops the row.
			staleAllowlist = append(staleAllowlist, r.Name+" → "+slug+".md (now resolves)")
		case !exists && knownBrokenRunbookSlugs[r.Name]:
			// Expected baseline gap; F-17.7.4 tracks the remediation.
			continue
		case !exists:
			newBroken = append(newBroken, r.Name+" → "+slug+".md")
		default:
			verified++
		}
	}
	for _, n := range newBroken {
		t.Errorf("new alert→runbook slug points at missing file (add the runbook or update knownBrokenRunbookSlugs after closing F-17.7.4): %s", n)
	}
	for _, s := range staleAllowlist {
		t.Errorf("knownBrokenRunbookSlugs allowlist is stale; the slug now resolves and the row should be removed: %s", s)
	}
	t.Logf("verified %d alert→runbook mappings; %d entries in F-17.7.4 baseline still missing", verified, len(knownBrokenRunbookSlugs))
}

// knownBrokenRunbookTriggers is the baseline of runbook trigger →
// alert mismatches (the reverse direction from
// knownBrokenRunbookSlugs). Each entry is the runbook's frontmatter
// trigger alert name; the test only fails when a new entry — outside
// this allowlist — appears. The allowlist shrinks as the underlying
// F-IDs close.
//
// spec: §11.8 deployer guidance pointing at runbooks; §17.7 line 850
// (each runbook references the relevant alerts). F-11.8.2 /
// F-11.8.3 cover the reverse-mapping convention; F-11.8.4 is the
// EmergencyCredentialRevoked orphan; the remaining entries are
// catalog-drift inherited from the broader F-17.7.4 cluster (alert
// names diverged from runbook frontmatter names).
var knownBrokenRunbookTriggers = map[string]bool{
	// F-11.8.4 (Low, OPEN): emergency-credential-revocation.md trigger
	// names an alert that has no §16.5 catalog entry yet; the runbook
	// itself is operator-initiated, not alert-driven, but the
	// frontmatter convention requires a triggers row. Drops when
	// either the alert lands in pkg/alerting/rules/rules.go or the
	// runbook is restructured to remove the trigger.
	"EmergencyCredentialRevoked": true,

	// F-17.7.4 / §17.7-cluster (High, OPEN): runbook trigger names
	// pre-date the §16.5 alert-catalog naming pass. Each row drops
	// when either the alert is added to pkg/alerting/rules/rules.go
	// or the runbook's trigger is renamed to a catalog name. The
	// runbook still serves its operator purpose; the cross-link to
	// the canonical alert is what is stale.
	"AgentLLMProviderPartition":         true,
	"CertificateExpiringSoon":           true,
	"ChildSessionCrashed":               true,
	"CRDImmutableFieldChanged":          true,
	"CRDVersionSkew":                    true,
	"StaleCRDDetected":                  true,
	"EphemeralCredGuardUnreachable":     true,
	"CredentialCompromiseSuspected":     true,
	"CredentialRotationFailed":          true,
	"CrossZoneNetworkPartition":         true,
	"DelegationBudgetExhausted":         true,
	"DelegationDepthDeadlock":           true,
	"DenyListPropagationDegraded":       true,
	"DoubleClaimDetected":               true,
	"ElicitationDeadlockDetected":       true,
	"GatewayPodPartition":               true,
	"KMSKeyProbeStale":                  true,
	"KMSUnavailable":                    true,
	"LeaseExtensionCoolOffPersist":      true,
	"MinIOReplicationLag":               true,
	"NetworkPolicyConfigDrift":          true,
	"NodeDrainDuringMinIOOutage":        true,
	"ParentSessionCrashedAwaiting":      true,
	"AgentPodKilledDuringActiveSession": true,
	"PoolUpgradeRolledBack":             true,
	"PostgresReplicationLag":            true,
	"PostgresUnavailable":               true,
	"RedisClusterDegraded":              true,
	"RedisMasterFailover":               true,
	"SandboxClaimRaceDetected":          true,
	"SandboxFinalizerHang":              true,
	"SchemaMigrationDirtyFlag":          true,
	"SchemaMigrationFailed":             true,
	"SchemaMigrationDirty":              true,
	"T3T4SLABreach":                     true,
	"TokenServiceCircuitOpen":           true,
	"OpsPlaneUnavailable":               true,
}

// TestRunbookTriggersResolveToAlertCatalog walks docs/runbooks/ and
// asserts every `triggers[].alert` value in a runbook's front matter
// names a real alert in pkg/alerting/rules/rules.go Catalog. The
// reverse direction of TestAlertCatalogRunbookSlugsResolveToDocs:
// without it, a runbook can keep a stale trigger name after the
// catalog renames the alert (F-11.8.2 was exactly this defect for
// AuditChainGap → AuditChainGapDetected).
//
// The current orphan baseline is in knownBrokenRunbookTriggers
// (F-11.8.4). The test fails when a new runbook trigger outside the
// baseline names a missing alert, or when a baseline row silently
// regains (rename or alert-add should drop the row).
//
// spec: §11.8 + §17.7 line 850. F-11.8.2 / F-11.8.3.
func TestRunbookTriggersResolveToAlertCatalog(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "docs", "runbooks")
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		t.Skip("docs/runbooks/ does not exist; nothing to cross-check (runbooks ship per §17.7)")
	}
	catalog := map[string]bool{}
	for _, r := range rules.Catalog() {
		catalog[r.Name] = true
	}
	newBroken := []string{}
	staleAllowlist := []string{}
	verified := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		base := filepath.Base(path)
		if base == "index.md" || base == "README.md" {
			return nil
		}
		meta, _, perr := parseRunbook(path)
		if perr != nil {
			return nil // TestRunbookStructure already reports parse failures
		}
		for _, tr := range meta.Triggers {
			if tr.Alert == "" {
				continue
			}
			exists := catalog[tr.Alert]
			switch {
			case exists && knownBrokenRunbookTriggers[tr.Alert]:
				staleAllowlist = append(staleAllowlist, tr.Alert+" in "+base+" (now resolves to catalog)")
			case !exists && knownBrokenRunbookTriggers[tr.Alert]:
				continue
			case !exists:
				newBroken = append(newBroken, tr.Alert+" in "+base)
			default:
				verified++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk runbooks: %v", err)
	}
	for _, n := range newBroken {
		t.Errorf("runbook trigger names alert that is not in pkg/alerting/rules/rules.go Catalog (rename trigger or add alert; spec §11.8 / §17.7): %s", n)
	}
	for _, s := range staleAllowlist {
		t.Errorf("knownBrokenRunbookTriggers allowlist is stale; the alert now resolves and the row should be removed: %s", s)
	}
	t.Logf("verified %d runbook trigger → catalog mappings; %d entries in F-11.8.4 baseline still missing", verified, len(knownBrokenRunbookTriggers))
}
