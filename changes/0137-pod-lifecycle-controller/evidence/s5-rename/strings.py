# SPDX-License-Identifier: MIT
"""Rename the retired pod lifecycle kind names in text (0137.S5).

goident.go renames Go identifiers through the AST. This script renames the
same names where they appear as text: Go comments and string literals, YAML,
Markdown, SQL, shell, and every other tracked text file in scope. It applies
the identifier renames goident.go recorded in renames.tsv, then the
resource, webhook, metric, label, field-manager, runbook, and kind renames
of "PLC-2" in 0137's change.md.

Usage, from the repository root:

    python3 changes/0137-pod-lifecycle-controller/evidence/s5-rename/strings.py --dry-run
    python3 changes/0137-pod-lifecycle-controller/evidence/s5-rename/strings.py

--dry-run prints each rule's matches with their context so the rules can be
reviewed before they are applied. Paths can be given to limit the run.

Out of scope: the change records, the staged proposals, testdata fixtures,
the Architecture Decision Records (0125 removes them), the root-level audit
and planning records, CHANGELOG.md, and the historical migrations, all of
which record what was true when they were written. A Go comment line that
opens an indented `// spec:` annotation is left as it is, because the
annotation lint refuses a changed annotation inside a function body or on a
struct field.
"""
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))

EXCLUDE_PREFIXES = (
    "changes/", "proposals/", "docs/adr/", ".claude/", "designs/",
)
EXCLUDE_FILES = {
    "BUILD-GAPS.md", "TEST-GAPS.md", "PROPOSAL-QUEUE.md", "CODE-REVIEW.md",
    "BUILD-PLAN.md", "BUILD-PROGRESS.md", "CHANGELOG.md",
    "gateway-runtime-comms.md", "gateway-runtime-comms-remediation.md",
    # These name other products' sandboxes and GKE Sandbox in the isolation
    # sense; their kind names, if any, are edited by hand.
    "docs/about/comparisons.md", "charts/lenny/answers/catalog/gke-production.yaml",
}
TEXT_EXT = {
    ".go", ".md", ".yaml", ".yml", ".json", ".sql", ".sh", ".py", ".tpl",
    ".tmpl", ".txt", ".svg", ".toml", ".proto", ".mjs", ".js", ".ts", ".tsx",
    ".html", ".cfg", ".conf", ".rego", ".jsonl", ".csv", ".tsv", "",
}

# Identifiers kept in the isolation sense of the specification's sandbox
# isolation profile, the pkg/sandbox package path,
# and product names. They are masked before the rules run.
PROTECT = [
    "GKE Sandbox", "gVisor Sandbox", "Sandbox option",
    "assertScheduledOnSandboxGvisorNode", "TestT4SandboxKMSFailClosedTestbed",
    "sandbox_proxy_testbed", "sandboxcond", "sandboxstate",
]


def load_identifier_renames():
    out = []
    with open(os.path.join(HERE, "renames.tsv")) as f:
        for line in f:
            old, new = line.rstrip("\n").split("\t")
            # Lower-case single words ("sandbox", "sandboxes", "sbx") are
            # ordinary prose words outside Go; the rules below handle
            # them in code contexts only.
            if old in ("sandbox", "sandboxes", "sbx", "sandboxclaim_guard"):
                continue
            out.append((re.compile(r"(?<![A-Za-z0-9_])" + re.escape(old) + r"(?![A-Za-z0-9_])"), new, "id:" + old))
    # Longest first, so a longer identifier is not split by a shorter one.
    out.sort(key=lambda r: -len(r[2]))
    return out


def R(pattern, repl, name=None):
    return (re.compile(pattern), repl, name or pattern)


RULES = [
    # Package and file paths.
    R(r"pkg/controller/sandbox\b", "pkg/controller/agentpod"),
    R(r"controller/sandbox/", "controller/agentpod/"),
    R(r"pkg/admission/sandboxclaim_guard", "pkg/admission/podclaim_guard"),
    # D14: the claim binding-state machine moves with the kind.
    R(r"pkg/sandboxclaim\b", "pkg/podclaim"),
    R(r"\bsandboxclaim_guard\b", "podclaim_guard"),
    R(r"\bsandboxclaim_guard(_test)?\.go\b", r"podclaim_guard\1.go"),
    R(r"\bsandboxtemplate_deletion_guard(_test)?\.go\b", r"podset_deletion_guard\1.go"),
    R(r"\bsandboxtemplate_deletion_guard\b", "podset_deletion_guard"),
    R(r"\bagentsandbox_errorpaths_test\.go\b", "crd_errorpaths_test.go"),
    R(r"\bagentsandbox(_test)?\.go\b", r"crd\1.go"),
    R(r"\bsandbox_types\.go\b", "agentpod_types.go"),
    R(r"\bsandboxclaim_types\.go\b", "podclaim_types.go"),
    R(r"\bsandboxtemplate_types\.go\b", "podset_types.go"),
    R(r"\bsandboxwarmpool_types\.go\b", "podset_types.go"),
    R(r"\bsandbox_workspace_plan_test\.go\b", "agentpod_workspace_plan_test.go"),
    R(r"\bsandboxtemplate_runtime_process_test\.go\b", "podset_runtime_process_test.go"),
    # Webhooks, chart template files, metrics, labels, field managers.
    R(r"sandboxclaim-guard", "podclaim-guard"),
    R(r"sandboxtemplate-deletion-guard", "podset-deletion-guard"),
    R(r"lenny_sandboxclaim_guard_rejections_total", "lenny_podclaim_guard_rejections_total"),
    R(r"lenny_sandbox_finalizer_terminating_seconds", "lenny_agentpod_finalizer_terminating_seconds"),
    R(r"lenny\.dev/sandbox-ready", "lenny.dev/agent-pod-ready"),
    R(r"lenny-sandbox-cond-", "lenny-agentpod-cond-"),
    # Runbooks.
    R(r"\bsandbox-claim-race\b", "pod-claim-race"),
    R(r"\bsandbox-finalizer-hang\b", "agent-pod-finalizer-hang"),
    # Resources and CRD names.
    R(r"\bsandboxwarmpools\b", "podsets"),
    R(r"\bsandboxtemplates\b", "podsets"),
    R(r"\bsandboxclaims\b", "podclaims"),
    R(r"\bsandboxwarmpool\b", "podset"),
    R(r"\bsandboxtemplate\b", "podset"),
    R(r"\bsandboxclaim\b", "podclaim"),
    R(r"(?<![A-Za-z])sandboxes(?=\.lenny\.dev|/status|/finalizers|`|\"|'|\]|,|\s*$|\.yaml)", "agentpods", "sandboxes-resource"),
    R(r"(?<=kubectl get )sandboxes\b", "agentpods"),
    R(r"(?<=kubectl get )sandbox\b", "agentpod"),
    R(r"(?<=lenny\.dev_)sandboxes\b", "agentpods"),
    R(r"(?m)(?<=^\s{2}- )sandboxes$", "agentpods"),
    R(r"(?m)(?<=^\s{4}- )sandboxes$", "agentpods"),
    R(r"(?m)(?<=^\s{6}- )sandboxes$", "agentpods"),
    R(r"(?<=resources: \[)\"?sandboxes", "agentpods"),
    # Short names.
    R(r"\bsbxwp\b", "pset"),
    R(r"\bsbxt\b", "pset"),
    R(r"\bsbxc\b", "pclaim"),
    # Fields.
    R(r"\bsandboxRef\b", "agentPodRef"),
    R(r"(?<=Sandbox\.spec\.)poolRef\b", "podSetRef"),
    R(r"(?<=AgentPod\.spec\.)poolRef\b", "podSetRef"),
    # Upstream-free type names of PLC-5 in prose.
    R(r"\bAgentSandbox(?=[A-Z])", "CRD"),
    # Kind names, plural first.
    R(r"\bSandboxWarmPools\b", "PodSets"),
    R(r"\bSandboxTemplates\b", "PodSets"),
    R(r"\bSandboxClaims\b", "PodClaims"),
    R(r"(?<![A-Za-z])SandboxWarmPool(?![a-z])", "PodSet"),
    R(r"(?<![A-Za-z])SandboxTemplate(?![a-z])", "PodSet"),
    R(r"(?<![A-Za-z])SandboxClaim(?![a-z])", "PodClaim"),
    R(r"(?<![A-Za-z])Sandboxes\b", "AgentPods"),
    R(r"(?<![A-Za-z])Sandbox(?![a-z])", "AgentPod"),
    # Prose for the claim kind.
    R(r"\bsandbox claims\b", "pod claims"),
    R(r"\bsandbox claim\b", "pod claim"),
    # The merge leaves duplicated names where a sentence named both halves.
    R(r"PodSet(s?)( and |/| or | \+ |, )(the |a |its |each )?PodSet(s?)\b", r"PodSet\1", "merge-dup"),
    R(r"PodSet(s?) and (the |a |its )?PodSet(s?)\b", r"PodSet\1", "merge-dup2"),
]

TABLE_RULES = [
    # sandbox_warm_pools outside historical migration file names.
    R(r"(?<![0-9]_)(?<!_)\bsandbox_warm_pools\b(?!_)", "pools", "table"),
]


def tracked_files(paths):
    out = subprocess.run(["git", "ls-files", "-z", "--"] + paths, capture_output=True, check=True).stdout
    return [p for p in out.decode().split("\x00") if p]


def in_scope(path):
    if path.startswith(EXCLUDE_PREFIXES) or path in EXCLUDE_FILES:
        return False
    if "/testdata/" in path or path.startswith("testdata/"):
        return False
    if path.startswith("migrations/") and path.endswith(".sql"):
        return False
    ext = os.path.splitext(path)[1]
    return ext in TEXT_EXT


SPEC_IN_BODY = re.compile(r"^\s+//\s*spec:")

# Every rule needs one of these substrings on the line it rewrites.
PREFILTER = re.compile(r"(?i)andbox|sbx")


def transform(text, rules, path, report):
    masks = {}
    for i, word in enumerate(PROTECT):
        token = f"\x00P{i}\x00"
        if word in text:
            masks[token] = word
            text = text.replace(word, token)
    go = path.endswith(".go")
    lines = text.split("\n")
    for n, line in enumerate(lines):
        if go and SPEC_IN_BODY.match(line):
            continue
        if not PREFILTER.search(line):
            continue
        new = line
        for rx, repl, name in rules:
            if rx.search(new):
                after = rx.sub(repl, new)
                if after != new and report is not None:
                    report.setdefault(name, []).append(f"{path}:{n+1}: {line.strip()[:160]}")
                new = after
        lines[n] = new
    text = "\n".join(lines)
    for token, word in masks.items():
        text = text.replace(token, word)
    return text


def main(argv):
    dry = "--dry-run" in argv
    paths = [a for a in argv if not a.startswith("--")]
    rules = load_identifier_renames() + RULES
    # The table rename skips test files: several apply the historical
    # migration chain only up to an earlier version, so each is edited by
    # hand against the migration it applies.
    table_ok = lambda p: (p.startswith(("pkg/", "cmd/", "tests/", "scripts/", "docs/", "spec/", "charts/")) or p in ("README.md", "TESTING.md")) and not p.endswith("_test.go")
    report = {}
    changed = 0
    for path in tracked_files(paths):
        if not in_scope(path) or not os.path.isfile(path):
            continue
        try:
            src = open(path, encoding="utf-8").read()
        except UnicodeDecodeError:
            continue
        r = rules + (TABLE_RULES if table_ok(path) else [])
        out = transform(src, r, path, report)
        if out != src:
            changed += 1
            if not dry:
                open(path, "w", encoding="utf-8").write(out)
    for name in sorted(report):
        print(f"== {name}: {len(report[name])}")
        if dry:
            for ctx in report[name][:400]:
                print("   " + ctx)
    print(f"{'would change' if dry else 'changed'} {changed} files")


if __name__ == "__main__":
    main(sys.argv[1:])
