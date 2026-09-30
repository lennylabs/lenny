# Implementation checklist: Keep the pod's runtime listener across a session teardown

## Implementation checklist

- [ ] **S1 · code** — CODE-1 and CODE-3 (non-spec-changes.md §7.1, §7.7).
      Tiers 0, 1. Landing order: summary **Decisions.**
- [ ] **S2 · code** — CODE-2. `CloseListener` on the concrete type, and its call site in
      `cmd/lenny-adapter/main.go`.
      Tiers 0, 1. Depends on: S1.
- [ ] **S3 · test** — TEST-1 through TEST-4 and TEST-9. The tier-1 battery in `pkg/adapter`, and the
      conversion of the existing socket cases' cleanups to `CloseListener`.
      Tiers 0, 1. Depends on: S2.
- [ ] **S4 · test** — TEST-5 and TEST-8, and §7.5's cleanup conversion in the tier-4 files.
      Tiers 0, 4. Depends on: S2.
- [ ] **S5 · test** — TEST-6 and TEST-7. The tier-7a race case, and the drain fixture's accept-timeout
      bound, tightened assertion, and §7.5 cleanup conversion.
      Tiers 0, 7a. Depends on: S2.
- [ ] **S6 · docs** — DOCS-1. The runtime-author lifecycle page states the socket address's lifetime.
      Tiers 0, 11. Depends on: S1.
