---
id: CHINTRA-I1
status: open
kind: build-gap
severity: medium
requirements: []
---

# Credential revocation into the session's own file has no gateway caller

## Specification

§28.5.3 (Intra-pod) states that the adapter writes each session's own credential file at `/run/lenny/slots/{sessionId}/credentials.json` (the `CH-RUNTIMEOPS` card, **Preconditions.**, and the `session_start` field `credentialsPath` on the `CH-MSGSOCK` card). No section names the adapter's `RevokeCredentials` RPC, which `schemas/lenny-adapter.proto` defines.

## Evidence

- Claim register row (`tests/claim-map.json`, retired by 0116.S5): claim "Credential revocation addressed to the session's own lease file", status `UNWIRED`, anchor `#2853-intra-pod`, deferral `R14`.
- Surface recorded on the row: "`pkg/adapter/credentials.go` `RevokeCredentials` into `revokeCredentialsSlot` (`pkg/adapter/slotcreds.go`)".
- Note recorded on the row: "the handler drops the named providers from the addressed session's own credential file, and no gateway caller invokes the adapter's RevokeCredentials; the Token Service revocation the gateway does call is a separate service and is not part of this claim".

## Gap

The adapter can drop revoked providers from the addressed session's credential file, and no gateway path asks it to, so a revocation does not reach a running session's file.

## Suggested resolution

The row deferred the work to step R14, "Agent-pod mTLS client identity", of `gateway-runtime-comms-remediation.md`.

0133 ("LU-4 Removal") removes the LLM credential lease and the credentials file for LLM leases, and it keeps emergency revocation of VCS credentials. Its "Validation owed" item "Confirm that no VCS path uses `AssignCredentials` or the adapter's credentials file" decides whether this mechanism survives; if no VCS path uses the file, 0133.S2 makes this issue obsolete.
