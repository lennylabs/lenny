## Implementation checklist

Every step lands the tests that the non-spec Testing section places under its deliverables. S1, S4, S6, and S9 land in one spec pass (see the spec-changes **Design** section). S26 is retired with CHART-3, and the later steps keep their numbers. S33 (CODE-13) is listed after S19 because it depends only on S11, and S29 depends on it.

- [ ] **S1 · spec** — SPEC-1. Lands all of SPEC-1: the signing model, the signing topology, and the edits that follow from them.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-2. Lands all of SPEC-2: the verifier key set and rotation paragraph.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. Lands all of SPEC-3: the fixed lifetime, the `auth.capabilityTokenTtlSeconds` default, and the request `expires_in` row.
      Tiers 0, 11. Depends on: S1
- [ ] **S4 · spec** — SPEC-4. Lands all of SPEC-4: the KMS signing failure mode and its error row.
      Tiers 0, 11. Depends on: S1, S2
- [ ] **S5 · spec** — SPEC-5. Lands all of SPEC-5: the signing metrics and alerts.
      Tiers 0, 11. Depends on: S4
- [ ] **S6 · spec** — SPEC-6. Lands all of SPEC-6: the §4.3 item 3 minting topology and the §17.7 audit-lock remedy.
      Tiers 0, 11. Depends on: S1
- [ ] **S7 · spec** — SPEC-7. Lands all of SPEC-7: signing outside the lock and the in-transaction revocation re-read.
      Tiers 0, 11. Depends on: S1
- [ ] **S8 · spec** — SPEC-8. Lands all of SPEC-8: the lifetime and quota defaults and the KMS Sign rate.
      Tiers 0, 11. Depends on: S3, S4
- [ ] **S9 · spec** — SPEC-9. Lands all of SPEC-9: the local development mode edits.
      Tiers 0, 11. Depends on: S1
- [ ] **S10 · spec** — SPEC-10. Lands all of SPEC-10: the gateway and `lenny-ops` KMS endpoint egress.
      Tiers 0, 11. Depends on: S1
- [ ] **S11 · spec** — SPEC-11. Lands all of SPEC-11: the build-sequence bullet and the `lenny-ops` edits, including the revocation set of owner decision 4 and the readiness probe of owner decision 6.
      Tiers 0, 11. Depends on: S2
- [ ] **S12 · code** — CODE-1. Lands the KMS seam in every adapter and the reference validation.
      Tiers 0, 1, 6. Depends on: S1, S2, S4, S9
- [ ] **S13 · code** — CODE-2. Lands the context-taking `Sign`, the remote ES256 signer, and `KeySet`.
      Tiers 0, 1. Depends on: S1, S2, S12
- [ ] **S14 · code** — CODE-3. Lands the breaker backpressure changes.
      Tiers 0, 1. Depends on: S4, S5, S13
- [ ] **S15 · code** — CODE-10. Lands the JWKS endpoint and rotation observer deletion.
      Tiers 0, 1, 4. Depends on: S2, S11
- [ ] **S16 · code** — CODE-4. Lands the gateway wiring.
      Tiers 0, 1, 2, 4, 9. Depends on: S1, S2, S6, S13, S14, S15
- [ ] **S17 · code** — CODE-5. Lands the Token Service wiring.
      Tiers 0, 1, 2, 4, 9. Depends on: S1, S2, S6, S13, S14
- [ ] **S18 · code** — CODE-6. Lands the `lenny-ops` wiring, including the key set in every process and the revocation set.
      Tiers 0, 1, 2, 4, 5, 9. Depends on: S1, S2, S11, S13
- [ ] **S19 · code** — CODE-12. Lands the deletion of the HMAC signer and `RotatingVerifier`.
      Tiers 0, 1. Depends on: S16, S17, S18
- [ ] **S33 · code** — CODE-13. Lands the either-dependency permissive `/healthz`, the harness wait on `/readyz`, the production readiness probe on `/healthz`, the regenerated embedded manifests, and their tier-1, Helm, and embedded-manifest sync tests.
      Tiers 0, 1, 4, 5, 11. Depends on: S11
- [ ] **S20 · code** — CODE-7. Lands the outcome mapping and the sign metrics.
      Tiers 0, 1, 2, 3, 7a. Depends on: S4, S5, S8, S14, S16, S17
- [ ] **S21 · code** — CODE-8. Lands the lifetime flag rename on the gateway and the Token Service default lifetime.
      Tiers 0, 1, 3, 5. Depends on: S3, S16, S17
- [ ] **S22 · code** — CODE-9. Lands the in-transaction revocation re-read.
      Tiers 0, 1, 2, 7a, 9. Depends on: S7, S17
- [ ] **S23 · code** — CODE-11. Lands the alert catalog change and its generated files.
      Tiers 0, 1, 11. Depends on: S5
- [ ] **S24 · code** — CHART-1. Lands the values, schema, and preset changes.
      Tiers 0, 1, 5. Depends on: S1, S3, S8, S21
- [ ] **S25 · code** — CHART-2. Lands the local master key Secret and the KMS flags on all three deployments.
      Tiers 0, 2, 5, 11. Depends on: S1, S9, S18, S24
- [ ] **S27 · code** — CHART-4. Lands the gateway and `lenny-ops` KMS egress rule and the cloud-provider `kms.endpointCIDR` render refusal.
      Tiers 0, 5, 9. Depends on: S10, S25
- [ ] **S28 · code** — CHART-5. Lands the PrometheusRule rendering.
      Tiers 0, 11. Depends on: S5, S23, S24
- [ ] **S29 · test** — TEST-1. Lands the harness change and the cross-process tests.
      Tiers 0, 4, 5, 9, 11. Depends on: S16, S17, S18, S19, S22, S25, S33
- [ ] **S30 · docs** — DOCS-1. Lands the runbook rewrite.
      Tiers 0, 11. Depends on: S1, S2, S23
- [ ] **S31 · docs** — DOCS-2. Lands the reference, operator guide, error catalog, and audit-pipeline-degraded runbook edits.
      Tiers 0, 11. Depends on: S1, S3, S4, S6, S8, S11
- [ ] **S32 · docs** — REC-1. Lands the BUILD-GAPS and TEST-GAPS records.
      Tiers 0, 11. Depends on: S29, S30, S31
