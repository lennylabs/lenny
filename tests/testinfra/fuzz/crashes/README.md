# tests/testinfra/fuzz/crashes/

Repo-rooted store of every fuzz-discovered crash, per TESTING.md §19.2.

Layout:

```
crashes/
  <package_with_slashes_as_underscores>/
    <FuzzTargetName>/
      <content-addressed-id>     # the input that crashed
```

The directory is populated by `testinfra/fuzz.Mirror(t, pkg, target)`, called from a regular test in the same package immediately after `go test -fuzz=<target> -fuzztime=<duration>` writes a crash to `testdata/fuzz/<target>/`. Mirror copies the seed to its repo-rooted home so:

1. Crashes survive across go-test caches and CI runners.
2. Subsequent runs replay every entry as a regression seed.
3. The Tier 0 gate counts entries and refuses new additions without an accompanying fix commit (planned in a later phase).

Crashes are not pruned automatically; the file's presence is the regression test. Remove an entry only when the underlying input is no longer reachable (a parser change, a schema migration). Document the removal in the commit message so reviewers can re-derive the rationale.
