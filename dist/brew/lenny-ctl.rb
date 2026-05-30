# SPDX-License-Identifier: MIT
#
# Homebrew formula for the lenny-ctl operator CLI (spec §24.0, §17.6).
# Lives in the source tree so the release pipeline can copy it into the
# homebrew-tap repository (lennylabs/homebrew-tap) after stamping the
# version + sha256 fields.
#
# lenny-ctl is the standalone operator CLI: the same build that ships as
# the krew plugin kubectl-lenny. The release workflow signs each archive
# with cosign (keyless) and attaches a detached .sig + .pem.
#
# The release workflow does the following on tag push:
#
#   1. Build lenny-ctl binaries for darwin-amd64, darwin-arm64,
#      linux-amd64, linux-arm64.
#   2. Compute sha256 of each lenny-ctl_<tag>_<os>_<arch>.tar.gz archive.
#   3. Render this file with the version + per-platform SHAs.
#   4. Open a PR against lennylabs/homebrew-tap.
#
# After the tap PR merges:
#
#   brew install lennylabs/tap/lenny-ctl
#
# The formula uses url-based binary distribution (downloads a pre-built
# tarball from the release artifact) rather than building from source.

class LennyCtl < Formula
  desc "Kubernetes-native agent session platform — operator CLI"
  homepage "https://github.com/lennylabs/lenny"
  version "0.1.0" # release pipeline replaces this on tag push

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/lennylabs/lenny/releases/download/v#{version}/lenny-ctl_v#{version}_darwin_arm64.tar.gz"
      sha256 "REPLACE_WITH_DARWIN_ARM64_SHA256"
    else
      url "https://github.com/lennylabs/lenny/releases/download/v#{version}/lenny-ctl_v#{version}_darwin_amd64.tar.gz"
      sha256 "REPLACE_WITH_DARWIN_AMD64_SHA256"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/lennylabs/lenny/releases/download/v#{version}/lenny-ctl_v#{version}_linux_arm64.tar.gz"
      sha256 "REPLACE_WITH_LINUX_ARM64_SHA256"
    else
      url "https://github.com/lennylabs/lenny/releases/download/v#{version}/lenny-ctl_v#{version}_linux_amd64.tar.gz"
      sha256 "REPLACE_WITH_LINUX_AMD64_SHA256"
    end
  end

  license "MIT"

  def install
    bin.install "lenny-ctl"
  end

  test do
    # `lenny-ctl version` prints the local CLI build and runs offline, so
    # the formula test needs no running gateway. It must report the
    # release tag (§17.6 line 360 invariant, mirrored for the standalone
    # binary).
    assert_match version.to_s, shell_output("#{bin}/lenny-ctl version")
  end
end
