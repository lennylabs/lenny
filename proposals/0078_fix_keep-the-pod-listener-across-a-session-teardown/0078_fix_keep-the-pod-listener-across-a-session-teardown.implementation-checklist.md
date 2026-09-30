# Implementation checklist: Keep the pod's runtime listener across a session teardown

## Implementation checklist

- [ ] **S1 · code** — CODE-1. `Close` stops closing the listener, and the type, `Close`, and constructor
      doc comments state the listener's pod lifetime.
      Tiers 0, 1. Depends on: proposal 0073's code phase completing through S18.
- [ ] **S2 · code** — CODE-2. `CloseListener` on the concrete type, wired into the adapter's signal
      goroutine after `srv.GracefulStop()`.
      Tiers 0, 1. Depends on: S1.
- [ ] **S3 · test** — TEST-1 through TEST-4. The tier-1 battery in `pkg/adapter`, and the conversion of
      the existing socket cases' cleanups to `CloseListener`.
      Tiers 0, 1. Depends on: S2.
- [ ] **S4 · test** — TEST-5. The tier-4 case for two sequential sessions over one bound address, and the
      cleanup conversion in the two existing tier-4 cases.
      Tiers 0, 4. Depends on: S2.
- [ ] **S5 · test** — TEST-6 and TEST-7. The tier-7a race case, and the drain fixture's accept-timeout
      bound and tightened assertion.
      Tiers 0, 7a. Depends on: S2.
- [ ] **S6 · docs** — DOCS-1. The runtime-author lifecycle page states the socket address's lifetime.
      Tiers 0, 11. Depends on: S1.
