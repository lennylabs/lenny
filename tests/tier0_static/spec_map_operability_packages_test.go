// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// readSpecMapExceptedSections returns the set of spec-section ids listed
// in tests/spec-map-exceptions.yaml. A section listed there is exempt as
// a whole (its feature is deferred or non-normative), so its packages
// may name a directory that a later phase ships.
func readSpecMapExceptedSections(t *testing.T, root string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "tests", "spec-map-exceptions.yaml"))
	if err != nil {
		t.Fatalf("read spec-map-exceptions.yaml: %v", err)
	}
	var doc struct {
		Exceptions []struct {
			Section string `yaml:"section"`
		} `yaml:"exceptions"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse spec-map-exceptions.yaml: %v", err)
	}
	out := map[string]bool{}
	for _, e := range doc.Exceptions {
		out[e.Section] = true
	}
	return out
}

// readSpecMapPendingPaths returns the set of repo-relative paths listed
// in tests/spec-map-pending.txt. A path listed there is a reference
// committed ahead of the file, or a known stale reference awaiting
// repair under a separately tracked finding, and is tolerated by the
// existence guards.
func readSpecMapPendingPaths(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	body, err := os.ReadFile(filepath.Join(root, "tests", "spec-map-pending.txt"))
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatalf("read spec-map-pending.txt: %v", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out
}

// readSpecMapPackages returns, per section id, the `packages` list
// recorded in tests/spec-map.json.
func readSpecMapPackages(t *testing.T) map[string][]string {
	t.Helper()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "tests", "spec-map.json"))
	if err != nil {
		t.Fatalf("read spec-map.json: %v", err)
	}
	var doc struct {
		Sections map[string]struct {
			Packages []string `json:"packages"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse spec-map.json: %v", err)
	}
	out := map[string][]string{}
	for id, sec := range doc.Sections {
		out[id] = sec.Packages
	}
	return out
}

// readSpecMapBlockedUntilPhase returns, per section id, the
// `blocked_until_phase` value recorded in tests/spec-map.json, or the
// empty string when the section carries none.
func readSpecMapBlockedUntilPhase(t *testing.T) map[string]string {
	t.Helper()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "tests", "spec-map.json"))
	if err != nil {
		t.Fatalf("read spec-map.json: %v", err)
	}
	var doc struct {
		Sections map[string]struct {
			BlockedUntilPhase string `json:"blocked_until_phase"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse spec-map.json: %v", err)
	}
	out := map[string]string{}
	for id, sec := range doc.Sections {
		out[id] = sec.BlockedUntilPhase
	}
	return out
}

// readSpecMapTests returns, per section id, the `tests` list recorded
// in tests/spec-map.json, with any trailing `::TestName` selector
// stripped so the result is a set of repo-relative file paths.
func readSpecMapTests(t *testing.T) map[string][]string {
	t.Helper()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "tests", "spec-map.json"))
	if err != nil {
		t.Fatalf("read spec-map.json: %v", err)
	}
	var doc struct {
		Sections map[string]struct {
			Tests []string `json:"tests"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse spec-map.json: %v", err)
	}
	out := map[string][]string{}
	for id, sec := range doc.Sections {
		paths := make([]string, len(sec.Tests))
		for i, entry := range sec.Tests {
			if idx := strings.Index(entry, "::"); idx >= 0 {
				entry = entry[:idx]
			}
			paths[i] = entry
		}
		out[id] = paths
	}
	return out
}

// spec: 25.2 (agent operability architecture overview; the operability
//
//	surface is implemented under pkg/ops, split between the gateway and
//	the lenny-ops service)
//
// diagnosis: A spec-map `packages` entry names the pkg/operability/*
//
//	tree, which does not exist on disk: the §25 operability code lives
//	under pkg/ops/* (pkg/ops/events, pkg/ops/opsaudit, pkg/ops/driftservice,
//	pkg/ops/backup, pkg/ops/mcp, pkg/ops/conventions). A stale
//	pkg/operability reference makes the coverage and impact tooling that
//	reads the `packages` field point at a nonexistent package, so it can
//	neither report coverage for the real package nor tie an edit of it
//	back to its spec section. Repoint the entry at the real pkg/ops/*
//	package.
func TestSpecMapHasNoOperabilityPackageReferences(t *testing.T) {
	t.Parallel()

	stale := []string{}
	for id, pkgs := range readSpecMapPackages(t) {
		for _, p := range pkgs {
			// pkg/operability and pkg/operability/* are the retired
			// names. pkg/gateway/operability is a real package and is
			// intentionally not matched.
			if p == "pkg/operability" || strings.HasPrefix(p, "pkg/operability/") {
				stale = append(stale, id+" → "+p)
			}
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("spec-map.json references the retired pkg/operability tree, which does not exist on disk (the operability surface lives under pkg/ops/*): %s",
			strings.Join(stale, "; "))
	}
}

// spec: 25.2 (agent operability architecture overview; every §25 spec
//
//	section names the pkg/ops package that implements it)
//
// diagnosis: A pkg/ops/* package that the §25 spec-map sections name as
//
//	their implementation is absent from disk. The operability sections
//	map their packages field at pkg/ops/conventions (§25.2 API
//	conventions envelope), pkg/ops/events (§25.5), pkg/ops/opsaudit
//	(§25.9), pkg/ops/driftservice (§25.10), pkg/ops/backup (§25.11), and
//	pkg/ops/mcp (§25.7/§25.12). When one of these directories is missing,
//	the spec-map entry has drifted from the tree (a rename or typo) and
//	the coverage/impact tooling keyed on it resolves nothing. Realign the
//	entry with the package on disk.
func TestOperabilityImplementationPackagesExist(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	// The canonical pkg/ops packages the §25 operability sections are
	// implemented in. Each must exist as a directory on disk.
	want := []string{
		"pkg/ops/conventions",
		"pkg/ops/events",
		"pkg/ops/opsaudit",
		"pkg/ops/driftservice",
		"pkg/ops/backup",
		"pkg/ops/mcp",
	}
	for _, p := range want {
		info, err := os.Stat(filepath.Join(root, p))
		if err != nil || !info.IsDir() {
			t.Errorf("operability implementation package %q does not exist as a directory on disk: %v", p, err)
		}
	}
}

// spec: 25.2 (architecture overview: the operability surface splits
//
//	between gateway-side endpoints and the lenny-ops service, each
//	implemented in a real pkg/gateway or pkg/ops package), 25.3
//	(gateway-side ops endpoints)
//
// diagnosis: A §25 spec-map `packages` entry names a directory that does
//
//	not exist on disk. TESTING.md defines tests/spec-map.json as mapping
//	"every spec section to the tests, packages, migrations, and chart
//	templates that encode it"; a packages entry that resolves to no
//	directory encodes nothing, so the coverage and impact tooling keyed
//	on the packages field points at a nonexistent package and can
//	neither report its coverage nor tie an edit of it back to its spec
//	section. The lenny-test validate-maps test-file existence guard
//	probes only the tests[] .go references, so a stale packages[] path
//	escapes it. Every packages entry under a non-exempt §25 section must
//	resolve to a real directory (gateway-side ops under
//	pkg/gateway/operability/*, the lenny-ops service under pkg/ops/*);
//	repoint a dangling entry at the package on disk.
func TestOperabilityPackageReferencesResolveOnDisk(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	excepted := readSpecMapExceptedSections(t, root)
	pending := readSpecMapPendingPaths(t, root)

	dangling := []string{}
	for id, pkgs := range readSpecMapPackages(t) {
		// Scope: the §25 agent-operability sections this guard owns.
		if id != "25" && !strings.HasPrefix(id, "25.") {
			continue
		}
		// A section exempt as a whole (deferred or non-normative) may
		// name a package a later phase ships; do not probe it.
		if excepted[id] {
			continue
		}
		for _, p := range pkgs {
			// A path committed ahead of the code, or a known stale
			// reference tracked separately, is tolerated.
			if pending[p] {
				continue
			}
			info, err := os.Stat(filepath.Join(root, p))
			if err != nil || !info.IsDir() {
				dangling = append(dangling, id+" → "+p)
			}
		}
	}
	sort.Strings(dangling)
	if len(dangling) > 0 {
		t.Errorf("spec-map.json §25 `packages` entries point at directories that do not exist on disk "+
			"(gateway-side ops lives under pkg/gateway/operability/*, the lenny-ops service under pkg/ops/*): %s",
			strings.Join(dangling, "; "))
	}
}

// spec: TESTING.md §5 ("tests/spec-map.json maps every spec section to
//
//	the tests, packages, migrations, and chart templates that encode
//	it"); 25.9 ("GET /v1/admin/audit-events" paginated query, plus the
//	single-event, summary, retranslate, republish, and partition-drop
//	endpoints of the Audit Log Query API)
//
// diagnosis: pkg/gateway/externalapi/admin implements every §25.9
//
//	endpoint (handleListAuditEvents, handleGetAuditEvent,
//	handleAuditSummary, the retranslate/republish handlers, and the
//	cross-tenant scatter-gather path). The §25.9 spec-map entry's
//	tests[] already references pkg/gateway/externalapi/admin/
//	audit_query_test.go and its siblings, but packages[] named only
//	pkg/ops/opsaudit (the §11.7 audit-append funnel that feeds the log,
//	not the §25.9 query surface), so coverage/impact tooling keyed on
//	packages[] could not tie an edit of the query implementation back
//	to §25.9.
func TestSpecMapAuditQueryPackagesIncludeAdminImplementation(t *testing.T) {
	t.Parallel()

	pkgs := readSpecMapPackages(t)["25.9"]
	if !contains(pkgs, "pkg/gateway/externalapi/admin") {
		t.Errorf("spec-map.json §25.9 packages %v does not include pkg/gateway/externalapi/admin, "+
			"which implements the GET /v1/admin/audit-events query API", pkgs)
	}
}

// spec: TESTING.md §5 ("tests/spec-map.json maps every spec section to
//
//	the tests, packages, migrations, and chart templates that encode
//	it"); 25.10 ("GET /v1/admin/drift compares... Running state...
//	Desired state — read from bootstrap_seed_snapshot table in
//	Postgres")
//
// diagnosis: pkg/drift implements the §25.10 field-by-field desired-
//
//	vs-running diff and severity classification, and
//	pkg/ops/driftservice/pgstore implements the Postgres-backed
//	bootstrap_seed_snapshot store the "Snapshot Validation" and "When
//	the snapshot is updated" subsections describe. The §25.10 spec-map
//	entry's tests[] already references pkg/drift/drift_test.go, but
//	packages[] omitted both pkg/drift and pkg/ops/driftservice/pgstore,
//	so coverage/impact tooling keyed on packages[] could not tie an
//	edit of the diff engine or the snapshot store back to §25.10.
func TestSpecMapDriftPackagesIncludeDiffAndSnapshotStore(t *testing.T) {
	t.Parallel()

	pkgs := readSpecMapPackages(t)["25.10"]
	for _, want := range []string{"pkg/drift", "pkg/ops/driftservice/pgstore"} {
		if !contains(pkgs, want) {
			t.Errorf("spec-map.json §25.10 packages %v does not include %s", pkgs, want)
		}
	}
}

// spec: 25.11 ("The backup pipeline has two surfaces: a Postgres/config
//
//	archive pipeline ... and a continuous ArtifactStore replication
//	pipeline (MinIO workspace bucket replicated to an off-cluster
//	destination — see ArtifactStore Backup below)." and "#### ArtifactStore
//	Backup (MinIO workspace bucket replication)", spec/25_agent-
//	operability.md); TESTING.md §5 ("tests/spec-map.json maps every spec
//	section to the tests, packages, migrations, and chart templates that
//	encode it")
//
// diagnosis: §25.11 names two surfaces: the Postgres/config archive
//
//	pipeline (implemented across pkg/ops/backup and its runner, pgstore,
//	and k8slauncher subpackages — each subpackage is a distinct Go
//	package, so coverage/impact tooling keyed on packages[] does not
//	walk into a subdirectory unless it is listed explicitly) and the
//	continuous ArtifactStore replication pipeline (implemented in
//	pkg/blobstore/replication and the pkg/gateway/externalapi/admin
//	artifact-replication resume endpoint). The §25.11 packages[] entry
//	previously named only pkg/ops/backup, so an edit to the runner,
//	pgstore, k8slauncher, replication, or admin packages could not be
//	tied back to §25.11 by the coverage/impact tooling.
func TestSpecMapBackupPackagesIncludeSubpackagesAndReplication(t *testing.T) {
	t.Parallel()

	pkgs := readSpecMapPackages(t)["25.11"]
	for _, want := range []string{
		"pkg/ops/backup",
		"pkg/ops/backup/runner",
		"pkg/ops/backup/pgstore",
		"pkg/ops/backup/k8slauncher",
		"pkg/blobstore/replication",
		"pkg/gateway/externalapi/admin",
	} {
		if !contains(pkgs, want) {
			t.Errorf("spec-map.json §25.11 packages %v does not include %s", pkgs, want)
		}
	}
}

// spec: 25.11 (Backup Execution, Restore Execution, Storage, and
//
//	ArtifactStore Backup subsections; spec/25_agent-operability.md);
//	TESTING.md §5 (spec-map maps sections to the tests that encode them)
//
// diagnosis: The substantive §25.11 coverage lives outside the single
//
//	tests[] entry (pkg/ops/backup/backup_test.go) the spec-map
//	previously recorded: the restore, post-restore, region-selection,
//	and size-estimate paths (restore_test.go, postrestore_test.go,
//	region_test.go, estimate_test.go), the Postgres-backed job store and
//	the Kubernetes Job launcher (pgstore/pgstore_test.go,
//	k8slauncher/k8slauncher_test.go), the lenny-backup runner's dump,
//	upload, redaction, and restore-test logic (the runner/*_test.go
//	files), the lenny-ops HTTP handlers (opsserver/backup_test.go), and
//	the continuous ArtifactStore replication surface
//	(artifact_replication_test.go, replication_test.go,
//	replication_wiring_test.go). A reader or tool relying on tests[]
//	alone was misled about what §25.11 coverage exists on disk.
func TestSpecMapBackupTestsIncludeRestoreAndReplicationFiles(t *testing.T) {
	t.Parallel()

	tests := readSpecMapTests(t)["25.11"]
	for _, want := range []string{
		"pkg/ops/backup/restore_test.go",
		"pkg/ops/backup/postrestore_test.go",
		"pkg/ops/backup/region_test.go",
		"pkg/ops/backup/estimate_test.go",
		"pkg/ops/backup/pgstore/pgstore_test.go",
		"pkg/ops/backup/k8slauncher/k8slauncher_test.go",
		"pkg/ops/backup/runner/exec_test.go",
		"pkg/ops/backup/runner/export_test.go",
		"pkg/ops/backup/runner/redact_exec_test.go",
		"pkg/ops/backup/runner/restoretest_test.go",
		"pkg/ops/opsserver/backup_test.go",
		"pkg/gateway/externalapi/admin/artifact_replication_test.go",
		"pkg/blobstore/replication/replication_test.go",
		"cmd/lenny-gateway/replication_wiring_test.go",
	} {
		if !contains(tests, want) {
			t.Errorf("spec-map.json §25.11 tests %v does not include %s", tests, want)
		}
	}
}

// spec: 25.12 ("The `ManagementMCPAdapter` lives in `lenny-ops` at
//
//	`/mcp/management` on port 8090." spec/25_agent-operability.md); tests/README.md
//	("`spec-map-exceptions.yaml` | Spec sections explicitly exempt from the
//	'every section has at least one test' rule, with justifications.")
//
// diagnosis: tests/spec-map-exceptions.yaml lists "25.12" with reason
//
//	"deferred" and justification "The MCP management server is built in
//	Wave 4 (Phase 13)." pkg/ops/mcp implements the ManagementMCPAdapter
//	the spec cites, pkg/ops/opsserver/mcp.go wires it into lenny-ops, and
//	the §25.12 spec-map entry already carries eighteen tests[] references
//	spanning tier9 authz/isolation, tier5 e2e, tier4 integration, tier8
//	chaos, and unit coverage of the tool inventory, correlation, and role
//	gate. The exceptions file exists only to waive the "every section has
//	at least one test" rule for a section that has none; §25.12 already
//	satisfies that rule on its own tests[] entry, so the exception is both
//	unnecessary and factually wrong about the section being deferred. A
//	reader or tool trusting the exceptions file's "deferred" claim is
//	misled into thinking §25.12 has zero coverage and skipping it, when
//	the map's own tests[] array (and the packages[] directory) says
//	otherwise. The remaining unbuilt slices of §25.12 (gateway-owned tool
//	routing via GatewayClient, and the notifications/subscribe streaming
//	transport) are already tracked precisely by t.Skip scaffolds carrying
//	their own §25.12 citations (tests/tier4_integration/
//	mcp_management_event_subscription_test.go), so a section-wide
//	deferral exception is not needed to account for them.
func TestSpecMapExceptionsDoesNotDeferShippedMCPManagementServer(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	excepted := readSpecMapExceptedSections(t, root)
	if excepted["25.12"] {
		t.Errorf("tests/spec-map-exceptions.yaml lists §25.12 as exempt from the " +
			"\"every section has at least one test\" rule, but the section already " +
			"carries real tests[] entries in spec-map.json and pkg/ops/mcp implements " +
			"the ManagementMCPAdapter the spec describes; remove the §25.12 entry")
	}

	tests := readSpecMapTests(t)["25.12"]
	if len(tests) == 0 {
		t.Errorf("spec-map.json §25.12 has no tests[] entries; the ManagementMCPAdapter " +
			"spec/25_agent-operability.md describes is implemented in pkg/ops/mcp and " +
			"pkg/ops/opsserver, so the section should carry real test references rather " +
			"than relying on a spec-map-exceptions.yaml deferral")
	}
}

// spec: tests/README.md ("`spec-map-exceptions.yaml` | Spec sections
//
//	explicitly exempt from the 'every section has at least one test'
//	rule, with justifications.")
//
// diagnosis: tests/spec-map-exceptions.yaml lists "25.5", "25.8", and
//
//	"25.13" with reason "deferred", using the same "is built in Wave 4
//	(Phase 13)" justification pattern the §25.12 exception used before
//	it was removed. Each of the three sections already carries real
//	tests[] entries in spec-map.json (§25.5 has 23, §25.8 has 37, §25.13
//	has 8) exercising an implementation package that is wired into a
//	real binary: pkg/ops/events, pkg/ops/eventsubscription (+ pgstore),
//	pkg/ops/opsservice, and pkg/webhookdelivery for §25.5;
//	pkg/ops/upgradeservice and pkg/ops/configservice for §25.8;
//	pkg/alerting/inproceval for §25.13. The exceptions file exists only
//	to waive the "every section has at least one test" rule for a
//	section that has none; all three sections already satisfy that rule
//	on their own tests[] entries, so each exception is both unnecessary
//	and factually wrong about the section being wholly deferred. §25.5
//	does still have one genuinely unbuilt slice — the read-side Redis
//	stream source and the Redis-down/gateway-down degradation matrix
//	(pkg/ops/events package doc, "F-25.5.1 / F-25.5.14") — but, exactly
//	like the remaining unbuilt slices of §25.12, that gap is already
//	tracked precisely by a t.Skip scaffold carrying its own §25.5
//	citation (tests/tier5_e2e_kind/event_buffer_fanout_test.go), so a
//	section-wide deferral exception is not needed to account for it
//	either.
func TestSpecMapExceptionsDoesNotDeferShippedOperabilitySections(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	excepted := readSpecMapExceptedSections(t, root)
	tests := readSpecMapTests(t)

	for _, section := range []string{"25.5", "25.8", "25.13"} {
		if excepted[section] {
			t.Errorf("tests/spec-map-exceptions.yaml lists §%s as exempt from the "+
				"\"every section has at least one test\" rule, but the section already "+
				"carries real tests[] entries in spec-map.json against a shipped "+
				"implementation package; remove the §%s entry", section, section)
		}
		if len(tests[section]) == 0 {
			t.Errorf("spec-map.json §%s has no tests[] entries; if the section is genuinely "+
				"unbuilt it should keep its spec-map-exceptions.yaml deferral, but that "+
				"deferral should not coexist with claiming the section has zero coverage "+
				"while also being described as shipped", section)
		}
	}
}

// spec: TESTING.md §5 ("tests/spec-map.json maps every spec section to
//
//	the tests, packages, migrations, and chart templates that encode
//	it")
//
// diagnosis: For each of §25.3, §25.4, §25.6, §25.7, §25.8, §25.11,
//
//	§25.12, and §25.13, the section's own tests[] array already
//	references pkg/*_test.go files that implement the section, but the
//	packages[] array omits the directory one or more of those files
//	live in — the same drift pattern TestSpecMapAuditQueryPackagesIncludeAdminImplementation
//	and TestSpecMapDriftPackagesIncludeDiffAndSnapshotStore fixed for
//	§25.9 and §25.10. A packages[] entry missing an implementation
//	directory means coverage/impact tooling keyed on packages[] cannot
//	tie an edit of that directory back to its spec section. This
//	computes, for each of these sections, the set of pkg/ directories
//	its own tests[] references and asserts every one of them also
//	appears in that section's packages[].
func TestSpecMapPackagesIncludeEveryTestReferencedPackage(t *testing.T) {
	t.Parallel()

	sections := []string{"25.3", "25.4", "25.6", "25.7", "25.8", "25.11", "25.12", "25.13"}
	packages := readSpecMapPackages(t)
	tests := readSpecMapTests(t)

	for _, id := range sections {
		referenced := map[string]bool{}
		for _, test := range tests[id] {
			if !strings.HasPrefix(test, "pkg/") {
				continue
			}
			referenced[filepath.Dir(test)] = true
		}
		pkgs := packages[id]
		missing := []string{}
		for p := range referenced {
			if !contains(pkgs, p) {
				missing = append(missing, p)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("spec-map.json §%s packages %v is missing implementation package(s) its own tests[] already references: %s",
				id, pkgs, strings.Join(missing, ", "))
		}
	}
}

// spec: TESTING.md §5 ("`blocked_until_phase` records when the section
//
//	is testable. Earlier phases skip its tests with a
//	`not-yet-applicable` reason rather than failing.")
//
// diagnosis: §25.4 (The lenny-ops Service), §25.6 (Diagnostic
//
//	Endpoints), §25.7 (Operational Runbooks), §25.8 (Platform Lifecycle
//	Management), §25.11 (Backup and Restore API), §25.12 (MCP
//	Management Server), and §25.13 (Bundled Alerting Rules) each carry
//	a `blocked_until_phase` even though every one is already testable:
//	each section's implementation package (pkg/ops/opsserver,
//	pkg/ops/diagnostics, pkg/ops/runbooks, pkg/ops/upgradeservice +
//	pkg/ops/configservice, pkg/ops/backup, pkg/ops/mcp, and
//	pkg/alerting/inproceval respectively) is wired into a real binary
//	(cmd/lenny-ops or cmd/lenny-gateway) and none of its tests is a
//	`not-yet-applicable`/`phase-gated` scaffold. Each section's own
//	tests[] array already carries a tier5_e2e_kind, tier6_e2e_cloud, or
//	tier8_chaos entry driving the feature against a deployed binary or
//	real dependency-down scenario (for example
//	tests/tier5_e2e_kind/diagnostics_fix_test.go for §25.6,
//	tests/tier5_e2e_kind/runbook_index_test.go for §25.7,
//	tests/tier9_security/release_channel_signature_test.go and
//	tests/tier10_conformance/release_channel_manifest_test.go for
//	§25.8, tests/tier5_e2e_kind/backup_test.go for §25.11,
//	tests/tier5_e2e_kind/mcp_management_e2e_test.go for §25.12, and
//	tests/tier4_integration/alerting_rules_prometheus_firing_test.go for
//	§25.13), so a `blocked_until_phase` on any of these sections
//	misrepresents the section as not-yet-testable to a reader or to the
//	coverage tooling, mirroring the §25.9/§25.10 precedent
//	(TestSpecMapAuditQueryPackagesIncludeAdminImplementation /
//	TestSpecMapDriftPackagesIncludeDiffAndSnapshotStore) that dropped the
//	same stale field once each section's packages[] was backfilled.
func TestSpecMapShippedOperabilitySectionsCarryNoBlockedUntilPhase(t *testing.T) {
	t.Parallel()

	shipped := []string{"25.4", "25.6", "25.7", "25.8", "25.11", "25.12", "25.13"}
	phases := readSpecMapBlockedUntilPhase(t)
	stale := []string{}
	for _, id := range shipped {
		if phase := phases[id]; phase != "" {
			stale = append(stale, id+" → \""+phase+"\"")
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("spec-map.json carries a stale blocked_until_phase for §25 sections whose "+
			"implementation is already shipped and reachable through a deployed binary: %s",
			strings.Join(stale, "; "))
	}
}
