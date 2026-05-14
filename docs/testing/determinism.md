---
layout: default
title: "Determinism"
parent: "Testing"
nav_order: 2
description: How to keep tests deterministic — testinfra/timectl for the clock, testinfra/randctl for randomness, no naked Sleep.
---

# Determinism

§17.4 forbids non-deterministic test behavior. The harness ships
two helpers that replace the stdlib calls every test would otherwise
reach for.

## Clock: testinfra/timectl

`time.Now()` is not allowed in tests. Use `testinfra/timectl`.

```go
import "github.com/lennylabs/lenny/tests/testinfra/timectl"

func TestSomething(t *testing.T) {
    clock := timectl.New(t)             // starts at a deterministic epoch
    now := clock.Now()                  // deterministic time
    clock.Advance(5 * time.Minute)      // move forward
    later := clock.Now()
}
```

The clock is t-scoped: each test gets its own. `clock.Now()` is the
canonical replacement for `time.Now()` and is what every helper in
`testinfra/` reads from.

When code under test takes a `func() time.Time` (e.g. for an
expiry calculation), pass `clock.Now` directly:

```go
record := idempotency.Record{StoredAt: clock.Now()}
expired := record.IsExpired(clock.Now())
```

## Randomness: testinfra/randctl

`crypto/rand` and `math/rand` are not allowed in tests directly.
Use `testinfra/randctl`.

```go
import "github.com/lennylabs/lenny/tests/testinfra/randctl"

func TestSomething(t *testing.T) {
    rng := randctl.New(t)               // seeded with the test name
    n := rng.Intn(100)                  // deterministic given the seed
    bytes := rng.Bytes(16)              // 16 random bytes, deterministic
}
```

The seed is derived from `t.Name()` so the same test always sees
the same sequence; two tests get independent streams. `randctl.New`
also exposes `Crypto()` for the rare case where you need a stream
compatible with `crypto/rand.Reader`.

## No naked Sleep

`time.Sleep` in tests is forbidden. Use `testinfra/wait.For`:

```go
wait.For(t, 5*time.Second, "session reaches running", func() (bool, error) {
    return getState() == "running", nil
})
```

The wait fails the test on timeout with the descriptive message you
provided. Poll interval is 50 ms; predicates that error end the
wait immediately.

## Goroutine leaks

`defer goleak.VerifyNone(t)` at the top of any test that spawns
goroutines:

```go
import "github.com/lennylabs/lenny/tests/testinfra/goleak"

func TestSomething(t *testing.T) {
    defer goleak.VerifyNone(t)
    // ... body that spawns goroutines and is expected to clean up
}
```

`VerifyNone` compares the goroutine set at test exit against the
set at entry and fails the test if any new goroutine survived.
Known-noisy frames (the test runner's own goroutines, HTTP/2's
idle background server) are filtered out.

## Ports

`testinfra/ports.NewListener(t)` returns a fresh OS-assigned port
on `127.0.0.1`. Never hardcode a port number in a test.

## Property-based tests

Property tests via `pgregory.net/rapid` use rapid's own RNG, which
is seeded from the test name. A failing example is recorded under
`testdata/rapid/<TestName>/` and replayed on every subsequent run
so regressions don't slip in.

## Summary

| Don't | Use |
|:------|:----|
| `time.Now()` | `timectl.New(t).Now()` |
| `time.Sleep(n)` | `wait.For(t, deadline, msg, cond)` |
| `crypto/rand.Read` | `randctl.New(t).Crypto()` |
| `math/rand.Intn` | `randctl.New(t).Intn(n)` |
| `net.Listen(":8080")` | `ports.NewListener(t)` |
| Goroutines without check | `defer goleak.VerifyNone(t)` |
