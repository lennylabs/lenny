# SPDX-License-Identifier: MIT
"""Measurements behind change 0117 (gateway package layout).

Assigns each package under pkg/gateway to a part of the proposed layout and
reports sizes, import sites, and the import edges between parts. It also
assigns each non-test file of pkg/gateway/sessionserver to a part.

Usage, from the repository root (see README.md):

  CGO_ENABLED=0 go list -json=ImportPath,Dir,GoFiles,TestGoFiles,XTestGoFiles,Imports,TestImports,XTestImports ./... > /tmp/golist.json
  python3 -I changes/0117-gateway-package-layout/evidence/measure.py /tmp/golist.json . packages
  python3 -I .../measure.py /tmp/golist.json . parts
  python3 -I .../measure.py /tmp/golist.json . edges [FROM TO]
  python3 -I .../measure.py /tmp/golist.json . sessionserver
"""
import collections
import json
import os
import re
import sys

MOD = "github.com/lennylabs/lenny/"
GW = MOD + "pkg/gateway/"

# (prefix under pkg/gateway, part, note). The longest matching prefix wins.
# "removed" packages stay where they are until the named change removes them.
# "domain" packages leave pkg/gateway for the concern directory in the note.
RULES = [
    ("connectors", "removed", "0124"),
    ("experiment", "removed", "0141"),
    ("llmproxy/proxycache", "removed", "0132"),
    ("llmproxy/semanticcache", "removed", "0132"),
    ("llmproxy/credrouter", "removed", "0133"),
    ("credentials/credassign", "removed", "0133"),
    ("credentials/credleasestore", "removed", "0133"),
    ("credentials/credrenewal", "removed", "0133"),
    ("credentials/credfallback", "removed", "0133"),
    ("credentials/leaserevoke", "removed", "0133"),
    ("session/tokenanomaly", "removed", "0133"),
    ("runtime/adapterregistry", "removed", "0138"),
    ("runtime/externaladapterstore", "removed", "0138"),
    ("runtime/agentcard", "removed", "0138"),
    ("environment/translator", "removed", "0138"),
    ("externalapi/outputpartfidelity", "removed", "0138"),
    ("mcpfabric/mcpschemagen", "removed", "0138"),
    ("mcpfabric/mcptools/internal/genmcpschemas", "removed", "0138"),
    ("runtime/statelessproxy", "removed", "0119"),
    ("runtime/statelessrouting", "removed", "0119"),
    ("runtime/tenantaffinity", "removed", "0119"),
    ("runtime/sdkwarm", "removed", "0119"),
    ("mcpfabric/mcpruntimes", "removed", "0119"),
    ("session/memorystore", "removed", "0123"),
    ("core/subsystem", "seammodel", ""),
    ("metrics/extractionthreshold", "seammodel", ""),
    ("core/semver", "domain", "common"),
    ("middleware", "api", ""),
    ("middleware/auth", "domain", "auth"),
    ("middleware/idempotency", "domain", "idempotency"),
    ("middleware/environment", "domain", "environment"),
    ("callercheck", "domain", "auth"),
    ("serviceidentity", "api", ""),
    ("externalapi", "api", ""),
    ("externalapi/errorclassify", "domain", "apierrors"),
    ("externalapi/pagination", "domain", "apierrors"),
    ("externalapi/openapi", "domain", "apierrors"),
    ("mcpfabric/playground", "playground", ""),
    ("sessionserver", "core", ""),
    ("podlifecycle/podclaim", "core", ""),
    ("podlifecycle/podsession", "core", ""),
    ("podlifecycle/bindrelease", "core", ""),
    ("podlifecycle/slotreaper", "core", ""),
    ("coordination/coordination", "core", ""),
    ("coordination/coordlease", "core", ""),
    ("coordination/coordfence", "core", ""),
    ("coordination/barrier", "core", ""),
    ("checkpoint/checkpointer", "core", ""),
    ("checkpoint/resumechunks", "core", ""),
    ("session", "core", ""),
    ("storage/dualstore", "core", ""),
    ("storage/leasestore", "core", ""),
    ("storage/slotcounter", "core", ""),
    ("storage/derivelock", "core", ""),
    ("storage/evictionfallback", "core", ""),
    ("runtime/adapterclient", "core", ""),
    ("runtime/adapter", "core", ""),
    ("runtime/slothealth", "core", ""),
    ("runtime/watchdog", "core", ""),
    ("session/sessionevents", "stream", ""),
    ("mcpfabric", "tools", ""),
    ("gatewaycontrol", "tools", ""),
    ("session/inputwait", "tools", ""),
    ("session/toolapproval", "tools", ""),
    ("llmproxy/llmproxy", "metering", ""),
    ("session/sessionbudget", "metering", ""),
    ("podlifecycle/prestop", "replica", "drain"),
    ("podlifecycle/drainreadiness", "replica", "drain"),
    ("metrics", "replica", ""),
    ("operability/health", "replica", "opsendpoints"),
    ("coordination/gatewayleader", "replica", ""),
    ("coordination/pdbwatcher", "replica", ""),
    ("deployment", "replica", ""),
    ("pki/tlsprobe", "replica", ""),
    ("session/sessionstore", "domain", "session"),
    ("session/sessionlogstore", "domain", "session"),
    ("session/sessioncheckpointmeta", "domain", "session"),
    ("session/sessionusage", "domain", "billing"),
    ("session/interactionstore", "domain", "session"),
    ("checkpoint", "domain", "checkpoint"),
    ("audit", "domain", "audit"),
    ("billing", "domain", "billing"),
    ("credentials", "domain", "credential"),
    ("environment", "domain", "tenancy"),
    ("policy", "domain", "policy"),
    ("quota", "domain", "quota"),
    ("runtime", "domain", "runtime"),
    ("storage", "domain", "storage"),
    ("provisioning", "domain", "workspaceplan"),
    ("pki", "domain", "mtls"),
    ("upgrade", "domain", "runtime"),
    ("eventbuffer", "domain", "events"),
    ("operability/recommendations", "domain", "recommendations"),
    ("mcpfabric/delegationpolicystore", "domain", "delegation"),
]
RULES.sort(key=lambda r: -len(r[0]))

# Non-test files of pkg/gateway/sessionserver, by part.
SESSIONSERVER = {
    "api": "route_table request_identity rbac_gate user_gate error_envelope json_body envelope "
           "admission_ratelimit playground_caps service session_response session_read "
           "session_subresources transcript callback route",
    "stream": "events logs",
    "upload": "upload upload_to_session upload_limits upload_abort uploadmetrics",
    "tools": "interactions interactions_resolution toolapproval elicitation_expiry tree treerecovery "
             "children_reattached delegated_child_materialize delegation_seams budget_keys_expired "
             "budget_rearm memory",
    "domain": "experimentrouter eval pools runtimes pool_selection pool_policy_mirror pool_drain_gate "
              "quota credential_preclaim metering",
}


def part_of(path):
    rest = path[len(GW):]
    for prefix, part, note in RULES:
        if rest == prefix or rest.startswith(prefix + "/"):
            return part, note
    return "unassigned", ""


def load(path):
    dec, text, out, i = json.JSONDecoder(), open(path).read(), {}, 0
    while i < len(text):
        while i < len(text) and text[i].isspace():
            i += 1
        if i >= len(text):
            break
        obj, i = dec.raw_decode(text, i)
        out[obj["ImportPath"]] = obj
    return out


def count_lines(directory, files):
    return sum(sum(1 for _ in open(os.path.join(directory, f), errors="replace")) for f in files or [])


def import_sites(root):
    """Map each gateway import path to the set of Go files that import it."""
    pattern = re.compile(r'"(github\.com/lennylabs/lenny/pkg/gateway/[^"]*)"')
    sites = collections.defaultdict(set)
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in (".git", ".claude", "node_modules")]
        for name in filenames:
            if name.endswith(".go"):
                full = os.path.join(dirpath, name)
                for m in pattern.finditer(open(full, errors="replace").read()):
                    sites[m.group(1)].add(os.path.relpath(full, root))
    return sites


def main():
    golist, root, mode = sys.argv[1], sys.argv[2], sys.argv[3]
    pkgs = load(golist)
    gw = sorted(p for p in pkgs if p.startswith(GW))
    part = {p: part_of(p) for p in gw}
    if mode == "packages":
        sites = import_sites(root)
        print("package\tpart\tnote\tgo_files\tcode_lines\ttest_lines\timport_sites_outside_package")
        for p in gw:
            o = pkgs[p]
            rel = p[len(MOD):]
            outside = [f for f in sites.get(p, ()) if os.path.dirname(f) != rel]
            tests = (o.get("TestGoFiles") or []) + (o.get("XTestGoFiles") or [])
            print("%s\t%s\t%s\t%d\t%d\t%d\t%d" % (rel, part[p][0], part[p][1], len(o.get("GoFiles") or []),
                  count_lines(o["Dir"], o.get("GoFiles")), count_lines(o["Dir"], tests), len(outside)))
    elif mode == "parts":
        agg = collections.defaultdict(lambda: [0, 0, 0])
        for p in gw:
            o = pkgs[p]
            tests = (o.get("TestGoFiles") or []) + (o.get("XTestGoFiles") or [])
            a = agg[part[p][0]]
            a[0] += 1
            a[1] += count_lines(o["Dir"], o.get("GoFiles"))
            a[2] += count_lines(o["Dir"], tests)
        for k, v in sorted(agg.items()):
            print("%-10s packages=%3d code=%6d tests=%6d" % (k, v[0], v[1], v[2]))
    elif mode == "edges":
        edges, examples = collections.Counter(), collections.defaultdict(set)
        for p in gw:
            for x in pkgs[p].get("Imports") or []:
                if x in part and part[p][0] != part[x][0]:
                    key = (part[p][0], part[x][0])
                    edges[key] += 1
                    examples[key].add(p[len(GW):] + " -> " + x[len(GW):])
        for (a, b), n in sorted(edges.items()):
            print("%s -> %s: %d" % (a, b, n))
        if len(sys.argv) > 5:
            for line in sorted(examples[(sys.argv[4], sys.argv[5])]):
                print("   ", line)
    elif mode == "sessionserver":
        cls = {f: k for k, v in SESSIONSERVER.items() for f in v.split()}
        d = os.path.join(root, "pkg/gateway/sessionserver")
        tot, nfiles = collections.Counter(), collections.Counter()
        print("file\tpart\tlines")
        for name in sorted(os.listdir(d)):
            if not name.endswith(".go") or name.endswith("_test.go"):
                continue
            k = cls.get(name[:-3], "core")
            n = count_lines(d, [name])
            tot[k] += n
            nfiles[k] += 1
            print("%s\t%s\t%d" % (name, k, n))
        for k in sorted(tot):
            print("# %s files=%d lines=%d" % (k, nfiles[k], tot[k]))


if __name__ == "__main__":
    main()
