# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Lenny is in early implementation. There are no tagged releases yet. This section is populated as work lands on `main`.

### Added

- Root-level contributor on-ramp: `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `GOVERNANCE.md`, `ROADMAP.md`, and this changelog.
- Implementation Status page in the documentation.
- Gateway binary (`cmd/lenny-gateway`) serving the §15.1 REST session lifecycle: create, start, finalize, interrupt, terminate, resume, delete, derive, replay, upload, messages, transcript, tree, events (SSE), and extend-retention.
- §15.1 admin API: tenant, runtime, user, pool, connector, and circuit-breaker CRUD; bootstrap upsert; the §25.3 health API; the §25.4 self-introspection endpoints; the §25.9 audit-event query API; and platform version/config introspection.
- §15.2 protocol adapters: the MCP JSON-RPC adapter at `/mcp` with the §8.5 platform tool surface, the OpenAI Chat Completions translator, and the Open Responses translator.
- §13.3 Token Service (`POST /v1/oauth/token`, RFC 8693) served in-process.
- §4.9 end-user credential registry and `/v1/credentials` endpoints.
- §11.7 per-tenant audit hash chain recording every admin mutation, with chain verification.
- §16.1 Prometheus metrics at `/metrics`; §15.1 OpenAPI 3.1 document at `/openapi.yaml`.
- §8 recursive-delegation service with cycle detection, isolation monotonicity, and depth limits.
- §5.75 quota interceptor and §11.6 circuit-breaker enforcement on the request path.
- `cmd/lenny-ctl` operator CLI covering the resource-management subset.
- `make run` contributor dev loop: the gateway with in-memory stores and the echo runtime wired as a subprocess.
- Database migration framework: the `migrations/` directory holds the §12 schema, applied with golang-migrate. The schema covers the tenant, runtime, session, transcript, audit, billing, issued-token, user, connector, and pod-state tables; the §12.3 `lenny_tenant_guard` trigger and row-level security policies; the §11.7 `lenny_audit_immutability` and `lenny_billing_immutability` triggers; and the `lenny_app` and `lenny_erasure` role separation.
- Schema linter (`scripts/lint-schema.sh`, R-01) and query linter (`scripts/lint-queries.sh`, R-02).
- Postgres-backed stores for sessions, session transcripts, tenants, runtimes, users, connectors, and issued-token metadata. The gateway selects them with the `--postgres-dsn` flag and serves from in-memory stores otherwise.
- Redis-backed stores selected with the `--redis-url` flag: the §11.6 circuit-breaker registry, the §10.1 session-coordination lease, and the §11.2 token-usage counter.
- §10.1 session-coordination lease sweeper: each gateway replica acquires and renews the lease on every non-terminal session it owns, so a crashed replica's sessions free up on lease expiry.
- §11.7 durable audit hash chain: admin mutations are committed to the `audit_log` table and the per-tenant chain is verifiable through the §25.9 audit-query API.
- §11.7 startup audit-integrity verification: the gateway checks the ledger grants, the integrity triggers, and the erasure-mode guard before serving, and refuses to start in production on a violation.
- §25.3 health checks that probe the live Postgres and Redis backends.
- Reference runtime `cmd/runtimes/streaming-echo` (Full integration level) and the `cmd/lenny-compliance` Basic and Full conformance batteries.

### Changed

- Documentation terminology aligned on "integration levels" (Basic / Standard / Full).
- GitHub organization and repository references consolidated on `lennylabs/lenny`.
- Design-phase status banners added to README, documentation home, and contributing guide.
- The Implementation Status page reflects the shipped early-implementation surface.
- Session identifiers are §12.6 UUIDv8 values.
- The OpenAI Chat Completions and Open Responses translators support streaming responses (`stream: true`).

### Notes

The first tagged release will correspond to the first working slice landing on `main`. See [`ROADMAP.md`](ROADMAP.md) and [`spec/18_build-sequence.md`](spec/18_build-sequence.md).

[Unreleased]: https://github.com/lennylabs/lenny/compare/HEAD
