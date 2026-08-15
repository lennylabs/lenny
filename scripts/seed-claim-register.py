#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Seed the claim register from the reference document's status table.

WHAT THE REGISTER IS FOR. Specification §28.4 requires every normative statement
§28 makes about a mechanism to carry a row here, so the specification cannot
assert a mechanism that does not run. A row that is not `WIRED` names the step
that closes it, which makes the register the work queue for the steps that
follow.

WHERE THE ROWS COME FROM. `gateway-runtime-comms.md` §7.1, "Status of every
mechanism named in this document", is the enumeration the reference already made
and evidenced: each row names a mechanism, its status, and the `file:line`
citation that was opened to establish it. Re-deriving that from the tree would be
a second investigation with no better evidence than the first.

WHY A COMPOUND STATUS BECOMES TWO ROWS. The reference labels a mechanism per side
of the boundary it crosses, so "server WIRED, client UNWIRED" is one line about
two mechanisms. §28.4's set holds one status per row, and the two halves are
separately true and separately closed, so each becomes its own row.

WHY THE DEFERRAL FALLS BACK TO R24. A row that is not `WIRED` names the step that
closes it. Where the plan has a step for the mechanism the mapping names it, and
where it does not the row names R24, "disposition of the remaining unwired
surfaces", which is the step the plan defines for exactly this remainder. The
fallback is visible in the output rather than silent, so a row that deserves a
specific step can be moved to one.

Usage:
    seed-claim-register.py --out tests/claim-map.json [--print]
"""

import argparse
import json
import re
import sys

REFERENCE = "gateway-runtime-comms.md"
SECTION = "### 7.1 Status of every mechanism named in this document"

STATUS = re.compile(r"\b(WIRED|UNWIRED|ABSENT|UNVERIFIED)\b")

# A status cell naming a side before its label describes that side alone.
SIDED = re.compile(
    r"\b(server|client|gateway|pod client|producer|consumer|binary|deployment)\b"
    r"[^,]*?\b(WIRED|UNWIRED|ABSENT|UNVERIFIED)\b", re.I)

# Which plan step closes a mechanism, by the words the reference uses for it.
# The order is significant: the first match wins, so a more specific rule sits
# above a more general one.
DEFERRAL_RULES = [
    (r"off.holder", "R21"),
    (r"mtls|certificate|client identity|peercred|spiffe", "R14"),
    (r"kubelet|probe|podspec|chart|deployment boundary", "R6"),
    (r"runtimeops|runtime.operations|runtime ops", "R17"),
    (r"runtime.frame|frame schema|schema conformance", "R15"),
    (r"llm proxy|proxy dialect|responses|proxy .*lease", "R11"),
    (r"evict|quiesce|generation (gap|fence)|in.flight rpc|poddisruptionbudget|pdb|drain",
     "R16"),
    (r"coordinator (resolution|address)|coordinator_address|bind|mirror|coordlease"
     r"|routing (read|cache)|getbysession", "R13"),
    (r"inter.replica|gateway.to.gateway", "R18"),
    (r"cross.replica|forward", "R19"),
    (r"durabilit|input wait|message plane|inbox|redeliver|ready_for_input"
     r"|single.consumer|attach", "R20"),
    (r"slot|resume|restore", "R22"),
    (r"pre.?connect|sdk.warm", "R23"),
    (r"health", "R23"),
    (r"lifecycle channel|adapter.to.gateway|gatewaycontrol|control (stream|direction)"
     r"|hold state|service.account token", "R12"),
]

# The reference document is frozen at a point before the channel rename, so its
# citations spell identifiers the way the tree no longer does. The register is a
# live document about the current tree rather than a copy of a frozen one, so
# each citation is carried over at the canonical spelling the §28.3 naming table
# declares. Leaving the retired spellings in would give every renamed identifier
# a second live spelling, which is the state the identifier gate exists to
# prevent.
RENAMES = [
    ("controlchannel", "adapterevents"),
    ("lifecyclechannel", "runtimeops"),
    ("lifecycle-events", "runtime-ops-events"),
    ("lifecycle-socket", "runtime-ops-socket"),
    ("@lenny-lifecycle", "@lenny-runtime-ops"),
    ("LifecycleChannel", "AdapterEvents"),
    ("lifecycleChannel", "runtimeOps"),
]


def canonical(text):
    for retired, current in RENAMES:
        text = text.replace(retired, current)
    return text


# Rows the proposal writes explicitly, because no status table carries them.
EXPLICIT = [
    {
        "claim": "Adapter metric names carrying the retired channel spelling",
        "status": "ABSENT",
        "deferral_id": "R12",
        "surface": "pkg/adapter/metrics.go:71, pkg/adapter/metrics.go:79",
        "note": "the two metric names keep the retired spelling until R12 adds the "
                "adapter metrics endpoint and the catalog entries",
    },
    {
        "claim": "Agent podspec mTLS certificate material",
        "status": "ABSENT",
        "deferral_id": "R14",
        "surface": "spec/04_system-components.md §4.4",
        "note": "the agent-pod mTLS client identity step supplies the certificate volumes",
    },
    {
        "claim": "Runtime-operations events schema asserted by the external-adapter "
                 "compliance suite",
        "status": "ABSENT",
        "deferral_id": "R8",
        "surface": "cmd/lenny-compliance/schemavalidate.go",
        "note": "the suite compiles two schema files and reads no third, while "
                "spec/24, the adapter-contract reference, and the publishing guide "
                "all name schemas/runtime-ops-events.schema.json as an artifact it asserts against",
    },
]

# One row per request-message field the specification adds, each naming the step
# the plan assigns as the field's reader. The field does not exist in the tree
# before that change, so no status table can carry a row for it.
GENERATION_FENCE = [
    "InterruptRequest", "SignalDeadlineRequest", "ReportUsageRequest",
    "CheckpointBarrierRequest", "ResumeRequest", "AttachRequest",
    "SendMessageRequest", "ShutdownRequest", "CheckpointRequest",
    "RotateCredentialsRequest", "ExtendCredentialLeaseRequest",
    "RevokeCredentialsRequest", "ExportPathsRequest",
]
SLOT_BEARING = [
    "InterruptRequest", "SignalDeadlineRequest", "ReportUsageRequest",
    "CheckpointBarrierRequest",
]


def rows_of_section(path, heading):
    """The table rows under the named heading, as lists of cells."""
    lines = open(path, errors="ignore").read().splitlines()
    try:
        start = next(i for i, l in enumerate(lines) if l.strip() == heading)
    except StopIteration:
        raise SystemExit(f"{path}: heading not found: {heading}")
    out = []
    for l in lines[start + 1:]:
        if l.startswith("|"):
            cells = [c.strip() for c in l.strip("|").split("|")]
            if not all(set(c) <= set(":- ") for c in cells):
                out.append(cells)
        elif out:
            break
    return out[1:] if out else []


def deferral_for(mechanism):
    for pat, step in DEFERRAL_RULES:
        if re.search(pat, mechanism, re.I):
            return step, False
    # R24 is the plan's own step for the surfaces no earlier step claims.
    return "R24", True


def split_status(cell):
    """The (side, status) pairs a status cell states."""
    sides = SIDED.findall(cell)
    if len(sides) > 1:
        return [(s.lower(), st.upper()) for s, st in sides]
    found = STATUS.findall(cell)
    if not found:
        return []
    if len(set(found)) > 1:
        # Two labels with no side word, as in "WIRED in the binary, UNWIRED at
        # deployment": keep both, named by the clause each sits in.
        parts = [p.strip() for p in re.split(r",|;", cell) if STATUS.search(p)]
        return [(re.sub(r"\b(WIRED|UNWIRED|ABSENT|UNVERIFIED)\b", "", p).strip(" .`") or "overall",
                 STATUS.search(p).group(1).upper()) for p in parts]
    return [(None, found[0].upper())]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True)
    ap.add_argument("--print", action="store_true", dest="show")
    args = ap.parse_args()

    claims, fallbacks, dropped = [], [], []
    for cells in rows_of_section(REFERENCE, SECTION):
        if len(cells) < 3:
            continue
        mechanism, status_cell, citation = cells[0], cells[1], cells[2]
        pairs = split_status(status_cell)
        if not pairs:
            # A cell with no label states something other than a status, such as
            # a mechanism retired by design. It carries no claim to verify.
            dropped.append((mechanism, status_cell))
            continue
        for side, status in pairs:
            if status == "UNVERIFIED":
                # §28.4's set has no UNVERIFIED. The reference uses it for what
                # it could not establish, which is not a statement the
                # specification makes, so it carries no row.
                dropped.append((mechanism, status_cell))
                continue
            claim = mechanism if side is None else f"{mechanism} ({side})"
            row = {"claim": canonical(claim), "status": status, "surface": canonical(citation)}
            if status != "WIRED":
                step, fell_back = deferral_for(mechanism + " " + (side or ""))
                row["deferral_id"] = step
                if fell_back:
                    fallbacks.append(claim)
            claims.append(row)

    claims.extend(EXPLICIT)
    for msg in GENERATION_FENCE:
        claims.append({
            "claim": f"{msg}.coordination_generation generation fence field",
            "status": "UNWIRED",
            "deferral_id": "R16",
            "surface": "schemas/lenny-adapter.proto",
            "note": "the field is carried on the request and no production reader "
                    "compares it until the generation fence lands",
        })
    for msg in SLOT_BEARING:
        claims.append({
            "claim": f"{msg} slot identifier field",
            "status": "UNWIRED",
            "deferral_id": "R22",
            "surface": "schemas/lenny-adapter.proto",
            "note": "the slot dimension is carried and unread until concurrent-slot "
                    "resume and restore lands",
        })
    claims.append({
        "claim": "ResumeRequest.slot_id",
        "status": "UNWIRED",
        "deferral_id": "R22",
        "surface": "schemas/lenny-adapter.proto",
        "note": "the carved resume path reads the slot identifier when it lands",
    })

    seen = {}
    for c in claims:
        if c["claim"] in seen:
            raise SystemExit(f"duplicate claim: {c['claim']}")
        seen[c["claim"]] = c

    doc = {
        "kind": "claim-register",
        "version": 1,
        "claims": sorted(claims, key=lambda c: c["claim"]),
    }
    with open(args.out, "w") as fh:
        json.dump(doc, fh, indent=2)
        fh.write("\n")

    by_status = {}
    for c in claims:
        by_status[c["status"]] = by_status.get(c["status"], 0) + 1
    print(f"{len(claims)} claim(s) -> {args.out}")
    print("  by status:", by_status)
    print(f"  rows whose deferral fell back to R24: {len(fallbacks)}")
    for f in fallbacks:
        print(f"      {f[:90]}")
    if dropped:
        print(f"  rows carrying no verifiable status: {len(dropped)}")
        for m, s in dropped:
            print(f"      {m[:60]:62} | {s[:44]}")
    if args.show:
        print(json.dumps(doc, indent=2)[:1500])


if __name__ == "__main__":
    sys.exit(main())
