# Homebrew distribution for the lenny CLI

`lenny.rb` is the Homebrew formula source the release pipeline copies
into `lennylabs/homebrew-tap` on tag push. The formula uses URL-based
binary distribution (downloads a pre-built tarball from the release
artifact) so `brew install lennylabs/tap/lenny` stays under the §22.7
TTHW five-minute budget from install to working session.

## Release-pipeline contract

On tag push (`v*.*.*`):

1. The release workflow builds the `lenny` binary for each
   `(GOOS, GOARCH)` pair the formula references:
   `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`.
2. Each binary is packaged into a tarball named
   `lenny-${VERSION}-${GOOS}-${GOARCH}.tar.gz`.
3. The workflow computes the SHA-256 of each tarball and renders this
   file with the version + the four per-platform digests stamped into
   the `REPLACE_WITH_*_SHA256` placeholders.
4. The workflow opens a PR against `lennylabs/homebrew-tap` updating
   `Formula/lenny.rb` with the rendered file.

## Manual publishing

Until the release pipeline lands the tap-update step, a release
manager runs the substitution by hand:

```bash
VERSION=0.1.0
sha256sum dist/lenny-${VERSION}-darwin-arm64.tar.gz | awk '{print $1}'
sha256sum dist/lenny-${VERSION}-darwin-amd64.tar.gz | awk '{print $1}'
sha256sum dist/lenny-${VERSION}-linux-arm64.tar.gz  | awk '{print $1}'
sha256sum dist/lenny-${VERSION}-linux-amd64.tar.gz  | awk '{print $1}'
```

Then edit `lenny.rb`, replace `version` and the four
`REPLACE_WITH_*_SHA256` lines, and open the tap PR.

## Tap repository

The tap repository (`lennylabs/homebrew-tap`) holds the rendered
formulas. The repo's only requirement is the standard Homebrew tap
layout:

```
homebrew-tap/
├── Formula/
│   └── lenny.rb     <- rendered from this file
└── README.md
```

## Verification

A green smoke run from a clean macOS user:

```bash
brew tap lennylabs/tap
brew install lennylabs/tap/lenny
lenny help            # exits 0, prints the Embedded Mode subcommand list
brew test lennylabs/tap/lenny
```

The §22.7 TTHW load-tier scenario exercises this once the tap is
published; see `tests/tier11_docs/time_to_hello_world_test.go` step 1.
