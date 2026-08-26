// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// slotAddressAbsenceTestFile is the tier-3 file that pins the absence of a
// slot address from the client-facing REST surfaces. Its cases each assert
// one spec section's contract, and tests/spec-map.json must credit each case
// to the sections that case actually exercises. The map validator only checks
// that a reference resolves, so a case filed under an unrelated section is
// invisible to it: this test closes that gap for the file.
const slotAddressAbsenceTestFile = "tests/tier3_contract/rest_sessions/slot_address_absence_test.go"

// creditGateFile is this file, whose own cases the credit gate must account
// for as it accounts for every other file in the inventory.
const creditGateFile = "tests/tier0_static/spec_map_slot_address_registration_test.go"

// specMapFunctionEntries returns, per spec-section id, the test function
// names that tests/spec-map.json registers from the given repo-relative
// file. Entries without a `::TestName` selector name the whole file and
// are not per-case registrations, so they are skipped.
func specMapFunctionEntries(t *testing.T, file string) map[string][]string {
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
		for _, entry := range sec.Tests {
			idx := strings.Index(entry, "::")
			if idx < 0 || entry[:idx] != file {
				continue
			}
			out[id] = append(out[id], entry[idx+2:])
		}
	}
	return out
}

// spec: 5.2 (client error on exhaustion), 7.1 (session lifecycle normal
// flow), 7.2 (message dispatch), 4.1 (message scope)
//
// Every per-case spec-map registration of a slot-address-absence test names
// a section that case's own `// spec:` annotation carries. A registration
// under a section the case does not exercise credits that section with
// coverage no regression in it would break, and leaves the section the case
// does pin unmapped.
func TestSlotAddressAbsenceCasesAreMappedToTheSectionsTheyExercise(t *testing.T) {
	registered := specMapFunctionEntries(t, slotAddressAbsenceTestFile)
	annotated := annotatedSectionsPerCase(t, slotAddressAbsenceTestFile)
	if len(registered) == 0 {
		t.Fatalf("spec-map.json registers no case from %s", slotAddressAbsenceTestFile)
	}
	for section, fns := range registered {
		for _, fn := range fns {
			sections, ok := annotated[fn]
			if !ok {
				t.Errorf("spec-map.json section %s registers %s, which %s does not declare", section, fn, slotAddressAbsenceTestFile)
				continue
			}
			if !sections[section] {
				t.Errorf("spec-map.json section %s registers %s, whose spec annotation names %v instead", section, fn, sortedSectionIDs(sections))
			}
		}
	}
}

// sortedSectionIDs renders a section-id set in a stable order for failure
// messages.
func sortedSectionIDs(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// slotAddressCaseFiles is the inventory of test cases that landed with the
// per-session slot address contract: every test file this change added, and
// every existing test file whose `// spec:` annotation this change extended
// with a further section. Each of them must be credited in tests/spec-map.json
// under every spec section its own annotation names, so that a section the
// case pins has a test against it in the coverage view and
// `lenny-test --spec <section>` selects the case. The list is derived from the
// change rather than chosen by hand: it is the set of test files this change
// added, plus every test file whose `// spec:` citation lines the change
// touched, taken from the diff over the change's own commit range rather than
// assembled by reading. It includes this file, because a
// gate that omits itself does not check the case it is part of. The map
// validator walks tier 2 through tier 10 only, and it checks that a reference
// resolves rather than that a case is credited to the sections it exercises,
// so a tier-0, tier-11, `cmd/`, `pkg/`, `sdks/`, or `migrations/` case can
// carry an annotation no section records.
var slotAddressCaseFiles = []string{
	"cmd/lenny-compliance/full_test.go",
	"cmd/lenny-compliance/sessionecho_test.go",
	"cmd/lenny-ctl/runtimescaffold/scaffold_test.go",
	"cmd/lenny-test/cmd_run_race_test.go",
	"cmd/runtimes/echo-concurrent/main_test.go",
	"migrations/0178_checkpoint_manifest_test.go",
	"migrations/session_checkpoints_slot_id_test.go",
	"pkg/adapter/attach_test.go",
	"pkg/adapter/checkpoint_stream_test.go",
	"pkg/adapter/credentials_test.go",
	"pkg/adapter/credexpiry_test.go",
	"pkg/adapter/exportpaths_test.go",
	"pkg/adapter/holdstate_test.go",
	"pkg/adapter/integrationlevel_test.go",
	"pkg/adapter/manifest_fields_test.go",
	"pkg/adapter/manifest_test.go",
	"pkg/adapter/one_session_only_test.go",
	"pkg/adapter/oplock_test.go",
	"pkg/adapter/podmcp_arming_internal_test.go",
	"pkg/adapter/podscrub_test.go",
	"pkg/adapter/resume_test.go",
	"pkg/adapter/scrub/scrub_test.go",
	"pkg/adapter/session_test.go",
	"pkg/adapter/sessionscrub_emit_test.go",
	"pkg/adapter/slot_test.go",
	"pkg/adapter/slotframe_test.go",
	"pkg/adapter/socketruntime_e2e_test.go",
	"pkg/adapter/staging_test.go",
	"pkg/adapter/tracingcontext_addressing_test.go",
	"pkg/adapter/usage_test.go",
	"pkg/adapter/warmlayout_test.go",
	"pkg/controller/sandbox/podspec/podspec_test.go",
	"pkg/gateway/checkpoint/checkpointer/checkpointer_test.go",
	"pkg/gateway/checkpoint/checkpointer/checkpointstart_test.go",
	"pkg/gateway/checkpoint/checkpointer/uploaddriver_test.go",
	"pkg/gateway/checkpoint/checkpointretention/checkpointretention_test.go",
	"pkg/gateway/checkpoint/partialmanifeststore/partialmanifeststore_test.go",
	"pkg/gateway/podlifecycle/podclaim/reserveslotonpod_test.go",
	"pkg/gateway/podlifecycle/podsession/resume_slot_reservation_test.go",
	"pkg/gateway/podlifecycle/podsession/workspace_base_propagation_test.go",
	"pkg/gateway/runtime/adapterclient/client_test.go",
	"pkg/gateway/session/executor/pod_test.go",
	"pkg/gateway/sessionserver/messages_component_test.go",
	"pkg/gateway/sessionserver/messages_test.go",
	"pkg/gateway/sessionserver/podclaimerror_internal_test.go",
	"pkg/gateway/sessionserver/slotretry_test.go",
	"pkg/gateway/sessionserver/workspace_root_persist_test.go",
	"sdks/runtime/go/runtime/runtime_test.go",
	"tests/testinfra/sessiondriver/sessiondriver_test.go",
	"tests/tier0_static/adapter_proto_message_scope_test.go",
	"tests/tier0_static/adapter_proto_parse_test.go",
	"tests/tier0_static/checkpoint_dropped_slot_column_comment_test.go",
	"tests/tier0_static/checkpoint_scoping_key_comment_test.go",
	"tests/tier0_static/claim_register_generator_test.go",
	"tests/tier0_static/claim_register_test.go",
	"tests/tier0_static/slot_absence_claim_comment_test.go",
	"tests/tier0_static/slot_assignment_attribution_test.go",
	"tests/tier0_static/spec_map_exception_blocker_retention_test.go",
	"tests/tier0_static/spec_map_slot_address_registration_test.go",
	"tests/tier10_conformance/concurrent_slot_conformance_test.go",
	"tests/tier10_conformance/credential_path_resolution_conformance_test.go",
	"tests/tier10_conformance/scaffold_battery_test.go",
	"tests/tier10_conformance/scaffolds_test.go",
	"tests/tier11_docs/adapter_manifest_credentials_path_doc_reconciliation_test.go",
	"tests/tier11_docs/adapter_manifest_rewrite_trigger_doc_test.go",
	"tests/tier11_docs/adapter_manifest_session_identifier_currency_doc_test.go",
	"tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go",
	"tests/tier11_docs/checkpoint_pipeline_consistency_test.go",
	"tests/tier11_docs/checkpoint_scoping_key_test.go",
	"tests/tier11_docs/credential_path_literal_sweep_test.go",
	"tests/tier11_docs/ephemeral_container_cred_guard_path_reconciliation_test.go",
	"tests/tier11_docs/frame_address_spec_citation_test.go",
	"tests/tier11_docs/frame_identifier_schema_reconciliation_test.go",
	"tests/tier11_docs/intra_pod_mcp_nonce_doc_reconciliation_test.go",
	"tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go",
	"tests/tier11_docs/pod_filesystem_layout_doc_reconciliation_test.go",
	"tests/tier11_docs/recycle_scrub_trigger_consistency_test.go",
	"tests/tier11_docs/redis_key_prefix_registry_test.go",
	"tests/tier11_docs/retirement_sweep_surfaces_test.go",
	"tests/tier11_docs/rotation_ceiling_cotenant_reconciliation_test.go",
	"tests/tier11_docs/runtime_page_metric_names_resolve_test.go",
	"tests/tier11_docs/session_policy_table_mirror_reconciliation_test.go",
	"tests/tier11_docs/session_scoped_frame_example_address_doc_reconciliation_test.go",
	"tests/tier11_docs/session_scoped_frame_population_doc_reconciliation_test.go",
	"tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go",
	"tests/tier11_docs/slot_definition_glossary_reconciliation_test.go",
	"tests/tier11_docs/tracing_context_addressing_doc_reconciliation_test.go",
	"tests/tier11_docs/workspace_path_literal_sweep_test.go",
	"tests/tier2_component/migrations/checkpoint_slot_id_drop_test.go",
	"tests/tier2_component/migrations/prod_columns_test.go",
	"tests/tier2_component/rls/checkpoint_manifest_test.go",
	"tests/tier2_component/slotrelease/revoke_double_teardown_test.go",
	"tests/tier2_component/stores/checkpointretention_test.go",
	"tests/tier2_component/stores/partialmanifeststore_test.go",
	"tests/tier2_component/warmlayout/warm_layout_test.go",
	"tests/tier3_contract/adapter_frame_resolution/session_scoped_frame_resolution_test.go",
	"tests/tier3_contract/adapter_jsonl/gateway_envelope_address_test.go",
	"tests/tier3_contract/adapter_jsonl/session_scoped_frame_address_test.go",
	"tests/tier3_contract/adapter_jsonl/subprocess_envelope_address_test.go",
	"tests/tier3_contract/adapter_negotiate/negotiate_workspace_base_wire_test.go",
	"tests/tier3_contract/adapter_reportusage/reportusage_wire_test.go",
	"tests/tier3_contract/adapter_session_address/session_address_wire_test.go",
	"tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go",
	"tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go",
	"tests/tier3_contract/rest_sessions/slot_address_absence_test.go",
	"tests/tier3_contract/sdks/go_client_test.go",
	"tests/tier3_contract/sdks/python_client_test.go",
	"tests/tier3_contract/sdks/typescript_client_test.go",
	"tests/tier4_integration/checkpoint_concurrent_pool_test.go",
	"tests/tier4_integration/concurrent_delegation_proxy_test.go",
	"tests/tier4_integration/concurrent_workspace_test.go",
	"tests/tier4_integration/token_service_unavailability_guard_test.go",
	"tests/tier5_e2e_kind/admission_test.go",
	"tests/tier5_e2e_kind/checkpoint_resume_test.go",
	"tests/tier5_e2e_kind/diagnostics_fix_test.go",
	"tests/tier5_e2e_kind/execution_modes_test.go",
	"tests/tier5_e2e_kind/gateway_probe_test.go",
	"tests/tier5_e2e_kind/gateway_replica_continuity_test.go",
	"tests/tier5_e2e_kind/journey_pool_reclaim_test.go",
	"tests/tier5_e2e_kind/runtime_publish_journey_test.go",
	"tests/tier5_e2e_kind/scaffolds_test.go",
	"tests/tier7a_load_local/coordinator_hold_termination_race_test.go",
	"tests/tier7a_load_local/podmcp_arming_handoff_test.go",
	"tests/tier7a_load_local/podmcp_once_per_pod_start_race_test.go",
	"tests/tier7a_load_local/racestart_testsupport_test.go",
	"tests/tier7a_load_local/shutdown_drain_gate_race_test.go",
	"tests/tier7a_load_local/sole_session_concurrent_release_test.go",
	"tests/tier7a_load_local/tracing_context_release_race_test.go",
	"tests/tier8_chaos/config_drift_test.go",
	"tests/tier8_chaos/credential_rotation_ceiling_test.go",
	"tests/tier8_chaos/live_session_test.go",
	"tests/tier8_chaos/token_service_unavailability_guard_test.go",
	"tests/tier9_security/adapter_hold_termination_surface_test.go",
	"tests/tier9_security/adapter_shared_mcp_surface_test.go",
	"tests/tier9_security/audit_integrity_test.go",
	"tests/tier9_security/audit_sequence_precondition_test.go",
	"tests/tier9_security/live_session_test.go",
	"tests/tier9_security/ops_network_policy_test.go",
	"tests/tier9_security/session_teardown_surface_test.go",
	"tests/tier9_security/tracing_context_session_isolation_test.go",
}

// citedSectionHeadRE splits one item of a `// spec:` annotation into the
// optional section sign, a dotted section id anchored at the head of the item,
// and whatever follows the id inside the item.
var citedSectionHeadRE = regexp.MustCompile(`^(§?)(\d+(?:\.\d+)*)(.*)$`)

// citedSectionAtHead returns the spec-section id an annotation item cites at
// its head. The id must open the item, and what follows it decides whether the
// item is a citation: the section sign, the end of the item, a trailing
// period, colon, or semicolon, the opening parenthesis of a gloss, or, for a
// dotted id, a gloss introduced by whitespace, which is how the repo's
// annotations write an em-dash or bare-word gloss. An undotted number followed
// by prose ("a Phase 3 contract migration", "the latest 2 checkpoints") is a
// quantity rather than a citation and is not read as one.
func citedSectionAtHead(item string) (string, bool) {
	m := citedSectionHeadRE.FindStringSubmatch(strings.TrimSpace(item))
	if m == nil {
		return "", false
	}
	sign, id, rest := m[1], m[2], strings.TrimRight(m[3], " \t")
	switch {
	case sign == "§", rest == "", rest == ".", rest == ":", rest == ";":
		return id, true
	}
	if strings.HasPrefix(strings.TrimLeft(rest, " \t"), "(") {
		return id, true
	}
	if strings.Contains(id, ".") && rest != strings.TrimLeft(rest, " \t") {
		return id, true
	}
	return "", false
}

// isIndentedCommentContinuation reports whether the line at idx is a comment
// line whose text is indented under its `//` marker by a tab or by two or more
// spaces. gofumpt reflows a wrapped `// spec:` annotation into the first
// physical line, a bare `//`, and then tab-indented continuation lines, so an
// indented comment line after a blank one continues the annotation rather than
// opening a new paragraph. An ordinary `// text` prose line carries a single
// space and ends the annotation instead.
func isIndentedCommentContinuation(lines []string, idx int) bool {
	if idx >= len(lines) {
		return false
	}
	trimmed := strings.TrimSpace(lines[idx])
	if !strings.HasPrefix(trimmed, "//") {
		return false
	}
	rest := strings.TrimPrefix(trimmed, "//")
	if strings.TrimSpace(rest) == "" {
		return false
	}
	// A single space after `//` is how an ordinary comment line is written, so
	// only a tab or a wider indent marks a reflowed continuation line.
	return strings.HasPrefix(rest, "\t") || strings.HasPrefix(rest, "  ")
}

// annotationBlockAt joins the `// spec:` annotation opening at lines[i] with
// the comment lines that continue it, and returns the joined text and the
// index of the last line consumed. A blank comment line terminates the block
// unless the comment line after it is indented, which is how gofumpt renders a
// wrapped annotation: dropping the block there would hide every section after
// the first physical line. The block also ends at the `diagnosis:` tag, at a
// following `spec:` tag, and at the end of the comment group.
func annotationBlockAt(lines []string, i int) (string, int) {
	block := []string{strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "// spec:"))}
	j := i + 1
	for ; j < len(lines); j++ {
		next := strings.TrimSpace(lines[j])
		if !strings.HasPrefix(next, "//") {
			break
		}
		next = strings.TrimSpace(strings.TrimPrefix(next, "//"))
		if strings.HasPrefix(next, "diagnosis:") || strings.HasPrefix(next, "spec:") {
			break
		}
		if next == "" {
			if !isIndentedCommentContinuation(lines, j+1) {
				break
			}
			continue
		}
		block = append(block, next)
	}
	return strings.Join(block, " "), j - 1
}

// sectionsInAnnotation returns every spec-section id cited at the head of an
// item of one joined `// spec:` annotation. The repo's annotations separate
// citations with a comma or a semicolon, so both are item separators.
func sectionsInAnnotation(block string, out map[string]bool) {
	for _, item := range strings.FieldsFunc(block, func(r rune) bool { return r == ',' || r == ';' }) {
		if id, ok := citedSectionAtHead(item); ok {
			out[id] = true
		}
	}
}

// annotatedSectionsInFile returns every spec-section id that the `// spec:`
// annotations anywhere in the given file name.
func annotatedSectionsInFile(t *testing.T, file string) map[string]bool {
	t.Helper()
	return sectionsFromLines(repoFileLines(t, file))
}

// sectionsFromLines returns every spec-section id the `// spec:` annotations
// in the given lines name.
func sectionsFromLines(lines []string) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "// spec:") {
			continue
		}
		block, last := annotationBlockAt(lines, i)
		i = last
		sectionsInAnnotation(block, out)
	}
	return out
}

// testFuncRE matches a top-level test function declaration.
var testFuncRE = regexp.MustCompile(`^func (Test\w+)\(`)

// repoFileLines reads a repo-relative file and splits it into lines.
func repoFileLines(t *testing.T, file string) []string {
	t.Helper()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	return strings.Split(string(body), "\n")
}

// annotatedSectionsPerCase returns, per test function declared in the given
// file, the spec-section ids that function's own `// spec:` annotation cites.
// The map registers some files case by case, and a section credited for one
// case says nothing about the section another case in the same file pins, so
// the credit check resolves against the annotation of the case it names.
func annotatedSectionsPerCase(t *testing.T, file string) map[string]map[string]bool {
	t.Helper()
	return perCaseSectionsFromLines(repoFileLines(t, file))
}

// perCaseSectionsFromLines returns, per test function declared in the given
// lines, the spec-section ids that function's own `// spec:` annotation cites.
func perCaseSectionsFromLines(lines []string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	pending := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "// spec:") {
			block, last := annotationBlockAt(lines, i)
			i = last
			sectionsInAnnotation(block, pending)
			continue
		}
		if m := testFuncRE.FindStringSubmatch(lines[i]); m != nil {
			out[m[1]] = pending
			pending = map[string]bool{}
			continue
		}
		// A non-comment, non-blank line that is not a test declaration ends
		// the doc block an annotation opened, so the annotation belongs to
		// whatever it documents rather than to a later test.
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			pending = map[string]bool{}
		}
	}
	return out
}

// specMapEntryCoversFile reports whether one tests/spec-map.json entry
// registers the given repo-relative file. An entry names the whole file, one
// of its cases with a `::TestName` selector, or a package subtree with a
// trailing `/...`, and all three forms credit the file.
func specMapEntryCoversFile(entry, file string) bool {
	if entry == file || strings.HasPrefix(entry, file+"::") {
		return true
	}
	if subtree, ok := strings.CutSuffix(entry, "..."); ok {
		return strings.HasPrefix(file, subtree)
	}
	return false
}

// specMapCredits returns the set of spec-section ids tests/spec-map.json
// declares, the sections under which it registers the given file as a whole,
// and the sections under which it registers each of the file's cases by name.
// A section that names one case credits that case alone, so a file the map
// registers case by case is checked case by case.
func specMapCredits(t *testing.T, file string) (declared, wholeFile map[string]bool, perCase map[string]map[string]bool) {
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
	declared = map[string]bool{}
	wholeFile = map[string]bool{}
	perCase = map[string]map[string]bool{}
	for id, sec := range doc.Sections {
		declared[id] = true
		for _, entry := range sec.Tests {
			if strings.HasPrefix(entry, file+"::") {
				fn := strings.TrimPrefix(entry, file+"::")
				if perCase[fn] == nil {
					perCase[fn] = map[string]bool{}
				}
				perCase[fn][id] = true
				continue
			}
			if specMapEntryCoversFile(entry, file) {
				wholeFile[id] = true
			}
		}
	}
	return declared, wholeFile, perCase
}

// specHeadingNumbers returns every dotted section number the specification
// under spec/ declares as a heading.
//
// An id a `// spec:` annotation cites that this set does not carry names a
// TESTING.md section rather than a specification heading. The tier phase-gate
// groups are cited that way, so §13.7 and §13.19 in the Kind admission and
// scaffold cases name TESTING.md headings, and tests/spec-map.json keys
// specification sections alone. No map key can exist for such an id, so the
// credit gate carves it out rather than demanding one.
func specHeadingNumbers(t *testing.T) map[string]bool {
	t.Helper()
	root := schematest.RepoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "spec"))
	if err != nil {
		t.Fatalf("read spec/: %v", err)
	}
	out := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, "spec", entry.Name()))
		if err != nil {
			t.Fatalf("read spec/%s: %v", entry.Name(), err)
		}
		for _, h := range citation.Headings(string(body)) {
			if h.Number != "" {
				out[h.Number] = true
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("spec/ declares no numbered heading")
	}
	return out
}

// creditKey resolves one annotated section id to the tests/spec-map.json key
// that must carry the case's credit, and reports whether a credit is required
// at all.
//
// The map records a section at whatever granularity it chose, so requiring a
// key spelled exactly as the annotation spells it lets any finer citation pass
// uncredited. An id the map does not key exactly is resolved to its nearest
// declared ancestor: 15.4.2 is credited under 15.4, and 28.6 under 28. An id
// that names a specification heading the map declares at no granularity
// resolves to itself, so the gate reports it and the row is entered. An id
// that names no specification heading is carved out per specHeadingNumbers.
func creditKey(id string, declared, headings map[string]bool) (string, bool) {
	if !headings[id] {
		return "", false
	}
	for key := id; ; {
		if declared[key] {
			return key, true
		}
		dot := strings.LastIndex(key, ".")
		if dot < 0 {
			return id, true
		}
		key = key[:dot]
	}
}

// creditsMissing returns the spec-map keys that the annotated sections require
// and neither the whole-file credits nor the per-case credits supply. Pass a
// nil perCase for a file the map registers as a whole rather than case by case.
func creditsMissing(sections, declared, headings, wholeFile, perCase map[string]bool) map[string]bool {
	missing := map[string]bool{}
	for section := range sections {
		key, required := creditKey(section, declared, headings)
		if !required || wholeFile[key] || perCase[key] {
			continue
		}
		missing[key] = true
	}
	return missing
}

// spec: 4.1 (one address per gRPC request)
//
// Every case that landed with the per-session slot address contract is
// credited in tests/spec-map.json under each spec section its own annotation
// names. A section an annotation names but no map entry records has no test
// against it in the coverage view, and `lenny-test --spec <section>` does not
// select the case that pins it.
func TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate(t *testing.T) {
	headings := specHeadingNumbers(t)
	for _, file := range slotAddressCaseFiles {
		declared, wholeFile, perCase := specMapCredits(t, file)
		if len(perCase) == 0 {
			missing := creditsMissing(annotatedSectionsInFile(t, file), declared, headings, wholeFile, nil)
			if len(missing) > 0 {
				t.Errorf("spec-map.json does not credit %s to section(s) %v its `// spec:` annotation names", file, sortedSectionIDs(missing))
			}
			continue
		}
		for fn, sections := range annotatedSectionsPerCase(t, file) {
			missing := creditsMissing(sections, declared, headings, wholeFile, perCase[fn])
			if len(missing) > 0 {
				t.Errorf("spec-map.json does not credit %s::%s to section(s) %v its own `// spec:` annotation names", file, fn, sortedSectionIDs(missing))
			}
		}
	}
}

// spec: 4.1 (one address per gRPC request)
//
// A `// spec:` annotation that gofumpt has reflowed into a first physical
// line, a bare `//`, and tab-indented continuation lines is read whole. A
// parser that stops at the bare `//` sees only the sections on the first
// line, so every section after it is invisible to the credit gate and a case
// can lose a credit with the gate still green.
func TestReflowedSpecAnnotationIsReadPastTheBlankCommentLine(t *testing.T) {
	lines := []string{
		"// spec: 28.5.3 (the outbound message envelope names the session it is",
		"//",
		"//\taddressed to), 5.2 (the address is carried on every pod, whatever the",
		"//\tpool's concurrency), 15.4",
		"//",
		"// diagnosis: the key the gateway emits is not the one the published",
		"//",
		"//\tschema declares, and 9.9 in this paragraph is prose rather than a",
		"//\tcitation.",
		"func TestSomething(t *testing.T) {",
	}
	got := sortedSectionIDs(sectionsFromLines(lines))
	want := []string{"15.4", "28.5.3", "5.2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sectionsFromLines read %v, want %v", got, want)
	}
}

// spec: 4.1 (one address per gRPC request)
//
// Sections are resolved per test function, so a section one case cites is
// not attributed to a sibling case in the same file. Attributing the union
// of a file's annotations to every case in it lets a per-case spec-map
// registration credit a case the registered section does not belong to.
func TestAnnotationSectionsAreResolvedPerCase(t *testing.T) {
	lines := []string{
		"// spec: 4.1 (one address per request)",
		"//",
		"// diagnosis: the first case broke.",
		"func TestFirstCase(t *testing.T) {",
		"\tt.Parallel()",
		"}",
		"",
		"// spec: 15.4 (the published artifact is the runtime author's contract)",
		"//",
		"// diagnosis: the second case broke.",
		"func TestSecondCase(t *testing.T) {",
		"\tt.Parallel()",
		"}",
	}
	got := perCaseSectionsFromLines(lines)
	if ids := sortedSectionIDs(got["TestFirstCase"]); strings.Join(ids, ",") != "4.1" {
		t.Errorf("TestFirstCase resolved to %v, want [4.1]", ids)
	}
	if ids := sortedSectionIDs(got["TestSecondCase"]); strings.Join(ids, ",") != "15.4" {
		t.Errorf("TestSecondCase resolved to %v, want [15.4]", ids)
	}
}

// spec: 4.1 (message scope)
//
// The credit gate is itself credited by name under the section its own
// `// spec:` annotation cites. tests/spec-map.json registers this file case
// by case, so a section credited to a sibling case does not select this one:
// without its own row, `lenny-test --spec 4.1` runs every other case in the
// file and skips the gate.
func TestTheCreditGateIsCreditedByNameUnderTheSectionItAnnotates(t *testing.T) {
	const gate = "TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate"
	_, wholeFile, perCase := specMapCredits(t, creditGateFile)
	if !wholeFile["4.1"] && !perCase[gate]["4.1"] {
		t.Errorf("spec-map.json section 4.1 credits neither %s as a whole nor %s::%s", creditGateFile, creditGateFile, gate)
	}
}

// spec: 4.1 (one address per gRPC request)
//
// A citation list that ends in a bare section id with no parenthesised gloss
// keeps that id when an ordinary prose paragraph follows the blank comment
// line. A reader that treats every `// text` line as a wrapped-annotation
// continuation glues the trailing id to the first prose word, drops it, and
// then fails to demand the credit that id names.
func TestTrailingBareSectionIDSurvivesAFollowingProseParagraph(t *testing.T) {
	lines := []string{
		"// spec: 4.7 (Resume takes the same claim as StartSession), 5.2",
		"//",
		"// The gateway reserves the slot on the pod connect already claimed, so a",
		"// resume that lands on a second pod is rejected rather than placed.",
		"func TestSomething(t *testing.T) {",
	}
	got := sortedSectionIDs(sectionsFromLines(lines))
	want := []string{"4.7", "5.2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sectionsFromLines read %v, want %v", got, want)
	}
}

// spec: 4.1 (one address per gRPC request)
//
// Citations separated by semicolons, and a citation whose gloss follows an
// em-dash rather than a parenthesis, are both read. The repo's `// spec:`
// annotations use each form, and a reader that recognises only the
// comma-separated parenthesised form is blind to those files, so the credit
// gate passes over every section they name.
func TestSemicolonAndEmDashCitationFormsAreRead(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		want  []string
	}{
		{
			name:  "semicolons with a trailing period",
			lines: []string{"// spec: §5.2; §6.4; §28.5.3."},
			want:  []string{"28.5.3", "5.2", "6.4"},
		},
		{
			name:  "em-dash gloss",
			lines: []string{"// spec: 5.2 — a transient slot failure is retried once on a fresh slot"},
			want:  []string{"5.2"},
		},
		{
			name:  "a quantity in a gloss is not a citation",
			lines: []string{"// spec: 4.1 (one address per request), the latest 2 checkpoints are kept"},
			want:  []string{"4.1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sortedSectionIDs(sectionsFromLines(tc.lines))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("sectionsFromLines read %v, want %v", got, tc.want)
			}
		})
	}
}

// spec: 4.1 (one address per gRPC request)
//
// An annotated section id the map records at a coarser granularity is
// credited at the granularity the map chose. Demanding a key spelled exactly
// as the annotation spells it lets every finer citation pass uncredited, so a
// case can pin a section with no entry against it and the gate stays green.
func TestCreditIsDemandedAtTheMapGranularity(t *testing.T) {
	declared := map[string]bool{"15.4": true, "28": true}
	headings := map[string]bool{"15.4": true, "15.4.2": true, "28": true, "28.6": true, "29.4": true}
	for _, tc := range []struct {
		id   string
		want string
	}{
		{id: "15.4.2", want: "15.4"},
		{id: "28.6", want: "28"},
		{id: "15.4", want: "15.4"},
		// A heading the map declares at no granularity resolves to itself,
		// so the gate names the row that has to be entered.
		{id: "29.4", want: "29.4"},
	} {
		got, required := creditKey(tc.id, declared, headings)
		if !required || got != tc.want {
			t.Errorf("creditKey(%s) = %q, %v, want %q, true", tc.id, got, required, tc.want)
		}
	}
}

// spec: 4.1 (one address per gRPC request)
//
// A `// spec:` annotation that cites a TESTING.md section rather than a
// specification heading demands no spec-map credit, because the map keys
// specification sections alone and no key for such an id can exist.
func TestCreditIsNotDemandedForATestingDocumentSectionID(t *testing.T) {
	headings := specHeadingNumbers(t)
	for _, id := range []string{"13.7", "13.19"} {
		if headings[id] {
			t.Fatalf("spec/ declares heading %s, so it is no longer a TESTING.md-only citation", id)
		}
		if _, required := creditKey(id, map[string]bool{"13": true}, headings); required {
			t.Errorf("creditKey(%s) demands a credit for a TESTING.md section", id)
		}
	}
	for _, id := range []string{"29.4", "29.10", "27.3.1", "15.4.2", "28.6"} {
		if !headings[id] {
			t.Errorf("spec/ declares no heading %s, so the credit gate carves it out", id)
		}
	}
}

// spec: 4.1 (one address per gRPC request)
//
// creditsMissing reports the ancestor key an annotation's finer citation
// resolves to when nothing credits it, and stays silent once either the
// whole-file or the per-case credit supplies that key.
func TestCreditsMissingReportsTheUncreditedAncestorKey(t *testing.T) {
	sections := map[string]bool{"15.4.2": true, "13.7": true}
	declared := map[string]bool{"15.4": true}
	headings := map[string]bool{"15.4": true, "15.4.2": true}
	missing := creditsMissing(sections, declared, headings, map[string]bool{}, nil)
	if got := sortedSectionIDs(missing); strings.Join(got, ",") != "15.4" {
		t.Errorf("creditsMissing reported %v, want [15.4]", got)
	}
	if got := creditsMissing(sections, declared, headings, map[string]bool{"15.4": true}, nil); len(got) != 0 {
		t.Errorf("creditsMissing reported %v for a whole-file credit, want none", sortedSectionIDs(got))
	}
	if got := creditsMissing(sections, declared, headings, map[string]bool{}, map[string]bool{"15.4": true}); len(got) != 0 {
		t.Errorf("creditsMissing reported %v for a per-case credit, want none", sortedSectionIDs(got))
	}
}
