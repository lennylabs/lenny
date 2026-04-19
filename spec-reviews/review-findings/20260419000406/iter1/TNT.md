### TNT-001 `noEnvironmentPolicy` Default Not Enforced at Gateway Startup [High]
**Files:** `10_gateway-internals.md`, `09_environments.md`, `11_policy-and-controls.md`

The spec establishes `noEnvironmentPolicy: deny-all` as the normative default when a session does not name an environment, but the gateway startup path does not perform a validation step that guarantees the platform-level `noEnvironmentPolicy` (or per-tenant override) is configured before the gateway marks itself ready. A misconfigured Helm deployment that omits `noEnvironmentPolicy` from values and skips a bootstrap config map could start with undefined behavior — the gateway would fall through to an implementation default that may or may not match `deny-all`.

Compare with `defaultMaxSessionDuration` and `auth.oidc.*` which have explicit "must be set; gateway refuses to start without" language. `noEnvironmentPolicy` does not have an equivalent guard, even though it gates the blast radius of every untagged request.

**Recommendation:** Add an explicit gateway readiness requirement in §10: "The gateway MUST validate that a platform-level `noEnvironmentPolicy` is configured (either `deny-all` or a named policy reference) during startup. If the setting is missing, the gateway MUST refuse to become ready and MUST emit a `LENNY_CONFIG_MISSING` structured log entry identifying the `noEnvironmentPolicy` key." Cross-reference from §11 (Policy) and §9 (Environments) to this normative startup check.
