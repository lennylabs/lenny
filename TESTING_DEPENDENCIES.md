# Testing Dependencies and Setup

Companion to [`TESTING.md`](TESTING.md). This file lists every tool the test infrastructure depends on, the version it expects, the install steps per platform, and a verification command. It also covers cloud authentication setup, optional convenience tools, and troubleshooting.

## Table of Contents

1. [Quick start](#1-quick-start)
2. [Platforms and resource sizing](#2-platforms-and-resource-sizing)
3. [Setup scripts](#3-setup-scripts)
4. [Tier-to-tool map](#4-tier-to-tool-map)
5. [Core toolchain (tiers 0–4)](#5-core-toolchain-tiers-04)
6. [Kubernetes toolchain (tier 5)](#6-kubernetes-toolchain-tier-5)
7. [Cloud provider toolchain (tier 6)](#7-cloud-provider-toolchain-tier-6)
8. [Performance and chaos toolchain (tiers 7–8)](#8-performance-and-chaos-toolchain-tiers-78)
9. [Security toolchain (tier 9)](#9-security-toolchain-tier-9)
10. [SDK and language toolchains (§14.13)](#10-sdk-and-language-toolchains-1413)
11. [Documentation toolchain (tier 11)](#11-documentation-toolchain-tier-11)
12. [Optional convenience tools](#12-optional-convenience-tools)
13. [Cloud authentication setup](#13-cloud-authentication-setup)
14. [Verification](#14-verification)
15. [Updating dependencies](#15-updating-dependencies)
16. [Troubleshooting](#16-troubleshooting)
17. [Uninstalling](#17-uninstalling)

---

## 1. Quick start

The fastest path on a fresh machine.

```bash
# 1. Clone
git clone https://github.com/lennylabs/lenny.git
cd lenny
git checkout test-infrastructure

# 2. Install core dependencies (tiers 0–4) AND lenny-test itself.
#    setup-dev.sh installs the harness into $(go env GOPATH)/bin
#    after the external toolchain is set up.
./scripts/setup-dev.sh

# 3. Verify
./scripts/preflight.sh

# 4. Confirm lenny-test is on PATH (add $(go env GOPATH)/bin if not).
command -v lenny-test

# 5. Start the cached container daemon (one-time, persists across runs)
lenny-test infra up --profile containers

# 6. Run the developer inner loop
lenny-test --group pr-fast
```

`scripts/setup-dev.sh` covers tiers 0–4 by default. For higher tiers, run with explicit groups:

```bash
./scripts/setup-dev.sh --include kubernetes      # adds tier 5 tools
./scripts/setup-dev.sh --include cloud           # adds tier 6 tools (all three providers)
./scripts/setup-dev.sh --include security        # adds tier 9 tools
./scripts/setup-dev.sh --include sdk-python      # adds Python SDK toolchain
./scripts/setup-dev.sh --include sdk-typescript  # adds TypeScript SDK toolchain
./scripts/setup-dev.sh --include docs            # adds tier 11 tools
./scripts/setup-dev.sh --include all             # everything except cloud
```

The script is idempotent. Re-running it upgrades pinned versions and skips already-installed tools unless `--force` is passed.

---

## 2. Platforms and resource sizing

### Supported platforms

| Platform | Status | Notes |
|:---------|:------:|:------|
| macOS 13+ (Apple Silicon) | Tier 1 | Primary developer platform |
| macOS 13+ (Intel) | Tier 1 | Fully supported |
| Linux x86_64 (Ubuntu 22.04+, Fedora 38+, Debian 12+) | Tier 1 | Primary CI platform |
| Linux arm64 | Tier 1 | Equivalent to x86_64 for development |
| Windows 11 via WSL2 | Tier 2 | Use Ubuntu under WSL2; Docker Desktop with WSL2 backend |
| Native Windows | Not supported | Use WSL2 |

### Resource requirements

| Workload | CPU | RAM | Disk | Notes |
|:---------|:---:|:---:|:----:|:------|
| Tiers 0–1 (static, unit) | 2 cores | 4 GB | 5 GB | Native execution; minimal containers |
| Tiers 2–3 (component, contract) with `lenny-test-cached` warm | 4 cores | 8 GB | 15 GB | Postgres, Redis, MinIO, OIDC stub, KMS stub, OTLP collector all running |
| Tier 4 (integration via compose) | 6 cores | 12 GB | 25 GB | Full compose stack plus stores |
| Tier 5 (e2e on Kind) | 8 cores | 16 GB | 40 GB | Kind cluster with control plane, two workers, cert-manager, metrics-server |
| Tier 7 load smoke | 8 cores | 16 GB | 40 GB | k6 plus full stack; sustained runs prefer cloud |

A 16 GB MacBook Pro runs everything through Tier 5. Tier 6 (cloud) runs on managed clusters provisioned by CI; developers do not bring up cloud clusters in routine work.

### Docker resource allocation

Docker Desktop, Colima, and OrbStack default to conservative limits. Increase them before running Tier 2 or higher:

| Setting | Tier 2–3 | Tier 4 | Tier 5 |
|:--------|:--------:|:------:|:------:|
| CPUs | 4 | 6 | 8 |
| Memory | 6 GB | 10 GB | 14 GB |
| Disk | 32 GB | 40 GB | 64 GB |
| Swap | 1 GB | 2 GB | 2 GB |

Apple Silicon: Docker Desktop's "Use Rosetta for x86_64/amd64 emulation" must be on for tests that pull amd64 images. OrbStack handles this transparently.

---

## 3. Setup scripts

The repository ships three orchestration scripts. They wrap the per-tool installs documented in this file.

### `scripts/setup-dev.sh`

Installs core toolchains by tier group. Flags:

```
--include <group>      Add a group: kubernetes, cloud, load, chaos, security,
                       sdk-go, sdk-python, sdk-typescript, docs, all
--force                Re-install even if a compatible version is present
--dry-run              Print what would be installed without changing anything
--package-manager      Override detection: brew, apt, dnf, none
--non-interactive      Skip confirmations (CI mode)
```

The script detects the OS and package manager. On macOS it uses Homebrew. On Debian/Ubuntu it uses apt and supplementary scripts for tools not in the distro. On Fedora/RHEL it uses dnf. The script refuses to run as root.

### `scripts/preflight.sh`

Reports each tool's status: installed, missing, version-mismatch. Exit code is the count of issues. Suitable for CI gating.

```bash
$ ./scripts/preflight.sh
[ok]    go         1.22.3
[ok]    docker     27.0.3
[ok]    kubectl    1.28.4
[warn]  buf        1.20.0 (expected >= 1.25.0)
[miss]  k6         not installed (run: scripts/setup-dev.sh --include load)
3 issues. See https://github.com/lennylabs/lenny/blob/main/TESTING_DEPENDENCIES.md
```

### `scripts/setup-cluster.sh`

Provisions a fresh Kind cluster with the Lenny test profile (single control plane, two workers, Calico CNI, cert-manager, metrics-server, runtime-class registration). Idempotent.

```bash
./scripts/setup-cluster.sh                 # provisions or recreates
./scripts/setup-cluster.sh --reuse         # uses existing if present
./scripts/setup-cluster.sh --delete        # tears down
./scripts/setup-cluster.sh --kubeconfig <path>
```

---

## 4. Tier-to-tool map

Use this table to identify the minimum dependency set for the tier you intend to run.

| Tier | Tools required |
|:----:|:---------------|
| 0 — Static | Go, golangci-lint, gofumpt, goimports, buf, jq, helm, helm-unittest, conftest, markdown-link-check, vale (optional) |
| 1 — Unit | Go (only) |
| 2 — Component | Go, Docker, testcontainers (Go library; no extra install), migrate |
| 3 — Contract | Go, Docker, docker compose, protoc, buf |
| 4 — Integration | Go, Docker, docker compose, jq |
| 5 — E2E on Kind | Tier 0–4 plus kubectl, Kind, Helm, helm-unittest, cmctl, optionally kuttl and stern |
| 6 — E2E on cloud | Tier 5 plus gcloud SDK, aws CLI v2, az CLI, eksctl, kubectl auth plugins, optionally terraform |
| 7 — Load and SLO | Tier 4 plus k6, optionally vegeta |
| 8 — Chaos | Tier 4 or 5 plus toxiproxy; cloud scenarios require chaos-mesh (installed in the cluster, no local CLI) |
| 9 — Security | Tier 5 plus OWASP ZAP, kubeaudit, kube-bench, trivy, cosign |
| 10 — Conformance | Tier 4 plus `lenny-compliance` binary (built from this repo, no external install) |
| 11 — Documentation | Ruby 3.2, Jekyll, Node, markdown-link-check |
| SDK contract tests | Tier 3 plus Python 3.11 with pytest/tox/ruff/mypy and Node 20 LTS with vitest/eslint/prettier |

---

## 5. Core toolchain (tiers 0–4)

### Go 1.22+

Used to build every Lenny binary and to run the unit, component, contract, integration, and load tiers.

- macOS: `brew install go`
- Linux (Ubuntu/Debian): the distro package is often too old; install from the official tarball.

  ```bash
  GO_VERSION=1.22.3
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tgz
  sudo rm -rf /usr/local/go
  sudo tar -C /usr/local -xzf /tmp/go.tgz
  echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.profile
  source ~/.profile
  ```
- WSL2: same as Linux.
- Verify: `go version` reports 1.22 or higher.

Add `$(go env GOPATH)/bin` to `PATH` so `go install`-managed binaries (golangci-lint, sqlc, migrate, oapi-codegen) are on the PATH.

### Docker (or compatible runtime)

Required for testcontainers, compose, and Kind. Pick one:

- **Docker Desktop** (macOS, Windows): `brew install --cask docker`. Commercial license for organizations with > 250 employees or > $10M revenue.
- **OrbStack** (macOS): `brew install --cask orbstack`. Commercial license for paid work above the free tier. Fastest option on Apple Silicon.
- **Colima** (macOS, free): `brew install colima docker docker-compose docker-credential-helper`, then `colima start --cpu 6 --memory 12 --disk 64`.
- **Rancher Desktop** (macOS, Windows, Linux, free): `brew install --cask rancher`.
- **Docker Engine** (Linux): follow the official Docker install for your distro.
- Verify: `docker version && docker info`.

Resource allocation: see §2.

### docker compose v2.20+

Bundled with Docker Desktop, OrbStack, Rancher Desktop. With Colima or Docker Engine, install separately:

- macOS: `brew install docker-compose`
- Linux: install the `docker-compose-plugin` package or symlink the standalone binary.
- Verify: `docker compose version`.

### make

- macOS: pre-installed (BSD make) or `brew install make` for GNU make.
- Linux: `sudo apt install make` or `sudo dnf install make`.
- WSL2: same as Linux.
- Verify: `make --version`.

### git 2.34+

- macOS: `xcode-select --install` (provides git) or `brew install git`.
- Linux: `sudo apt install git` or `sudo dnf install git`.
- Verify: `git --version`.

### jq 1.6+

JSON processor used in scripts.

- macOS: `brew install jq`
- Linux: `sudo apt install jq` or `sudo dnf install jq`
- Verify: `jq --version`

### protoc 24.0+ and protoc-gen-go

Protocol Buffers compiler. Compiles `schemas/lenny-adapter.proto` and the gRPC service definitions.

- macOS: `brew install protobuf`
- Linux: distro packages are often outdated; install from the release tarball.

  ```bash
  PROTOC_VERSION=25.3
  ARCH=$(uname -m | sed 's/x86_64/x86_64/;s/aarch64/aarch_64/')
  curl -fsSL "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-${ARCH}.zip" -o /tmp/protoc.zip
  sudo unzip -o /tmp/protoc.zip -d /usr/local
  ```
- Verify: `protoc --version`

Install Go plugins:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

### buf 1.25+

Proto linter and breaking-change detector.

- macOS: `brew install bufbuild/buf/buf`
- Linux:
  ```bash
  BUF_VERSION=1.25.0
  curl -fsSL "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-$(uname -s)-$(uname -m)" -o /tmp/buf
  sudo install -m 0755 /tmp/buf /usr/local/bin/buf
  ```
- Verify: `buf --version`

### openssl 3.0+

Used to generate self-signed mTLS material in dev mode.

- macOS: `brew install openssl@3`. Add it to PATH before the system `/usr/bin/openssl` (which is LibreSSL):
  ```bash
  echo 'export PATH="$(brew --prefix openssl@3)/bin:$PATH"' >> ~/.zshrc
  ```
- Linux: usually pre-installed (`openssl version` to confirm). Upgrade via the distro package manager if needed.
- Verify: `openssl version` reports OpenSSL 3.0 or higher.

### golangci-lint 2.12+

Aggregated Go linter used in Tier 0. The v2 series is required: the v1.x line aborts on Go 1.26 export data with "unsupported version: 2". Install via the upstream install script on every platform — it downloads a prebuilt binary and avoids `go install` compatibility issues that surface when an older pinned version's transitive `golang.org/x/tools` does not compile against a newer Go release.

```bash
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
  | sh -s -- -b "$(go env GOPATH)/bin" v2.12.2
```

- Verify: `golangci-lint --version`
- Why not `brew install` on macOS: brew's package is fine when it tracks the pin, but on a `setup-dev.sh` upgrade we want one canonical path that pins to a specific patch version on every machine.
- Why not `go install`: see above.

### gofumpt and goimports

Stricter format and import-order checks.

```bash
go install mvdan.cc/gofumpt@latest
go install golang.org/x/tools/cmd/goimports@latest
```

Verify: `gofumpt -version && goimports -h`.

### sqlc 1.25+

SQL-to-Go code generator. Drives the type-safe query layer.

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
```

Verify: `sqlc version`.

### migrate 4.17+

Database migration runner. Lenny uses `golang-migrate` as the v1 choice; the migration framework abstracts the tool so swapping to `atlas` or `goose` requires changing one config.

- macOS: `brew install golang-migrate`
- Linux:
  ```bash
  go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
  ```
- Verify: `migrate -version`

### conftest 0.50+

Policy testing for Helm-rendered manifests.

- macOS: `brew install conftest`
- Linux:
  ```bash
  CONFTEST_VERSION=0.50.0
  curl -fsSL "https://github.com/open-policy-agent/conftest/releases/download/v${CONFTEST_VERSION}/conftest_${CONFTEST_VERSION}_$(uname)_$(uname -m).tar.gz" \
    | tar -xz -C /tmp conftest
  sudo install -m 0755 /tmp/conftest /usr/local/bin/conftest
  ```
- Verify: `conftest --version`

---

## 6. Kubernetes toolchain (tier 5)

### kubectl 1.27+

- macOS: `brew install kubectl`
- Linux:
  ```bash
  curl -LO "https://dl.k8s.io/release/$(curl -sL https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
  sudo install -m 0755 kubectl /usr/local/bin/kubectl
  ```
- Verify: `kubectl version --client`

### Kind 0.20+

Local Kubernetes via Docker-in-Docker.

- macOS: `brew install kind`
- Linux:
  ```bash
  go install sigs.k8s.io/kind@v0.20.0
  ```
- Verify: `kind version`

### Helm 3.12+

Chart install for Tier 5 e2e.

- macOS: `brew install helm`
- Linux:
  ```bash
  curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
  ```
- Verify: `helm version`

### helm-unittest 1.1+

Helm plugin for chart-template unit tests. The 1.x line supports both Helm 3 and Helm 4.

```bash
helm plugin install https://github.com/helm-unittest/helm-unittest.git \
  --version v1.1.0 \
  --verify=false
```

`--verify=false` is required on Helm 4 (which enforces plugin signature verification by default) and harmless on Helm 3.

Verify: `helm unittest --help`.

### cmctl 2.0+

cert-manager CLI used in mTLS verification tests.

- macOS: `brew install cmctl`
- Linux:
  ```bash
  CMCTL_VERSION=2.0.0
  curl -fsSL "https://github.com/cert-manager/cmctl/releases/download/v${CMCTL_VERSION}/cmctl_$(uname | tr '[:upper:]' '[:lower:]')_$(uname -m).tar.gz" \
    | tar -xz -C /tmp cmctl
  sudo install -m 0755 /tmp/cmctl /usr/local/bin/cmctl
  ```
- Verify: `cmctl version`

### kuttl 0.15+ (optional)

Declarative e2e assertions.

- macOS: `brew install kudobuilder/tap/kuttl-cli`
- Linux: `go install github.com/kudobuilder/kuttl/cmd/kubectl-kuttl@latest`
- Verify: `kubectl kuttl version`

### stern 1.27+ (optional)

Multi-pod log tailing.

- macOS: `brew install stern`
- Linux: download from https://github.com/stern/stern/releases.
- Verify: `stern --version`

---

## 7. Cloud provider toolchain (tier 6)

Tier 6 runs against three providers (GKE, EKS, AKS). The CLIs below are required if you want to run cloud tests locally; otherwise CI handles them.

### Google Cloud (GKE)

#### gcloud SDK

- macOS: `brew install --cask google-cloud-sdk`
- Linux:
  ```bash
  curl -fsSL https://sdk.cloud.google.com | bash
  exec -l "$SHELL"
  ```
- Required components:
  ```bash
  gcloud components install kubectl gke-gcloud-auth-plugin
  ```
- Verify: `gcloud version`

### AWS (EKS)

#### aws CLI v2

- macOS: `brew install awscli`
- Linux:
  ```bash
  curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-$(uname -m).zip" -o /tmp/awscli.zip
  unzip -o /tmp/awscli.zip -d /tmp
  sudo /tmp/aws/install
  ```
- Verify: `aws --version` reports `aws-cli/2.x`

#### eksctl 0.180+

- macOS: `brew tap weaveworks/tap && brew install weaveworks/tap/eksctl`
- Linux:
  ```bash
  ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
  curl -fsSL "https://github.com/eksctl-io/eksctl/releases/latest/download/eksctl_linux_${ARCH}.tar.gz" \
    | tar -xz -C /tmp eksctl
  sudo install -m 0755 /tmp/eksctl /usr/local/bin/eksctl
  ```
- Verify: `eksctl version`

#### aws-iam-authenticator (optional)

The aws CLI v2 includes `aws eks get-token`, so this is rarely needed. If a specific test demands it:

- macOS: `brew install aws-iam-authenticator`
- Linux: download from https://github.com/kubernetes-sigs/aws-iam-authenticator/releases.

### Microsoft Azure (AKS)

#### az CLI

- macOS: `brew install azure-cli`
- Linux:
  ```bash
  curl -fsSL https://aka.ms/InstallAzureCLIDeb | sudo bash         # Debian/Ubuntu
  # or:
  sudo dnf install -y azure-cli                                    # Fedora/RHEL
  ```
- Verify: `az --version`

#### kubelogin (Azure AD authentication for kubectl)

- macOS: `brew install Azure/kubelogin/kubelogin`
- Linux:
  ```bash
  az aks install-cli
  ```

  This installs both `kubectl` (skipped if present) and `kubelogin`.
- Verify: `kubelogin --version`

### Terraform 1.5+ (optional)

Reproducible cluster bring-up across providers.

- macOS: `brew tap hashicorp/tap && brew install hashicorp/tap/terraform`
- Linux: HashiCorp official repo or release zip.
- Verify: `terraform -version`

---

## 8. Performance and chaos toolchain (tiers 7–8)

### k6 0.50+

Primary load generator.

- macOS: `brew install k6`
- Linux (Debian/Ubuntu):
  ```bash
  sudo gpg -k
  sudo gpg --no-default-keyring --keyring /usr/share/keyrings/k6-archive-keyring.gpg \
       --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69
  echo "deb [signed-by=/usr/share/keyrings/k6-archive-keyring.gpg] https://dl.k6.io/deb stable main" \
    | sudo tee /etc/apt/sources.list.d/k6.list
  sudo apt-get update && sudo apt-get install -y k6
  ```
- Verify: `k6 version`

### vegeta 12.11+ (optional)

Burst HTTP load.

- macOS: `brew install vegeta`
- Linux: `go install github.com/tsenart/vegeta/v12@latest`
- Verify: `vegeta -version`

### toxiproxy 2.7+

Latency, bandwidth, and partition injection for stores.

- macOS: `brew install toxiproxy`
- Linux: download from https://github.com/Shopify/toxiproxy/releases. The repository also ships a `docker-compose` profile that uses the official image, so a local install is optional.
- Verify: `toxiproxy-server --version`

### chaos-mesh (cloud-only)

Installed in the cluster via Helm during pre-release. No local CLI is required, but `chaosctl` is occasionally useful:

```bash
curl -fsSL https://mirrors.chaos-mesh.org/v2.7.0/install.sh | sh
```

---

## 9. Security toolchain (tier 9)

### OWASP ZAP 2.14+

API fuzzing against REST and MCP surfaces.

- macOS: `brew install --cask owasp-zap`
- Linux: download the cross-platform installer from https://www.zaproxy.org/download/, or use the Docker image:
  ```bash
  docker pull ghcr.io/zaproxy/zaproxy:stable
  ```
- Verify: `zap.sh -version` (CLI) or `docker run --rm ghcr.io/zaproxy/zaproxy:stable zap.sh -version`

### kubeaudit 0.22+

Static analysis of Kubernetes manifests.

- macOS: `brew install kubeaudit`
- Linux: download from https://github.com/Shopify/kubeaudit/releases.
- Verify: `kubeaudit version`

### kube-bench 0.7+

CIS Kubernetes benchmark.

- macOS: `brew install kube-bench`
- Linux: download from https://github.com/aquasecurity/kube-bench/releases or run the Docker image:
  ```bash
  docker pull aquasec/kube-bench:latest
  ```
- Verify: `kube-bench version`

### trivy 0.51+

Image and filesystem vulnerability scanner.

- macOS: `brew install trivy`
- Linux:
  ```bash
  sudo apt-get install -y apt-transport-https gnupg lsb-release
  wget -qO - https://aquasecurity.github.io/trivy-repo/deb/public.key | sudo apt-key add -
  echo deb https://aquasecurity.github.io/trivy-repo/deb $(lsb_release -sc) main \
    | sudo tee -a /etc/apt/sources.list.d/trivy.list
  sudo apt-get update && sudo apt-get install -y trivy
  ```
- Verify: `trivy --version`

### cosign 2.2+

Image signing and verification.

- macOS: `brew install cosign`
- Linux:
  ```bash
  go install github.com/sigstore/cosign/v2/cmd/cosign@latest
  ```
- Verify: `cosign version`

---

## 10. SDK and language toolchains (§14.13)

The language-SDK contract tests (TESTING.md §14.13) require runtimes for Python and Node alongside the existing Go toolchain.

### Python 3.11 (for client and runtime-author SDKs)

Use a version manager to avoid clashing with the system Python.

- macOS:
  ```bash
  brew install pyenv
  pyenv install 3.11.9
  pyenv global 3.11.9
  ```
- Linux:
  ```bash
  curl -fsSL https://pyenv.run | bash
  # follow the printed PATH instructions, then:
  pyenv install 3.11.9
  pyenv global 3.11.9
  ```
- Verify: `python3 --version` reports 3.11.x

### pipx 1.4+

Installs Python CLI tools in isolated venvs.

- macOS: `brew install pipx && pipx ensurepath`
- Linux: `python3 -m pip install --user pipx && pipx ensurepath`
- Verify: `pipx --version`

### Python tools (pytest, tox, ruff, mypy, openapi-python-client)

```bash
pipx install tox==4.15.0
pipx install ruff==0.4.5
pipx install mypy==1.10.0
pipx install openapi-python-client==0.20.0
```

`pytest` is installed per-project via the SDK's `pyproject.toml` and `requirements-dev.txt`.

Verify:

```bash
tox --version && ruff --version && mypy --version && openapi-python-client --version
```

### Node 20 LTS (for TypeScript SDK)

Use `nvm` or `fnm` to manage Node versions.

- macOS / Linux (nvm):
  ```bash
  curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.39.7/install.sh | bash
  # restart your shell, then:
  nvm install 20
  nvm use 20
  ```
- macOS (fnm, faster): `brew install fnm && fnm install 20 && fnm use 20`
- Verify: `node --version` reports v20.x and `npm --version` reports 10.x.

### TypeScript tools (vitest, eslint, prettier, openapi-typescript)

These are installed per-project via `package.json` and `npm install`. The harness runs them through `npm run test` and `npm run lint`. No global install is required, but `typescript` is convenient globally:

```bash
npm install -g typescript@5.4
```

Verify: `tsc --version`

### Go SDK code generation (oapi-codegen)

```bash
go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.3.0
```

Verify: `oapi-codegen --version`

### Ruby toolchain (only if working on documentation; not required for SDKs)

See §11.

---

## 11. Documentation toolchain (tier 11)

### Ruby 3.2+

macOS ships an old system Ruby that should not be modified. Use a version manager.

- macOS:
  ```bash
  brew install rbenv ruby-build
  rbenv install 3.2.4
  rbenv global 3.2.4
  echo 'eval "$(rbenv init - zsh)"' >> ~/.zshrc
  ```
- Linux:
  ```bash
  sudo apt install -y rbenv ruby-build
  rbenv install 3.2.4
  rbenv global 3.2.4
  ```
- Verify: `ruby --version`

### Jekyll and Bundler

The `docs/` directory ships its own `Gemfile`.

```bash
gem install bundler
cd docs
bundle install
```

Verify: `bundle exec jekyll --version`

### markdown-link-check 3.12+

Requires Node (see §10).

```bash
npm install -g markdown-link-check@3.12.2
```

Verify: `markdown-link-check --version`

### vale 3.4+ (optional)

Prose linter; reports against the `.claude/rules/doc-style.md` style.

- macOS: `brew install vale`
- Linux: download from https://github.com/errata-ai/vale/releases.
- Verify: `vale --version`

---

## 12. Optional convenience tools

These are not required but speed up day-to-day work.

| Tool | Install | Use |
|:-----|:--------|:----|
| direnv | `brew install direnv` | Auto-load project env vars |
| watchexec | `brew install watchexec` | Re-run tests on file change |
| fzf | `brew install fzf` | Shell history and file search |
| gh CLI | `brew install gh` | GitHub PR and issue interaction |
| git-delta | `brew install git-delta` | Better `git diff` output |
| krew | `kubectl krew install` | kubectl plugin manager |
| k9s | `brew install k9s` | Terminal Kubernetes UI |
| jless | `brew install jless` | JSON viewer for verdict files |
| yq | `brew install yq` | YAML processor |
| dive | `brew install dive` | Container image inspection |

---

## 13. Cloud authentication setup

Cloud tests require credentials. CI uses OIDC federation; developers use interactive logins.

### Google Cloud

```bash
gcloud auth login
gcloud auth application-default login

# Set the active project
gcloud config set project lenny-dev-gcp

# Configure GKE credentials for a specific cluster
gcloud container clusters get-credentials lenny-dev-gke \
  --location us-central1 --project lenny-dev-gcp
```

The first command authenticates the CLI; the second writes Application Default Credentials used by the test harness. Both prompts open a browser.

### AWS

Prefer AWS SSO over long-lived access keys.

```bash
# One-time profile setup
aws configure sso --profile lenny-dev

# Sign in (opens browser)
aws sso login --profile lenny-dev

# Configure kubectl for an EKS cluster
aws eks update-kubeconfig \
  --region us-east-2 --name lenny-dev-eks --profile lenny-dev
```

Export the profile in your shell or set `AWS_PROFILE=lenny-dev` for the test harness.

### Microsoft Azure

```bash
az login

# Set the active subscription
az account set --subscription "Lenny Dev"

# Configure kubectl for an AKS cluster
az aks get-credentials \
  --resource-group lenny-dev-rg --name lenny-dev-aks
```

For AAD-integrated AKS clusters, `kubelogin` rewrites the kubeconfig:

```bash
kubelogin convert-kubeconfig -l azurecli
```

### Credentials in CI

CI uses workload identity federation. Long-lived keys are not stored in repository secrets. The relevant GitHub OIDC trust configurations are documented in `docs/operator-guide/installation.md` and provisioned via Terraform in `deploy/terraform/cloud/`.

---

## 14. Verification

`scripts/preflight.sh` is the canonical check. Re-run after each install or upgrade.

```bash
./scripts/preflight.sh                  # all tools
./scripts/preflight.sh --group core     # tiers 0–4
./scripts/preflight.sh --group kubernetes
./scripts/preflight.sh --group cloud
./scripts/preflight.sh --group sdk
./scripts/preflight.sh --json           # machine-readable
```

The script reports `ok`, `warn` (version below recommendation), or `miss` (not installed). Exit code is the count of issues.

`lenny-test infra status` reports the runtime state of containers and Kind clusters:

```bash
$ lenny-test infra status
profile=containers   running   postgres redis minio oidc kms otel
profile=kind          stopped   no cluster present
```

---

## 15. Updating dependencies

Pinned versions live in:

- `scripts/setup-dev.sh` (per-tool version constants).
- `go.mod` and `go.sum` (Go modules).
- `sdks/client/python/pyproject.toml` and `sdks/runtime/python/pyproject.toml`.
- `sdks/client/typescript/package.json` and `sdks/runtime/typescript/package.json`.
- `docs/Gemfile.lock` (Ruby gems).
- `charts/lenny/Chart.yaml` and `charts/lenny/requirements.lock` (chart dependencies).

To upgrade a pinned version:

1. Update the constant in `scripts/setup-dev.sh` (and the per-language manifest where relevant).
2. Run `./scripts/setup-dev.sh --force` locally.
3. Run `./scripts/preflight.sh` and confirm the new version reports `ok`.
4. Run `lenny-test --group pr-fast` and `lenny-test --tier static` to confirm nothing broke.
5. Open a PR. CI runs the full PR gate with the updated tools.

The harness performs a self-check at startup: if `lenny-test` detects a tool version below the pinned minimum, it refuses to run and prints the offending tool with the upgrade command.

---

## 16. Troubleshooting

### Docker

- **`Cannot connect to the Docker daemon`**: The Docker runtime is not running. macOS Docker Desktop: open the Docker app. Colima: `colima start`. OrbStack: `orb start`. Linux: `sudo systemctl start docker`.
- **`exec format error` pulling x86_64 images on Apple Silicon**: enable Rosetta in Docker Desktop settings, or use OrbStack which handles emulation transparently.
- **Out-of-memory during Kind bring-up**: increase Docker memory to 14 GB (§2). The Kind control plane plus Lenny chart needs at least 10 GB headroom.

### Kind

- **Cluster creation hangs at "Preparing nodes"**: usually a Docker resource shortage or a stale cluster. `kind delete cluster --name lenny-test && ./scripts/setup-cluster.sh`.
- **`kubectl` connects to the wrong cluster**: `kubectl config use-context kind-lenny-test`.
- **NetworkPolicy assertions fail on Kind**: Calico is required. The provisioning script installs it; if you customized the Kind config, re-run `./scripts/setup-cluster.sh`.

### Go

- **`go: command not found` after install**: ensure `/usr/local/go/bin` and `$(go env GOPATH)/bin` are in PATH. Restart the shell.
- **Module download stalls behind a corporate proxy**: configure `GOPROXY` and `GOSUMDB` per your organization's policy.

### Python and Node

- **System Python is too old**: use pyenv (§10). Do not modify the system Python on macOS.
- **`npm install` fails on Apple Silicon**: ensure Node was installed for arm64. `node -p "process.arch"` should report `arm64`. Reinstall via nvm if mismatched.

### Cloud authentication

- **`gcloud auth login` succeeds but `kubectl` returns "no Application Default Credentials"**: also run `gcloud auth application-default login`.
- **`aws sso login` opens browser but kubectl still fails**: confirm `AWS_PROFILE` matches the profile used in `aws configure sso`.
- **`kubelogin` returns "no AAD token"**: run `az login` first, then `kubelogin convert-kubeconfig -l azurecli`.

### Helm

- **`helm install` rejects values with `helm-unittest` errors**: `helm-unittest` is a Helm plugin; ensure it is installed (`helm plugin list`) and re-run.
- **CRD installation order errors**: Helm 3 installs CRDs before resources. If the chart adds new CRDs in a release, the upgrade path is documented in `docs/runbooks/crd-upgrade.md`.

### `lenny-test` harness

- **`lenny-test infra up --profile containers` exits with "image pull error"**: confirm Docker is running and your registry credentials are valid. Pinned image digests are in `tests/testinfra/containers/images.lock`.
- **Tests fail with "context deadline exceeded" on the first run**: container caching is cold. Re-run the same command; subsequent runs reuse warm containers.
- **Verdict file is missing**: the harness writes `tests/results/latest.json` only after a successful invocation. A panic at startup may leave it absent; check the stderr output.

---

## 17. Uninstalling

To remove the test infrastructure from a developer machine:

```bash
# Stop and remove all test infrastructure
lenny-test infra down --all
lenny-test infra prune

# Remove Kind clusters
kind delete cluster --name lenny-test

# Remove the cached container daemon socket
rm -f ${XDG_RUNTIME_DIR:-/tmp}/lenny-test/cached.sock

# Remove pinned Docker images
docker image prune --filter "label=org.lennylabs.test=true" --force
```

The `setup-dev.sh` script does not provide an uninstall mode for individual tools because most are useful outside Lenny. To remove a tool, use the package manager that installed it (`brew uninstall <name>`, `apt remove <name>`, `npm uninstall -g <name>`, `pipx uninstall <name>`).

---

## Appendix A — License notes

| Tool | License | Notes |
|:-----|:--------|:------|
| Docker Desktop | Commercial | Free for personal use, small companies, education, OSS contributors. Paid for orgs > 250 employees or > $10M revenue |
| OrbStack | Commercial | Free for personal use; paid for commercial use |
| Colima | Apache 2.0 | Free for any use |
| Rancher Desktop | Apache 2.0 | Free for any use |
| Docker Engine | Apache 2.0 | Free for any use |
| Go | BSD | Free for any use |
| Kubernetes ecosystem (kubectl, Kind, Helm, cert-manager, Calico) | Apache 2.0 | Free for any use |
| Cloud CLIs (gcloud, aws, az) | Provider EULAs | Free to use; cloud usage billed separately |
| k6 | AGPL-3.0 | Open-source; commercial support available |
| OWASP ZAP | Apache 2.0 | Free for any use |
| trivy | Apache 2.0 | Free for any use |

Organizations subject to commercial licensing for Docker Desktop should default to Colima or Rancher Desktop. CI uses Docker Engine on Linux runners.

## Appendix B — Network requirements

The setup scripts and `lenny-test infra up` reach the following hosts. Allowlist them on restricted networks.

| Host | Purpose |
|:-----|:--------|
| `go.dev`, `proxy.golang.org`, `sum.golang.org` | Go module downloads |
| `registry.npmjs.org` | npm packages |
| `pypi.org`, `files.pythonhosted.org` | Python packages |
| `rubygems.org` | Ruby gems |
| `docker.io`, `ghcr.io`, `quay.io`, `gcr.io` | Container images |
| `github.com`, `objects.githubusercontent.com` | Release artifacts |
| `storage.googleapis.com` | gcloud SDK and tarballs |
| `awscli.amazonaws.com` | AWS CLI installer |
| `aka.ms`, `packages.microsoft.com` | Azure CLI |
| `helm.sh`, `get.helm.sh` | Helm release artifacts |
| `kind.sigs.k8s.io` | Kind node images |
| `dl.k8s.io` | Kubernetes binaries |

The `lenny-test infra up` command and the chart install pull container images by digest; the digests are pinned in `tests/testinfra/containers/images.lock` and `charts/lenny/values.yaml`.
