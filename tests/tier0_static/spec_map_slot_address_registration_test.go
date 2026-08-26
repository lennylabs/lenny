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

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// slotAddressAbsenceTestFile is the tier-3 file that pins the absence of a
// slot address from the client-facing REST surfaces. Its cases each assert
// one spec section's contract, and tests/spec-map.json must credit each case
// to the sections that case actually exercises. The map validator only checks
// that a reference resolves, so a case filed under an unrelated section is
// invisible to it: this test closes that gap for the file.
// creditGateFile is this file, whose own cases the credit gate must account
// for as it accounts for every other file in the inventory.
const creditGateFile = "tests/tier0_static/spec_map_slot_address_registration_test.go"

const slotAddressAbsenceTestFile = "tests/tier3_contract/rest_sessions/slot_address_absence_test.go"

// specAnnotationSectionRE matches a section id in a `// spec:` annotation,
// with or without the section sign, and captures the dotted id.
var specAnnotationSectionRE = regexp.MustCompile(`§?(\d+(?:\.\d+)*)`)

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

// specAnnotationSections returns, per test function name declared in the
// given Go file, the set of spec-section ids the function's `// spec:`
// annotation names.
func specAnnotationSections(t *testing.T, file string) map[string]map[string]bool {
	t.Helper()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	lines := strings.Split(string(body), "\n")
	out := map[string]map[string]bool{}
	funcRE := regexp.MustCompile(`^func (Test\w+)\(`)
	for i, line := range lines {
		m := funcRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// Walk back over the contiguous comment block above the
		// declaration and collect every section id it names.
		sections := map[string]bool{}
		for j := i - 1; j >= 0 && strings.HasPrefix(strings.TrimSpace(lines[j]), "//"); j-- {
			for _, s := range specAnnotationSectionRE.FindAllStringSubmatch(lines[j], -1) {
				sections[s[1]] = true
			}
		}
		out[m[1]] = sections
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
	annotated := specAnnotationSections(t, slotAddressAbsenceTestFile)
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

// citedSectionRE matches one spec-section id at the head of a comma-separated
// item of a `// spec:` annotation. The id is anchored to the head of the item
// and must be followed by the opening parenthesis of its gloss or by the end
// of the item, so a number that appears inside a gloss ("a Phase 3 contract
// migration", "the latest 2 checkpoints") is not read as a citation.
var citedSectionRE = regexp.MustCompile(`^§?(\d+(?:\.\d+)*)(\s*\(|\s*$)`)

// isIndentedCommentContinuation reports whether the line at idx is a comment
// line whose text is indented under its `//` marker. gofumpt reflows a wrapped
// `// spec:` annotation into the first physical line, a bare `//`, and then
// tab-indented continuation lines, so an indented comment line after a blank
// one continues the annotation rather than opening a new paragraph.
func isIndentedCommentContinuation(lines []string, idx int) bool {
	if idx >= len(lines) {
		return false
	}
	trimmed := strings.TrimSpace(lines[idx])
	if !strings.HasPrefix(trimmed, "//") {
		return false
	}
	rest := strings.TrimPrefix(trimmed, "//")
	return rest != strings.TrimLeft(rest, " \t") && strings.TrimSpace(rest) != ""
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

// sectionsInAnnotation returns every spec-section id cited at the head of a
// comma-separated item of one joined `// spec:` annotation.
func sectionsInAnnotation(block string, out map[string]bool) {
	for _, item := range strings.Split(block, ",") {
		if m := citedSectionRE.FindStringSubmatch(strings.TrimSpace(item)); m != nil {
			out[m[1]] = true
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

// spec: 28.5.3 (the session identifier addresses every session-scoped frame),
// 4.1 (one address per gRPC request), 6.1 (the per-session credential file),
// 6.4 (the per-slot workspace tree)
//
// Every case that landed with the per-session slot address contract is
// credited in tests/spec-map.json under each spec section its own annotation
// names. A section an annotation names but no map entry records has no test
// against it in the coverage view, and `lenny-test --spec <section>` does not
// select the case that pins it.
func TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate(t *testing.T) {
	for _, file := range slotAddressCaseFiles {
		declared, wholeFile, perCase := specMapCredits(t, file)
		if len(perCase) == 0 {
			missing := map[string]bool{}
			for section := range annotatedSectionsInFile(t, file) {
				if declared[section] && !wholeFile[section] {
					missing[section] = true
				}
			}
			if len(missing) > 0 {
				t.Errorf("spec-map.json does not credit %s to section(s) %v its `// spec:` annotation names", file, sortedSectionIDs(missing))
			}
			continue
		}
		for fn, sections := range annotatedSectionsPerCase(t, file) {
			missing := map[string]bool{}
			for section := range sections {
				if declared[section] && !wholeFile[section] && !perCase[fn][section] {
					missing[section] = true
				}
			}
			if len(missing) > 0 {
				t.Errorf("spec-map.json does not credit %s::%s to section(s) %v its own `// spec:` annotation names", file, fn, sortedSectionIDs(missing))
			}
		}
	}
}

// spec: 17.2 (every test carries a `// spec:` annotation naming the sections
// it exercises)
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

// spec: 17.2 (every test carries a `// spec:` annotation naming the sections
// it exercises)
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
