# SPDX-License-Identifier: MIT
#
# Homebrew formula for the lenny CLI. Lives in the source tree so a
# release pipeline can copy it into the homebrew-tap repository
# (lennylabs/homebrew-tap) after stamping the version + sha256 fields.
#
# The release workflow does the following on tag push:
#
#   1. Build lenny binaries for darwin-amd64, darwin-arm64,
#      linux-amd64, linux-arm64.
#   2. Compute sha256 of each tarball.
#   3. Render this file with the version + per-platform SHAs.
#   4. Open a PR against lennylabs/homebrew-tap.
#
# After the tap PR merges:
#
#   brew tap lennylabs/tap
#   brew install lennylabs/tap/lenny
#
# Or in one shot:
#
#   brew install lennylabs/tap/lenny
#
# The formula uses url-based binary distribution (downloads a
# pre-built tarball from the release artifact) rather than building
# from source on the user's machine. The §22.7 TTHW budget of five
# minutes from install to a working session depends on this.

class Lenny < Formula
  desc "Kubernetes-native agent session platform — CLI"
  homepage "https://github.com/lennylabs/lenny"
  version "0.1.0" # release pipeline replaces this on tag push

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/lennylabs/lenny/releases/download/v#{version}/lenny-#{version}-darwin-arm64.tar.gz"
      sha256 "REPLACE_WITH_DARWIN_ARM64_SHA256"
    else
      url "https://github.com/lennylabs/lenny/releases/download/v#{version}/lenny-#{version}-darwin-amd64.tar.gz"
      sha256 "REPLACE_WITH_DARWIN_AMD64_SHA256"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/lennylabs/lenny/releases/download/v#{version}/lenny-#{version}-linux-arm64.tar.gz"
      sha256 "REPLACE_WITH_LINUX_ARM64_SHA256"
    else
      url "https://github.com/lennylabs/lenny/releases/download/v#{version}/lenny-#{version}-linux-amd64.tar.gz"
      sha256 "REPLACE_WITH_LINUX_AMD64_SHA256"
    end
  end

  license "MIT"

  def install
    bin.install "lenny"
  end

  test do
    # The §22.7 TTHW step 1 verification is "the lenny binary is on
    # the PATH after install". The help command exits 0 and prints
    # the documented "Embedded Mode" subcommand list.
    assert_match "Embedded Mode", shell_output("#{bin}/lenny help")
  end
end
