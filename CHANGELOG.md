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

### Changed

- Documentation terminology aligned on "integration levels" (Basic / Standard / Full).
- GitHub organization and repository references consolidated on `lennylabs/lenny`.
- Design-phase status banners added to README, documentation home, and contributing guide.
- The Implementation Status page reflects the shipped early-implementation surface.

### Notes

The first tagged release will correspond to the first working slice landing on `main`. See [`ROADMAP.md`](ROADMAP.md) and [`spec/18_build-sequence.md`](spec/18_build-sequence.md).

[Unreleased]: https://github.com/lennylabs/lenny/compare/HEAD
