# Evidence for `0143-service-account-credentials`

No test ran for this design. The file below is the record of a read-only review of 2026-11-15. It is not product code, and it carries no specification citation.

| File | What it is |
| :-- | :-- |
| `security-review.md` | The adversarial security review of `0143-service-account-credentials` revision 1 and of the changes that revision made to `0151-user-credential-delivery` revision 39, `0127-egress-and-git` revision 91, `0124-connector-removal` revision 6, `0152-user-git-identity` revision 44, and `0149-tenant-scoping-identity` revision 41. It ranks ten findings by severity, lists the parts it found sound, and records which external claims it checked against live pages. The owner answered its summary on 2026-11-15 with "O1-O7: go with your recommendations", and the design's revision 2 applies every finding. It is kept verbatim as the record of that review, so its citations name the text of revision 1. |

The review read the designs, the specification sections §10.2 and §13.3, and `0144-session-derive`. It edited no file and ran nothing against a provider.
