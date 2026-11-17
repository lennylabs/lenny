#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Split tests/static into the build, chart, lint, and structure packages.

This is the mechanical part of change 0155, step S2. It runs from the root
of a clean checkout. Each test file of tests/static moves with `git mv` into
the package its category names, using the categories of the test
infrastructure review (changes/archive/2026-11-17-0154-merge-gate/evidence/
test-infrastructure-review.md, "Question 1", and "Merge"):

- build: category A, build and generated-artifact drift.
- chart: categories B and G, the Helm chart render, the deployment boundary,
  and the specification-to-chart sync.
- lint: categories C and H, code-invariant lints over Go source and
  test-hygiene lints.
- structure: categories E and F, the specification citation, naming, and
  migration gates, the gates of the specification structure, and the
  self-tests of the maps.

Three files sit in another package than their category because they share
helpers with it: claim_register_proto_agreement_test.go (C) reads the claim
register with the claim register gates, skip_reason_classifier_test.go (H)
shares its scan with the residual gate, and ops_redis_wiring_test.go (C)
reads the chart render helpers. The adapter proto parse that a lint and a
structure gate share moves to tests/static/internal/adapterproto.

A testdata directory moves with the package that reads it, and every path
reference to a moved file outside the historical records and the fixture
trees is rewritten.

Usage: static_split.py
"""
import os
import re
import subprocess

CATEGORY = {
    "build": [
        "api_codegen_drift_test.go",
        "changes_records_test.go",
        "crds_test.go",
        "license_test.go",
        "migration_numbers_unique_test.go",
        "proto_no_drift_test.go",
        "schemas_test.go",
        "supervisor_binary_test.go",
        "workflow_tool_staging_test.go",
    ],
    "chart": [
        "admission_token_service_gates_test.go",
        "airgap_registry_render_test.go",
        "controller_chart_flags_test.go",
        "core_deployment_inventory_test.go",
        "credentials_namespace_rbac_render_test.go",
        "deployment_boundary_register_test.go",
        "deployment_boundary_render_test.go",
        "deployment_boundary_scan_test.go",
        "deployment_boundary_test.go",
        "dev_profile_render_test.go",
        "embedded_postgres_port_test.go",
        "gateway_admin_tls_listener_test.go",
        "gateway_multi_tenant_render_test.go",
        "openslo_export_test.go",
        "ops_rbac_spec_sync_test.go",
        "ops_redis_wiring_test.go",
        "prestop_image_path_test.go",
        "self_managed_postgres_path_render_test.go",
        "token_service_tenant_render_test.go",
        "upgrade_channel_key_render_test.go",
    ],
    "lint": [
        "adapter_proto_message_scope_test.go",
        "calibration_scaffold_test.go",
        "coordinator_hold_allowlist_test.go",
        "erasure_exemption_guard_test.go",
        "jwt_retired_signer_symbols_test.go",
        "ops_dead_code_test.go",
        "production_mode_carrier_test.go",
        "redis_cluster_safety_test.go",
        "scrub_host_ops_test.go",
        "test_fixture_temp_root_test.go",
        "tier6_managed_service_provider_coverage_test.go",
    ],
    "structure": [
        "annotation_transition_rule_test.go",
        "change_graph_blobstore_replication_selection_test.go",
        "change_graph_drift_selection_test.go",
        "change_graph_gateway_operability_selection_test.go",
        "change_graph_ops_selection_test.go",
        "change_graph_slotreaper_selection_test.go",
        "citation_document_test.go",
        "claim_register_generator_test.go",
        "claim_register_proto_agreement_test.go",
        "claim_register_test.go",
        "compliance_check_channel_naming_test.go",
        "doc_spec_gates_test.go",
        "fragment_link_test.go",
        "gate_integrity_test.go",
        "heading_walker_test.go",
        "identifier_resolution_test.go",
        "line_citation_ratchet_test.go",
        "matrix_completeness_test.go",
        "naming_lint_test.go",
        "residual_gate_test.go",
        "skip_reason_classifier_test.go",
        "spec_annotation_lint_test.go",
        "spec_citation_resolution_test.go",
        "spec_defs_test.go",
        "spec_map_exception_blocker_retention_test.go",
        "spec_map_operability_packages_test.go",
        "spec_map_slot_address_registration_test.go",
        "spec_page_gates_test.go",
        "spec_reference_test.go",
        "spec_section_citation_ratchet_test.go",
        "spec_structure_records_test.go",
        "testing_md_change_graph_example_test.go",
    ],
}

# Fixture trees, by the package whose tests read them.
TESTDATA = {
    "adapter-proto-event-taxonomy": "lint",
    "adapter-proto-pointers": "lint",
    "citation-resolution": "structure",
    "degradation-lock-citation-form": "structure",
    "erasure-exemption": "lint",
    "gate-integrity": "structure",
    "identifier-resolution": "structure",
    "line-citations": "structure",
    "naming-lint": "structure",
    "residual": "structure",
}

ROOT = "tests/static"
PROTO_HELPER = "adapter_proto_parse_test.go"
PROTO_PKG = ROOT + "/internal/adapterproto"
HISTORICAL = (
    "changes/", "proposals/", "BUILD-GAPS.md", "TEST-GAPS.md", "CODE-REVIEW.md",
    "CHANGELOG.md", "gateway-runtime-comms", "PROPOSAL-QUEUE.md",
)

# The shared proto parse, renamed to the exported names of its package.
PROTO_NAMES = [
    ("adapterProtoPath", "adapterproto.Path"),
    ("protoFieldDecl", "adapterproto.FieldDecl"),
    ("protoFields(", "adapterproto.Fields("),
    ("protoServiceRequests(", "adapterproto.ServiceRequests("),
]


def git(*args):
    return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout


def owner(name):
    for pkg, files in CATEGORY.items():
        if name in files:
            return pkg
    return None


def move():
    files = sorted(f for f in os.listdir(ROOT) if f.endswith(".go"))
    left = [f for f in files if owner(f) is None and f != PROTO_HELPER]
    if left:
        raise SystemExit("unassigned test files: %s" % left)
    for pkg, names in CATEGORY.items():
        os.makedirs(os.path.join(ROOT, pkg), exist_ok=True)
        for f in names:
            git("mv", os.path.join(ROOT, f), os.path.join(ROOT, pkg, f))
    git("mv", os.path.join(ROOT, "errorprop"), os.path.join(ROOT, "lint", "errorprop"))
    for sub, pkg in TESTDATA.items():
        os.makedirs(os.path.join(ROOT, pkg, "testdata"), exist_ok=True)
        git("mv", os.path.join(ROOT, "testdata", sub), os.path.join(ROOT, pkg, "testdata", sub))
    os.makedirs(PROTO_PKG, exist_ok=True)
    git("mv", os.path.join(ROOT, PROTO_HELPER), os.path.join(PROTO_PKG, "adapterproto.go"))


def rewrite_packages():
    for pkg, names in CATEGORY.items():
        for f in names:
            p = os.path.join(ROOT, pkg, f)
            s = open(p, encoding="utf-8").read()
            s = re.sub(r"^package static\b", "package " + pkg, s, count=1, flags=re.M)
            if any(old in s for old, _ in PROTO_NAMES):
                for old, new in PROTO_NAMES:
                    s = s.replace(old, new)
                s = s.replace('\n\t"github.com/lennylabs/lenny/tests/testinfra/schematest"',
                              '\n\t"github.com/lennylabs/lenny/tests/static/internal/adapterproto"'
                              '\n\t"github.com/lennylabs/lenny/tests/testinfra/schematest"', 1)
            open(p, "w", encoding="utf-8").write(s)
    p = os.path.join(PROTO_PKG, "adapterproto.go")
    s = open(p, encoding="utf-8").read()
    s = s.replace("package static\n", "// Package adapterproto parses the text of the gateway-adapter protocol for\n"
                  "// the static-tier gates that read it. It carries no spec: annotation: it\n"
                  "// is test tooling and decides no specified behavior.\npackage adapterproto\n", 1)
    s = s.replace("// The adapter proto's text parse, shared by the static-tier gates that read it.",
                  "// The adapter proto's text parse, shared by the static-tier gates that read it.")
    s = s.replace("// adapterProtoPath is the repo-relative path", "// Path is the repo-relative path")
    s = s.replace("const adapterProtoPath =", "const Path =")
    s = s.replace("// protoFieldDecl is one field declaration", "// FieldDecl is one field declaration")
    s = s.replace("type protoFieldDecl struct", "type FieldDecl struct")
    s = s.replace("// protoFields returns, per message,", "// Fields returns, per message,")
    s = s.replace("func protoFields(body string) map[string]map[string]protoFieldDecl {",
                  "func Fields(body string) map[string]map[string]FieldDecl {")
    s = s.replace("protoFieldDecl", "FieldDecl")
    s = s.replace("// protoServiceRequests returns the set", "// ServiceRequests returns the set")
    s = s.replace("func protoServiceRequests(", "func ServiceRequests(")
    open(p, "w", encoding="utf-8").write(s)


def path_map():
    pairs = []
    for pkg, names in CATEGORY.items():
        for f in names:
            pairs.append(("tests/static/" + f, "tests/static/%s/%s" % (pkg, f)))
    for sub, pkg in TESTDATA.items():
        pairs.append(("tests/static/testdata/" + sub, "tests/static/%s/testdata/%s" % (pkg, sub)))
    pairs.append(("tests/static/errorprop", "tests/static/lint/errorprop"))
    pairs.append(("tests/static/" + PROTO_HELPER, PROTO_PKG + "/adapterproto.go"))
    return pairs


def rewrite_paths():
    pairs = path_map()
    pattern = re.compile("|".join(re.escape(a) + r"(?![\w-])" for a, _ in sorted(pairs, key=lambda p: -len(p[0]))))
    lookup = dict(pairs)
    for path in git("ls-files").split("\n"):
        if not path or path.startswith(HISTORICAL) or "/testdata/" in "/" + path:
            continue
        if not os.path.isfile(path) or os.path.islink(path):
            continue
        try:
            text = open(path, encoding="utf-8").read()
        except UnicodeDecodeError:
            continue
        new = pattern.sub(lambda m: lookup[m.group(0)], text)
        if new != text:
            open(path, "w", encoding="utf-8").write(new)


def use_slices_contains():
    """Replace the chart package's contains helper in the structure package.

    spec_map_operability_packages_test.go called the contains helper that
    airgap_registry_render_test.go declares; the standard library's
    slices.Contains does the same.
    """
    p = os.path.join(ROOT, "structure", "spec_map_operability_packages_test.go")
    s = open(p, encoding="utf-8").read()
    s = re.sub(r"(?<![\w.])contains\(", "slices.Contains(", s)
    s = s.replace('import (\n', 'import (\n\t"slices"\n', 1)
    open(p, "w", encoding="utf-8").write(s)


def deepen_relative_paths():
    """Add one level to the repository-relative paths the moved tests open.

    A Go test runs in its package directory, and each package now sits one
    directory below tests/static, so "../../charts/lenny" becomes
    "../../../charts/lenny".
    """
    for pkg in list(CATEGORY) + ["lint/errorprop"]:
        d = os.path.join(ROOT, pkg)
        for name in os.listdir(d):
            if not name.endswith(".go"):
                continue
            p = os.path.join(d, name)
            s = open(p, encoding="utf-8").read()
            new = s.replace('"../../', '"../../../').replace("`../../", "`../../../")
            if new != s:
                open(p, "w", encoding="utf-8").write(new)


def main():
    move()
    os.rmdir(os.path.join(ROOT, "testdata"))
    rewrite_packages()
    use_slices_contains()
    deepen_relative_paths()
    rewrite_paths()
    subprocess.run(["gofmt", "-w", ROOT], check=True)


if __name__ == "__main__":
    main()
