// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"io/fs"
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

// addressRuleGateFile is the tier-0 gate that holds every address-guard case
// to the section stating the rule. It carries its own `// spec:` annotation,
// so the credit inventory must carry it as it carries every other case file.
const addressRuleGateFile = "tests/tier0_static/address_rule_citation_test.go"

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

// slotAddressCaseFiles is the inventory of test cases that carry the
// per-session slot address contract. Each of them must be credited in
// tests/spec-map.json under every spec section its own annotation names, so
// that a section the case pins has a test against it in the coverage view and
// `lenny-test --spec <section>` selects the case.
//
// The inventory is the full set of case files the per-session address change
// stages, whether the change added the file, rewrote cases inside it, or left
// its citation lines untouched while rewriting the behavior it asserts. A
// narrower inventory taken from the change's own citation-line diff credits
// the gaps it happens to have edited and is blind to every staged case whose
// annotation it did not touch, which is the failure this gate exists to
// prevent. It includes this file, because a gate that omits itself does not
// check the case it is part of.
//
// The map validator walks tier 2 through tier 10 only, and it checks that a
// reference resolves rather than that a case is credited to the sections it
// exercises, so a tier-0, tier-11, `cmd/`, `pkg/`, `sdks/`, or `migrations/`
// case can carry an annotation no section records.
var slotAddressCaseFiles = []string{
	"cmd/lenny-compliance/full_test.go",
	"cmd/lenny-compliance/sessionecho_test.go",
	"cmd/lenny-compliance/standard_test.go",
	"cmd/lenny-ctl/runtimescaffold/scaffold_test.go",
	"cmd/lenny-gateway/direct_usage_quota_integration_test.go",
	"cmd/lenny-test/cmd_run_race_test.go",
	"cmd/runtimes/echo-concurrent/main_test.go",
	"migrations/0178_checkpoint_manifest_test.go",
	"migrations/session_checkpoints_slot_id_test.go",
	"pkg/adapter/adapterevents_test.go",
	"pkg/adapter/attach_test.go",
	"pkg/adapter/checkpoint_stream_test.go",
	"pkg/adapter/connectormcp_test.go",
	"pkg/adapter/coordination_test.go",
	"pkg/adapter/credentials_test.go",
	"pkg/adapter/credexpiry_test.go",
	"pkg/adapter/drain_test.go",
	"pkg/adapter/embedded_sdkwarm_test.go",
	"pkg/adapter/export_test.go",
	"pkg/adapter/exportpaths_test.go",
	"pkg/adapter/files_updated_test.go",
	"pkg/adapter/gatewaycontrol/scrubreport_test.go",
	"pkg/adapter/holdstate_test.go",
	"pkg/adapter/integrationlevel_test.go",
	"pkg/adapter/manifest_fields_test.go",
	"pkg/adapter/manifest_test.go",
	"pkg/adapter/mcpruntime_test.go",
	"pkg/adapter/one_session_only_test.go",
	"pkg/adapter/oplock_test.go",
	"pkg/adapter/platformmcp_test.go",
	"pkg/adapter/podmcp_arming_internal_test.go",
	"pkg/adapter/podscrub_test.go",
	"pkg/adapter/resume_test.go",
	"pkg/adapter/scrub/scrub_test.go",
	"pkg/adapter/sdkwarm_test.go",
	"pkg/adapter/session_test.go",
	"pkg/adapter/sessionscrub_emit_test.go",
	"pkg/adapter/shutdown_demote_test.go",
	"pkg/adapter/slot_test.go",
	"pkg/adapter/slotframe_test.go",
	"pkg/adapter/slotlayout/slotlayout_test.go",
	"pkg/adapter/socketruntime_e2e_test.go",
	"pkg/adapter/staging_test.go",
	"pkg/adapter/statelessslot/statelessslot_test.go",
	"pkg/adapter/tracing_external_test.go",
	"pkg/adapter/tracing_internal_test.go",
	"pkg/adapter/tracingcontext_addressing_test.go",
	"pkg/adapter/tracingcontext_sampling_test.go",
	"pkg/adapter/usage_test.go",
	"pkg/adapter/warmlayout_test.go",
	"pkg/controller/sandbox/podspec/podspec_test.go",
	"pkg/gateway/checkpoint/checkpointer/checkpointer_test.go",
	"pkg/gateway/checkpoint/checkpointer/checkpointstart_test.go",
	"pkg/gateway/checkpoint/checkpointer/uploaddriver_test.go",
	"pkg/gateway/checkpoint/checkpointretention/checkpointretention_test.go",
	"pkg/gateway/checkpoint/partialmanifeststore/partialmanifeststore_test.go",
	"pkg/gateway/coordination/barrier/wiring_test.go",
	"pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server_test.go",
	"pkg/gateway/podlifecycle/podclaim/maxpoduptime_test.go",
	"pkg/gateway/podlifecycle/podclaim/reserveslotonpod_test.go",
	"pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go",
	"pkg/gateway/podlifecycle/podclaim/tenant_label_test.go",
	"pkg/gateway/podlifecycle/podsession/binder_archive_test.go",
	"pkg/gateway/podlifecycle/podsession/binder_phases_test.go",
	"pkg/gateway/podlifecycle/podsession/binder_readopt_test.go",
	"pkg/gateway/podlifecycle/podsession/binder_test.go",
	"pkg/gateway/podlifecycle/podsession/one_session_only_test.go",
	"pkg/gateway/podlifecycle/podsession/resume_slot_reservation_test.go",
	"pkg/gateway/podlifecycle/podsession/sdkwarm_bind_test.go",
	"pkg/gateway/podlifecycle/podsession/slotbinder_test.go",
	"pkg/gateway/podlifecycle/podsession/workspace_base_propagation_test.go",
	"pkg/gateway/runtime/adapterclient/checkpointbarrier_test.go",
	"pkg/gateway/runtime/adapterclient/client_test.go",
	"pkg/gateway/runtime/slothealth/slothealth_test.go",
	"pkg/gateway/session/executor/pod_test.go",
	"pkg/gateway/session/sessionstore/memstore/reserve_slot_under_lock_test.go",
	"pkg/gateway/sessionserver/create_test.go",
	"pkg/gateway/sessionserver/delegated_child_materialize_test.go",
	"pkg/gateway/sessionserver/messages_component_test.go",
	"pkg/gateway/sessionserver/messages_test.go",
	"pkg/gateway/sessionserver/podclaimerror_internal_test.go",
	"pkg/gateway/sessionserver/pool_exhaustion_queue_test.go",
	"pkg/gateway/sessionserver/pool_selection_component_test.go",
	"pkg/gateway/sessionserver/recycle_scrub_fold_component_test.go",
	"pkg/gateway/sessionserver/resume_chunk_selection_internal_test.go",
	"pkg/gateway/sessionserver/resume_external_effect_regression_test.go",
	"pkg/gateway/sessionserver/slothealth_inject_internal_test.go",
	"pkg/gateway/sessionserver/slotretry_load_test.go",
	"pkg/gateway/sessionserver/slotretry_test.go",
	"pkg/gateway/sessionserver/start_pod_lease_component_test.go",
	"pkg/gateway/sessionserver/start_pod_test.go",
	"pkg/gateway/sessionserver/start_preclaim_internal_test.go",
	"pkg/gateway/sessionserver/upload_to_session_test.go",
	"pkg/gateway/sessionserver/workspace_root_persist_test.go",
	"pkg/gateway/storage/slotcounter/slotcounter_test.go",
	"pkg/sandbox/slotstate/slotstate_test.go",
	"sdks/runtime/go/runtime/runtime_test.go",
	"tests/testinfra/sessiondriver/sessiondriver_test.go",
	"tests/tier0_static/adapter_proto_message_scope_test.go",
	"tests/tier0_static/adapter_proto_parse_test.go",
	"tests/tier0_static/address_rule_citation_test.go",
	"tests/tier0_static/checkpoint_dropped_slot_column_comment_test.go",
	"tests/tier0_static/checkpoint_scoping_key_comment_test.go",
	"tests/tier0_static/claim_register_generator_test.go",
	"tests/tier0_static/claim_register_proto_agreement_test.go",
	"tests/tier0_static/claim_register_test.go",
	"tests/tier0_static/slot_absence_claim_comment_test.go",
	"tests/tier0_static/slot_assignment_attribution_test.go",
	"tests/tier0_static/spec_map_exception_blocker_retention_test.go",
	"tests/tier0_static/spec_map_slot_address_registration_test.go",
	"tests/tier10_conformance/concurrent_slot_conformance_test.go",
	"tests/tier10_conformance/credential_path_resolution_conformance_test.go",
	"tests/tier10_conformance/recycle_scrub_conformance_test.go",
	"tests/tier10_conformance/reference_battery_test.go",
	"tests/tier10_conformance/scaffold_battery_test.go",
	"tests/tier10_conformance/scaffolds_test.go",
	"tests/tier10_conformance/token_service_unavailability_guard_conformance_test.go",
	"tests/tier11_docs/adapter_manifest_credentials_path_doc_reconciliation_test.go",
	"tests/tier11_docs/adapter_manifest_rewrite_trigger_doc_test.go",
	"tests/tier11_docs/adapter_manifest_session_identifier_currency_doc_test.go",
	"tests/tier11_docs/adapter_metric_catalog_test.go",
	"tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go",
	"tests/tier11_docs/checkpoint_pipeline_consistency_test.go",
	"tests/tier11_docs/checkpoint_scoping_key_test.go",
	"tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go",
	"tests/tier11_docs/credential_path_literal_sweep_test.go",
	"tests/tier11_docs/embedded_manifests_sync_test.go",
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
	"tests/tier11_docs/successor_pointer_test.go",
	"tests/tier11_docs/tracing_context_addressing_doc_reconciliation_test.go",
	"tests/tier11_docs/workspace_path_literal_sweep_test.go",
	"tests/tier2_component/legalholdreconciler/reconciler_test.go",
	"tests/tier2_component/migrations/checkpoint_slot_id_drop_test.go",
	"tests/tier2_component/migrations/prod_columns_test.go",
	"tests/tier2_component/observability/catalog_crosscheck_test.go",
	"tests/tier2_component/rls/checkpoint_manifest_test.go",
	"tests/tier2_component/slotrelease/revoke_double_teardown_test.go",
	"tests/tier2_component/stores/checkpointretention_test.go",
	"tests/tier2_component/stores/partialmanifeststore_test.go",
	"tests/tier2_component/stores/reserve_slot_under_lock_test.go",
	"tests/tier2_component/translators/openai_singleshot_lifecycle_test.go",
	"tests/tier2_component/warmlayout/warm_layout_test.go",
	"tests/tier3_contract/adapter_extendcredlease/extend_credential_lease_wire_test.go",
	"tests/tier3_contract/adapter_frame_resolution/session_scoped_frame_resolution_test.go",
	"tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go",
	"tests/tier3_contract/adapter_jsonl/gateway_envelope_address_test.go",
	"tests/tier3_contract/adapter_jsonl/session_scoped_frame_address_test.go",
	"tests/tier3_contract/adapter_jsonl/set_tracing_context_test.go",
	"tests/tier3_contract/adapter_jsonl/subprocess_envelope_address_test.go",
	"tests/tier3_contract/adapter_negotiate/negotiate_workspace_base_wire_test.go",
	"tests/tier3_contract/adapter_reportusage/reportusage_wire_test.go",
	"tests/tier3_contract/adapter_session_address/session_address_wire_test.go",
	"tests/tier3_contract/adapter_usage_wired/wired_reportusage_test.go",
	"tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go",
	"tests/tier3_contract/gatewaycontrol_scrub/scrub_wire_test.go",
	"tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go",
	"tests/tier3_contract/rest_sessions/slot_address_absence_test.go",
	"tests/tier3_contract/sdks/go_client_test.go",
	"tests/tier3_contract/sdks/python_client_test.go",
	"tests/tier3_contract/sdks/runtime_sdk_test.go",
	"tests/tier3_contract/sdks/typescript_client_test.go",
	"tests/tier4_integration/checkpoint_chunk_helpers_test.go",
	"tests/tier4_integration/checkpoint_concurrent_pool_test.go",
	"tests/tier4_integration/checkpoint_driver_harness_test.go",
	"tests/tier4_integration/checkpoint_grant_remint_test.go",
	"tests/tier4_integration/checkpoint_intent_generation_test.go",
	"tests/tier4_integration/concurrent_delegation_proxy_test.go",
	"tests/tier4_integration/concurrent_workspace_test.go",
	"tests/tier4_integration/credential_delivery_gate_test.go",
	"tests/tier4_integration/credential_lifecycle_test.go",
	"tests/tier4_integration/cross_environment_delegation_test.go",
	"tests/tier4_integration/delegation_child_materialization_test.go",
	"tests/tier4_integration/eager_claim_lifecycle_test.go",
	"tests/tier4_integration/mcp_runtime_lifecycle_test.go",
	"tests/tier4_integration/recycle_scrub_path_test.go",
	"tests/tier4_integration/slot_counter_redis_outage_fallback_test.go",
	"tests/tier4_integration/token_service_unavailability_guard_test.go",
	"tests/tier5_e2e_kind/admission_test.go",
	"tests/tier5_e2e_kind/checkpoint_resume_test.go",
	"tests/tier5_e2e_kind/diagnostics_fix_test.go",
	"tests/tier5_e2e_kind/eager_claim_e2e_test.go",
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
	"tests/tier8_chaos/slot_counter_redis_outage_test.go",
	"tests/tier8_chaos/token_service_unavailability_guard_test.go",
	"tests/tier9_security/adapter_hold_termination_surface_test.go",
	"tests/tier9_security/adapter_mcp_nonce_test.go",
	"tests/tier9_security/adapter_shared_mcp_surface_test.go",
	"tests/tier9_security/audit_integrity_test.go",
	"tests/tier9_security/audit_sequence_precondition_test.go",
	"tests/tier9_security/concurrent_slot_isolation_test.go",
	"tests/tier9_security/credential_delivery_gate_test.go",
	"tests/tier9_security/delegation_child_materialization_cred_test.go",
	"tests/tier9_security/delegation_credential_deny_leakage_test.go",
	"tests/tier9_security/live_session_test.go",
	"tests/tier9_security/ops_network_policy_test.go",
	"tests/tier9_security/session_teardown_surface_test.go",
	"tests/tier9_security/tracing_context_session_isolation_test.go",
}

// citedSectionHeadRE splits one item of a `// spec:` annotation into the
// optional section sign, a dotted section id anchored at the head of the item,
// and whatever follows the id inside the item.
var citedSectionHeadRE = regexp.MustCompile(`^(§?)(\d+(?:\.\d+)*)(.*)$`)

// proposalGlossRE matches a gloss that opens with the word "proposal", which
// is how an annotation marks the number it carries as a proposal document's
// own section rather than a specification section.
var proposalGlossRE = regexp.MustCompile(`^\(\s*proposal\b`)

// glossMarksAProposalSection reports whether the text following a cited id
// opens a gloss that names the number as a proposal's own section. A proposal
// numbers its sections independently of the specification, so crediting such
// an id to a spec-map key records coverage of a specification section the case
// does not exercise. The marker is a defect in the annotation rather than a
// supported citation form: the reader recognizes it so that no id in the run it
// terminates is credited, and TestAddressCaseAnnotationsCiteSpecificationHeadingsOnly
// reports every site that carries one.
func glossMarksAProposalSection(rest string) bool {
	return proposalGlossRE.MatchString(strings.ToLower(strings.TrimLeft(rest, " \t")))
}

// itemCarriesTheProposalMarker reports whether any run of one item of a joined
// `// spec:` annotation glosses its id as a proposal document's own section.
func itemCarriesTheProposalMarker(item string) bool {
	for _, run := range strings.Split(item, "/") {
		m := citedSectionHeadRE.FindStringSubmatch(strings.TrimSpace(run))
		if m == nil {
			continue
		}
		if glossMarksAProposalSection(strings.TrimRight(m[3], " \t")) {
			return true
		}
	}
	return false
}

// citedSectionAtHead returns the spec-section id an annotation item cites at
// its head. The id must open the item, and what follows it decides whether the
// item is a citation: the section sign, the end of the item, a trailing
// period, colon, or semicolon, the opening parenthesis of a gloss, or, for a
// dotted id, a gloss introduced by whitespace, which is how the repo's
// annotations write an em-dash or bare-word gloss. An undotted number followed
// by prose ("a Phase 3 contract migration", "the latest 2 checkpoints") is a
// quantity rather than a citation and is not read as one. An id whose gloss
// opens with the word "proposal" names a proposal document's own section and
// is not a specification citation at all.
func citedSectionAtHead(item string) (string, bool) {
	m := citedSectionHeadRE.FindStringSubmatch(strings.TrimSpace(item))
	if m == nil {
		return "", false
	}
	sign, id, rest := m[1], m[2], strings.TrimRight(m[3], " \t")
	if glossMarksAProposalSection(rest) {
		return "", false
	}
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

// specTagRE matches the `spec:` tag of an annotation inside a comment line.
// The tag opens the comment line in the canonical position and also follows
// the line's own prose ("// rejected. spec: §7.4"), so the reader locates it
// anywhere in the comment text rather than at the head alone.
var specTagRE = regexp.MustCompile(`(^|[^\pL\pN_])spec:`)

// specTagInComment returns the offset just past the `spec:` tag of an
// annotation carried by the given line, and reports whether the line carries
// one. Only a comment line carries an annotation, so a `// spec:` written
// inside a string literal (a parser fixture, for example) is not one. A tag
// inside a backtick-quoted span is a prose mention of the convention rather
// than a citation, as in this file's own comments.
func specTagInComment(line string) (int, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "//") {
		return 0, false
	}
	offset := strings.Index(line, "//")
	text := line[offset:]
	for _, m := range specTagRE.FindAllStringIndex(text, -1) {
		if strings.Count(text[:m[0]], "`")%2 == 1 {
			continue
		}
		return offset + m[1], true
	}
	return 0, false
}

// annotationBlockAt joins the `// spec:` annotation opening at lines[i] with
// the comment lines that continue it, and returns the joined text and the
// index of the last line consumed. A blank comment line terminates the block
// unless the comment line after it is indented, which is how gofumpt renders a
// wrapped annotation: dropping the block there would hide every section after
// the first physical line. The block also ends at the `diagnosis:` tag, at a
// following `spec:` tag, and at the end of the comment group.
func annotationBlockAt(lines []string, i, tagEnd int) (string, int) {
	block := []string{strings.TrimSpace(lines[i][tagEnd:])}
	j := i + 1
	for ; j < len(lines); j++ {
		next := strings.TrimSpace(lines[j])
		if !strings.HasPrefix(next, "//") {
			break
		}
		next = strings.TrimSpace(strings.TrimPrefix(next, "//"))
		if strings.HasPrefix(next, "diagnosis:") {
			break
		}
		if _, ok := specTagInComment(lines[j]); ok {
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
// citations with a comma or a semicolon, and also run several citations
// together with a slash ("§5.1 / §15.4.3 — ..."), so a slash opens a further
// citation inside one comma-separated item.
//
// A slash carries other freight than a separator: it divides a gloss's own
// words ("Event / Checkpoint Store") and joins a pair of quantities
// ("paths 2/4"). A run after a slash is therefore read as a citation only
// when it announces itself as one, by carrying the section sign or by naming
// a dotted id. The run at the head of the item keeps the ordinary rule.
//
// The proposal marker is written once after a run of ids rather than after
// each of them ("§6.1, §4.3, §4.4 (proposal)"), so the marker disqualifies
// every id read since the last marked item rather than the one it directly
// follows. Crediting the unmarked head of such a run records coverage of a
// specification section the case does not exercise.
func sectionsInAnnotation(block string, out map[string]bool) {
	pending := []string{}
	for _, item := range strings.FieldsFunc(block, func(r rune) bool { return r == ',' || r == ';' }) {
		if itemCarriesTheProposalMarker(item) {
			pending = pending[:0]
			continue
		}
		for i, run := range strings.Split(item, "/") {
			id, ok := citedSectionAtHead(run)
			if !ok {
				continue
			}
			if i > 0 && !announcesItselfAsACitation(run) {
				continue
			}
			pending = append(pending, id)
		}
	}
	for _, id := range pending {
		out[id] = true
	}
}

// announcesItselfAsACitation reports whether a run of an annotation item names
// its section explicitly enough to be read as a citation in a position where a
// bare number is more often a quantity. The section sign and a dotted id both
// qualify; a bare integer does not.
func announcesItselfAsACitation(run string) bool {
	run = strings.TrimSpace(run)
	if strings.HasPrefix(run, "§") {
		return true
	}
	m := citedSectionHeadRE.FindStringSubmatch(run)
	return m != nil && strings.Contains(m[2], ".")
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
		tagEnd, ok := specTagInComment(lines[i])
		if !ok {
			continue
		}
		block, last := annotationBlockAt(lines, i, tagEnd)
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
		if tagEnd, ok := specTagInComment(lines[i]); ok {
			block, last := annotationBlockAt(lines, i, tagEnd)
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

// caseNameSectionRE captures the section id a test function name carries in
// its `_spec_<id>` suffix, with the id's dots written as underscores.
var caseNameSectionRE = regexp.MustCompile(`_spec_(\d+(?:_\d+)*)$`)

// caseNameSection returns the spec-section id a test function name asserts in
// its suffix, and reports whether the name carries one.
func caseNameSection(fn string) (string, bool) {
	m := caseNameSectionRE.FindStringSubmatch(fn)
	if m == nil {
		return "", false
	}
	return strings.ReplaceAll(m[1], "_", "."), true
}

// sectionAgreesWithCitation reports whether a section id a case name asserts
// is answered by one of the ids the case's own annotation cites. An exact
// match agrees; so does a citation at a finer or a coarser granularity than
// the name, because a name carrying a parent id over an annotation citing one
// of that id's subsections states the same heading at the granularity a name
// can carry.
func sectionAgreesWithCitation(named string, cited map[string]bool) bool {
	for id := range cited {
		if id == named || strings.HasPrefix(id, named+".") || strings.HasPrefix(named, id+".") {
			return true
		}
	}
	return false
}

// spec: 4.1 (one address per gRPC request)
//
// A case whose name carries a `_spec_X_Y` suffix names a section its own
// `// spec:` annotation cites. The suffix is the citation a reader sees in the
// verdict and in any per-case spec-map row entered later, so a name left
// behind when the annotation was re-pointed at the heading that states the
// behavior travels an unrelated section with the case. Nothing mechanical
// reads the suffix, so the disagreement is invisible until a reader chases the
// number.
func TestAddressCaseNamesAgreeWithTheirOwnCitations(t *testing.T) {
	for _, file := range slotAddressCaseFiles {
		for fn, cited := range annotatedSectionsPerCase(t, file) {
			named, ok := caseNameSection(fn)
			if !ok || len(cited) == 0 {
				continue
			}
			if !sectionAgreesWithCitation(named, cited) {
				t.Errorf("%s::%s names section %s, which its own `// spec:` annotation does not cite (it names %v)", file, fn, named, sortedSectionIDs(cited))
			}
		}
	}
}

// proposalNumberedProseRE matches a citation of a proposal document's own
// section number written in prose or in an assertion string, where the
// annotation gate does not look. The section sign is what makes the number a
// citation: a bare reference to a numbered proposal document ("proposal 0024")
// names the document rather than one of its sections.
var proposalNumberedProseRE = regexp.MustCompile(`(?i)proposal\s+§\s*\d`)

// spec: 4.1 (one address per gRPC request)
//
// No case file cites a proposal document's own section number outside a
// `// spec:` annotation. A failure message is the text a reader is handed when
// the case fails, so a proposal section number in one sends that reader to an
// unrelated specification heading, and the annotation gate does not see it
// because it reads annotation lines alone.
func TestAddressCaseTextCitesNoProposalSectionNumber(t *testing.T) {
	for _, file := range slotAddressCaseFiles {
		for i, line := range repoFileLines(t, file) {
			if proposalNumberedProseRE.MatchString(line) {
				t.Errorf("%s:%d cites a proposal document's own section number; name the specification heading that states the behavior", file, i+1)
			}
		}
	}
}

// proposalMarkedAnnotationSites returns, for one repo-relative file, the
// 1-based line numbers of the `// spec:` annotations that cite a proposal
// document's own section number.
func proposalMarkedAnnotationSites(t *testing.T, file string) []int {
	t.Helper()
	lines := repoFileLines(t, file)
	sites := []int{}
	for i := 0; i < len(lines); i++ {
		tagEnd, ok := specTagInComment(lines[i])
		if !ok {
			continue
		}
		block, last := annotationBlockAt(lines, i, tagEnd)
		for _, item := range strings.FieldsFunc(block, func(r rune) bool { return r == ',' || r == ';' }) {
			if itemCarriesTheProposalMarker(item) {
				sites = append(sites, i+1)
				break
			}
		}
		i = last
	}
	return sites
}

// spec: 4.1 (one address per gRPC request)
//
// A `// spec:` annotation names a specification heading. A proposal numbers
// its own sections independently, and those numbers exist only under
// proposals/, so an annotation that cites one records no traceable coverage:
// the number resolves to an unrelated specification heading, and the
// section that states the behavior keeps showing no test against it. The
// credit gate reads the marker so that it credits no such id, and this gate
// reports the site so the citation is corrected to the heading that states
// the behavior rather than left to pass silently.
func TestAddressCaseAnnotationsCiteSpecificationHeadingsOnly(t *testing.T) {
	for _, file := range slotAddressCaseFiles {
		for _, line := range proposalMarkedAnnotationSites(t, file) {
			t.Errorf("%s:%d cites a proposal document's own section number under `// spec:`; cite the specification heading that states the behavior", file, line)
		}
	}
}

// spec: 4.1 (one address per gRPC request)
//
// Every file a sibling gate inventories as carrying the address contract is
// also carried by the credit inventory. A gate that names a case file the
// credit inventory omits leaves that file's citations uncredited: the case
// asserts the rule, and the section the rule is stated in still shows no test
// against it in the coverage view.
func TestTheCreditInventoryCarriesEveryAddressGuardCaseFile(t *testing.T) {
	inventory := map[string]bool{}
	for _, file := range slotAddressCaseFiles {
		inventory[file] = true
	}
	files := map[string]string{addressRuleGateFile: "the address-guard gate inventories"}
	for file := range addressRuleCases {
		files[file] = "the address-guard gate inventories"
	}
	for file, reason := range derivedInventoryCaseFiles(t) {
		if _, seen := files[file]; !seen {
			files[file] = reason
		}
	}
	names := make([]string, 0, len(files))
	for file := range files {
		names = append(names, file)
	}
	sort.Strings(names)
	for _, file := range names {
		if !inventory[file] {
			t.Errorf("slotAddressCaseFiles omits %s, which %s", file, files[file])
		}
	}
}

// spec: 4.1 (one address per gRPC request)
//
// The completeness rules derived from the tree report the case files a
// hand-written inventory silently dropped: the claimer's own unit cases, the
// concurrent slot-retry load case, the gateway-side half of the
// one-session-per-pod invariant, and the create-time claim cases that reserve
// a session's slot without naming the claimer or the slot in the file name. A
// completeness check that resolves only against a sibling gate's five-file
// subset reports none of them, so the inventory stays short and every section
// those cases pin shows no test against it in the coverage view.
func TestDerivedInventoryRulesReportTheCaseFilesAHandListDrops(t *testing.T) {
	derived := derivedInventoryCaseFiles(t)
	for _, file := range []string{
		"pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go",
		"pkg/gateway/podlifecycle/podsession/one_session_only_test.go",
		"pkg/gateway/sessionserver/slotretry_load_test.go",
		"pkg/gateway/sessionserver/start_preclaim_internal_test.go",
	} {
		if _, ok := derived[file]; !ok {
			t.Errorf("the derived completeness rules do not report %s, so the inventory can omit it with tier 0 green", file)
		}
	}
}

// slotSurfaceCallRE matches a call into the pod-side slot surface the
// per-session address contract is built on: the claimer's claim, release, and
// reservation entry points, the gateway-side create-time claim and reserved-slot
// bind that drive them, and the slot state package the contract keys on.
//
// The create-time claim and the reserved-slot bind are named because a case
// can reserve a session's slot without naming the claimer: it calls
// claimAtCreate, which reserves through the claimer underneath, and its file
// name states neither the slot nor the one-session-per-pod invariant. Such a
// file falls outside both derived rules and is dropped from the inventory
// silently.
var slotSurfaceCallRE = regexp.MustCompile(`slotstate\.|ClaimSlot\(|ReleaseSlot\(|ReserveSlotOnPod\(|claimAtCreate\(|BindReservedSlot\(`)

// slotSubjectFileRE matches a case file whose own name states that its subject
// is the slot or the one-session-per-pod invariant the address contract binds
// a session to.
var slotSubjectFileRE = regexp.MustCompile(`(slot|one_session_only|sole_session)[^/]*_test\.go$`)

// inventoryWalkRoots are the top-level directories that hold test files. The
// derived inventory rules walk these rather than the whole tree so that build
// output, fixtures, and vendored trees are out of the walk.
var inventoryWalkRoots = []string{"cmd", "migrations", "pkg", "scripts", "sdks", "tests"}

// derivedInventoryCaseFiles returns the case files the credit inventory must
// carry that are derived from the tree rather than read off a sibling gate's
// hand-written list, mapped to the reason each one is required.
//
// The inventory is hand-maintained, and a hand-maintained list checked only
// against a five-file subset of itself cannot report the case file nobody
// entered. Two rules derived from the tree close the common omissions:
//
//  1. A case that calls the slot claim surface asserts the contract directly,
//     whatever package it sits in.
//  2. A case file whose name states the slot or the one-session-per-pod
//     invariant as its subject carries the contract by construction. This is
//     how an invariant enforced on both the adapter side and the gateway side
//     goes half-entered, with one side's case file in the inventory and the
//     other side's, under the same name in another package, left out.
//
// Neither rule reconstructs the whole inventory, and a case file outside both
// is still entered by hand. Each rule turns one class of silent omission into
// a tier-0 failure.
func derivedInventoryCaseFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, file := range repoTestFiles(t) {
		if slotSurfaceCallRE.Match(repoFileBytes(t, file)) {
			out[file] = "calls the slot claim surface the address contract is built on"
			continue
		}
		if slotSubjectFileRE.MatchString(file) {
			out[file] = "names the slot contract as its own subject"
		}
	}
	return out
}

// repoTestFiles returns every `_test.go` file under the walk roots, as
// repo-relative paths. Test fixtures under a `testdata` directory record a
// case as it was written rather than assert one, so they are out of the walk.
func repoTestFiles(t *testing.T) []string {
	t.Helper()
	root := schematest.RepoRoot(t)
	var out []string
	for _, dir := range inventoryWalkRoots {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			out = append(out, rel)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	sort.Strings(out)
	return out
}

// repoFileBytes reads a repo-relative file.
func repoFileBytes(t *testing.T, file string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(schematest.RepoRoot(t), file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	return body
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
		{
			name:  "slash-separated citations",
			lines: []string{"// spec: §5.1 / §15.4.3 — the integration level the manifest declares"},
			want:  []string{"15.4.3", "5.1"},
		},
		{
			name:  "slash-separated citations with an item-internal qualifier",
			lines: []string{"// spec: §17.2 item 5 / §5.2 / §13.2 NET-003"},
			want:  []string{"13.2", "17.2", "5.2"},
		},
		{
			name:  "a slash inside a gloss yields no second citation",
			lines: []string{"// spec: 4.4 (Event / Checkpoint Store)"},
			want:  []string{"4.4"},
		},
		{
			name:  "a pair of quantities joined by a slash is not a citation",
			lines: []string{"// spec: §15.1; §7.2 paths 2/4."},
			want:  []string{"15.1", "7.2"},
		},
		{
			name:  "a proposal-marked number is not a specification citation",
			lines: []string{"// spec: §4.1 (proposal), §7.1 step 4 — the phases a binder runs"},
			want:  []string{"7.1"},
		},
		{
			// The marker is written once after a run of proposal-numbered
			// ids, so it disqualifies the whole run rather than the id it
			// directly follows. Reading the head of the run as a citation
			// credits a specification section the case does not exercise.
			name:  "a proposal marker disqualifies the run it terminates",
			lines: []string{"// spec: §6.1, §4.3, §4.4 (proposal) — a pure demotion decision"},
			want:  nil,
		},
		{
			// A marked run ends at the marker, so a citation written after
			// one is still read.
			name:  "a citation after a marked run is still read",
			lines: []string{"// spec: §4.4, §4.6 (proposal); §15.1 (precondition); §6.1."},
			want:  []string{"15.1", "6.1"},
		},
		{
			name:  "a tag written after the line's own prose is read",
			lines: []string{"// rejected. spec: §7.4; §13.4 — the staging rules."},
			want:  []string{"13.4", "7.4"},
		},
		{
			// This file's own comments name the convention in backticks.
			// A quoted mention is prose rather than a citation.
			name:  "a backticked mention of the tag is not an annotation",
			lines: []string{"// The `// spec:` annotations name §5.2 and §6.4."},
			want:  nil,
		},
		{
			// A `// spec:` inside a string literal is fixture data rather
			// than an annotation on the code around it.
			name:  `a tag inside a string literal is not an annotation`,
			lines: []string{"\tlines: []string{\"// spec: §5.2\"},"},
			want:  nil,
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
