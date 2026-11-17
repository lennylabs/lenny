# Developer-tool proxy and credential-file spike

Date: 2026-11-01 (container clocks report 2026-11-02 UTC). Host: Docker Desktop on macOS, linux/arm64 containers.

## Topology

- `spk-int`: `docker network create --internal`. Client containers attach only here. A control `curl https://repo.maven.apache.org/` from this network fails with `Could not resolve host` (exit 6), so the network has no direct egress and no external DNS.
- `spk-ext`: normal bridge network.
- `spk-proxy`: alpine:3.22 running `bin/proxy` (source `src/proxy/main.go`, Go 1.26.7), attached to `spk-ext` and to `spk-int` with alias `proxy`, listening on `:3128`. It logs every `CONNECT host:port` and every absolute-form request (`ABSOLUTE GET http://...`), forwards both, and logs `Proxy-Authorization` (always empty in every run below: no client was given proxy credentials).
- `spk-fake`: alpine:3.22 running `bin/fake` (source `src/fake/main.go`) on `spk-int` only, aliases `ghe.example.test`, `gitlab.example.test`, `npm.example.test`, and `pypi.example.test`. It serves HTTPS on 443 (certificate from a throwaway test CA, `certs/ca.pem`) and plain HTTP on 80, and logs method, host, path, `Authorization`, `Private-Token`, `Job-Token`, and `User-Agent`.
- Every test script is under `scripts/`. Every client container runs with `--rm`.

The fake hosts are reachable directly from `spk-int`, so a proxy-honouring test is judged by the proxy log: a `CONNECT ghe.example.test:443` line proves the client tunnelled through the proxy.

## Versions

| Tool | Version | Source |
|:--|:--|:--|
| Maven 3 | Apache Maven 3.9.16, Java 21.0.12.1 | `maven:3.9-eclipse-temurin-21` |
| Maven 4 | Apache Maven 4.0.0-rc-7 | `maven:4.0.0-rc-7-eclipse-temurin-21` (no `maven:4` or `maven:4.0` tag exists on Docker Hub; rc-7 is the newest 4.x tag) |
| Gradle 8 | 8.14.6, Java 21.0.13 | `gradle:8-jdk21` |
| Gradle 9 | 9.8.2, Java 21.0.13 | `gradle:9-jdk21` |
| gh | 2.103.3 (2026-10-26) | `gh_2.103.3_linux_arm64.tar.gz` from github.com/cli/cli releases, run in alpine:3.22 |
| glab | 1.126.0 (4dd0d32d1) | `glab_1.126.0_linux_arm64.tar.gz` from gitlab.com/gitlab-org/cli releases, run in alpine:3.22 |
| npm | 10.8.2, node v20.20.2 | `node:20` |
| pip | 26.2.2, Python 3.13 | `python:3.13-alpine` |

Proxy properties used throughout (`$P`):
`-Dhttps.proxyHost=proxy -Dhttps.proxyPort=3128 -Dhttp.proxyHost=proxy -Dhttp.proxyPort=3128 -Dhttp.nonProxyHosts=localhost`

## 1. Maven

Project: `mvnproj/pom.xml`, one dependency `org.apache.commons:commons-lang3:3.17.0`. Command: `mvn -B -ntp -Dmaven.repo.local=/tmp/repo dependency:resolve` with a fresh local repository per run (`scripts/mvn.sh`). Settings file `shared/settings.xml` holds two active `<proxy>` entries (protocol `https` and `http`, host `proxy`, port 3128).

| Image | Configuration | Exit | Proxy log | Result |
|:--|:--|:--|:--|:--|
| 3.9.16 | none (control) | 1 | nothing | name resolution failure, as expected |
| 3.9.16 | (a) `JAVA_TOOL_OPTIONS=$P` | 1 | nothing | FAIL: JVM prints `Picked up JAVA_TOOL_OPTIONS`, Maven still resolves `repo.maven.apache.org` locally (`Temporary failure in name resolution`) |
| 3.9.16 | (b) `MAVEN_ARGS=-s /shared/settings.xml` | 0 | 5x `CONNECT repo.maven.apache.org:443` | PASS, jar downloaded |
| 3.9.16 | (c) `MAVEN_OPTS=$P` | 1 | nothing | FAIL, same as (a) |
| 3.9.16 | `JAVA_TOOL_OPTIONS=$P -Dmaven.resolver.transport=wagon` | 0 | 5x CONNECT | PASS: the Wagon transport honours the JVM proxy properties |
| 3.9.16 | `JAVA_TOOL_OPTIONS=$P -Djava.net.useSystemProxies=true` | 1 | nothing | FAIL |
| 3.9.16 | `-Dmaven.resolver.transport=jdk` | 1 | n/a | Maven aborts: `Unknown resolver transport 'jdk'. Supported transports are: wagon, native, auto` |
| 4.0.0-rc-7 | none (control) | 1 | nothing | expected failure |
| 4.0.0-rc-7 | (a) `JAVA_TOOL_OPTIONS=$P` | 1 | nothing | FAIL |
| 4.0.0-rc-7 | (b) `MAVEN_ARGS=-s /shared/settings.xml` | 0 | 5x `CONNECT repo.maven.apache.org:443`, 1x `CONNECT repository.apache.org:443` | PASS |
| 4.0.0-rc-7 | (c) `MAVEN_OPTS=$P` | 1 | nothing | FAIL |
| 4.0.0-rc-7 | `JAVA_TOOL_OPTIONS=$P -Dmaven.resolver.transport=wagon` | 0 | 5x + 1x CONNECT | PASS |
| 4.0.0-rc-7 | `JAVA_TOOL_OPTIONS=$P -Dmaven.resolver.transport=jdk` (JVM property, and separately as `MAVEN_ARGS=-Dmaven.resolver.transport=jdk`) | 1 | nothing | FAIL. `mvn -X` still reports `Using transporter ApacheTransporter`, so the `jdk` request did not change the transporter in rc-7 |

Default transporter observed with `mvn -X`: Maven 3.9.16 and Maven 4.0.0-rc-7 both log `Using transporter ApacheTransporter` with no override, and `WagonTransporter` with `maven.resolver.transport=wagon`.

Maven 4 rc-7 differences observed: it additionally fetches `/.meta/prefixes.txt` (remote repository prefix filter) and contacts `repository.apache.org` (the `apache.snapshots` repository from the super POM chain). An allowlist for Maven 4 therefore needs `repository.apache.org` in addition to `repo.maven.apache.org`, or the build logs warnings for the prefix fetch. The proxy-configuration behaviour is the same as 3.9.

## 2. Gradle

Project: `gradleproj/` (`java` plugin, `mavenCentral()`, `commons-lang3:3.17.0`, a `resolve` task printing `configurations.runtimeClasspath.files`). Command: `gradle --no-daemon -q resolve` (`scripts/gradle.sh`). For (c), `GRADLE_USER_HOME=/shared/guhN` on a bind mount holding `gradle.properties` with `systemProp.https.proxyHost=proxy`, `systemProp.https.proxyPort=3128`, `systemProp.http.proxyHost=proxy`, `systemProp.http.proxyPort=3128`, and `systemProp.http.nonProxyHosts=localhost`.

| Image | Configuration | Exit | Proxy log | Result |
|:--|:--|:--|:--|:--|
| 8.14.6 / 9.8.2 | control | 1 | nothing | `repo.maven.apache.org: Temporary failure in name resolution` |
| 8.14.6 / 9.8.2 | (a) `JAVA_TOOL_OPTIONS=$P` | 0 | `CONNECT repo.maven.apache.org:443` | PASS |
| 8.14.6 / 9.8.2 | (b) `GRADLE_OPTS=$P` with `--no-daemon` | 0 | same | PASS |
| 8.14.6 / 9.8.2 | (b) `GRADLE_OPTS=$P` with a daemon (no `--no-daemon`) | 0 | same | PASS |
| 8.14.6 / 9.8.2 | (c) `GRADLE_USER_HOME/gradle.properties` | 0 | same | PASS |

Wrapper: `gradle wrapper` was run offline (`--network none`) in each image, producing `gradleproj-w8` (`distributionUrl=.../gradle-8.14.6-bin.zip`) and `gradleproj-w9` (`.../gradle-9.8.2-bin.zip`). Command: `./gradlew --no-daemon -q resolve` with an empty `GRADLE_USER_HOME`, so the wrapper downloads the distribution and the build then resolves the dependency.

| Wrapper | Configuration | Exit | Proxy log | Result |
|:--|:--|:--|:--|:--|
| 8.14.6 / 9.8.2 | control | 1 | nothing | `Exception in thread "main" java.net.UnknownHostException: services.gradle.org` |
| 8.14.6 / 9.8.2 | (a) `JAVA_TOOL_OPTIONS=$P` | 0 | CONNECT `services.gradle.org`, `github.com`, `release-assets.githubusercontent.com`, `repo.maven.apache.org` | PASS |
| 8.14.6 / 9.8.2 | (b) `GRADLE_OPTS=$P` | 0 | same four | PASS |
| 8.14.6 / 9.8.2 | (c) `GRADLE_USER_HOME/gradle.properties` | 0 | same four | PASS |

The distribution download follows a redirect chain `services.gradle.org` to `github.com` to `release-assets.githubusercontent.com`. An allowlist for the wrapper needs all three hosts.

## 3. gh (GitHub CLI 2.103.3)

Fake GHES at `https://ghe.example.test`; `gh api --hostname ghe.example.test user` hits `GET /api/v3/user`. Base flags: `GH_CONFIG_DIR=/shared/gh`, `SSL_CERT_FILE=/certs/ca.pem`, `GH_NO_UPDATE_NOTIFIER=1`, `GH_PROMPT_DISABLED=1`. `hosts.yml`:

```yaml
ghe.example.test:
    oauth_token: tok-A
    user: alice
    git_protocol: https
```

| Test | Observed (fake log `Authorization`) | Result |
|:--|:--|:--|
| T1 `oauth_token: tok-A` | `token tok-A` | PASS |
| T2 file edited to `tok-B`, new container | `token tok-B` | PASS: re-read per invocation |
| T10 one long-lived container, two `docker exec gh api` calls, file replaced by write-temp-and-rename between them (`tok-P1` then `tok-P2`) | `token tok-P1` then `token tok-P2` | PASS: re-read per invocation, rename-replace works |
| T9 multi-account layout (`users: {alice: {oauth_token: ...}}` plus top-level `oauth_token`), `tok-M1` then `tok-M2` | `token tok-M1`, `token tok-M2` | PASS |
| T3 `GH_ENTERPRISE_TOKEN=tok-ENV` with `hosts.yml` holding `tok-B` | `token tok-ENV` | PASS: env overrides file |
| T3b `GH_TOKEN=tok-GHTOKEN` with `hosts.yml` holding `tok-B` | `token tok-B` | `GH_TOKEN` is ignored for a GHES host; the file wins |
| T8 `GH_ENTERPRISE_TOKEN` only, empty `GH_CONFIG_DIR`, with `GH_HOST` or with `--hostname` | `token tok-ENVONLY` | PASS: no `hosts.yml` needed |
| T3c `GH_HOST=ghe.example.test`, no `--hostname` | `token tok-B` | PASS |
| T4 `SSL_CERT_FILE` empty | `tls: failed to verify certificate: x509: certificate signed by unknown authority`, exit 1 | PASS: `SSL_CERT_FILE` is what made T1 trust the test CA |
| T5 `HTTPS_PROXY=http://proxy:3128` | proxy `CONNECT ghe.example.test:443`, request succeeds | PASS |
| T5b lowercase `https_proxy` | proxy CONNECT | PASS |
| T5c `HTTPS_PROXY` plus `NO_PROXY=.example.test` | no CONNECT, direct request succeeds | PASS: `NO_PROXY` honoured |
| T5d `gh api zen` (github.com) via `HTTPS_PROXY`, `GH_TOKEN=bogus`, system CAs | proxy `CONNECT api.github.com:443`; GitHub answers `Bad credentials (HTTP 401)` | PASS: tunnel works for a host the client cannot resolve |
| T6 no token (empty config dir), GHES and github.com | `To get started with GitHub CLI, please run: gh auth login ... populate the GH_TOKEN environment variable`, exit 4, no request reaches the fake | gh refuses to run |
| T7 `hosts.yml` on a `:ro` bind mount, uid 1000, `gh api` | `token tok-RO`, exit 0 | PASS: read-only works |
| T7b same, `gh auth status --hostname ghe.example.test` | `Logged in to ghe.example.test account alice (/shared/gh/hosts.yml)`, exit 0 | PASS (it also issued `GET /api/v3/` with the token) |
| T7c same, `gh config set editor vi` | `failed to write config to disk: open /shared/gh/config.yml: read-only file system`, exit 1 | expected: only explicit config writes fail |

`gh api` wrote no files into `GH_CONFIG_DIR` (only `hosts.yml` was present afterwards). gh sent `Authorization: token <value>`. The proxy saw `User-Agent: Go-http-client/1.1` on the CONNECT.

## 4. glab (GitLab CLI 1.126.0)

Fake GitLab at `gitlab.example.test`; `glab api --hostname gitlab.example.test user` hits `GET /api/v4/user`. Base flags: `GLAB_CONFIG_DIR=<dir>`, `SSL_CERT_FILE=/certs/ca.pem`. `config.yml` (mode 0600):

```yaml
hosts:
    gitlab.example.test:
        token: tok-A
        api_host: gitlab.example.test
        api_protocol: https
        git_protocol: https
```

| Test | Observed | Result |
|:--|:--|:--|
| G1 `token: tok-A`, `GLAB_CONFIG_DIR=/shared/glab` | fake `https GET /api/v4/user private-token="tok-A"` | PASS: `GLAB_CONFIG_DIR` honoured |
| G2 file rewritten to `tok-B` | `private-token="tok-B"` | PASS: re-read per invocation |
| G3 `api_protocol: http` | fake logs `http GET gitlab.example.test/api/v4/user private-token="tok-C"` on port 80 | PASS: plain HTTP is used, and the token goes in cleartext |
| G8 `api_protocol: http` plus `HTTP_PROXY`/`HTTPS_PROXY` | proxy `ABSOLUTE GET http://gitlab.example.test/api/v4/user` | PASS: absolute-form through `HTTP_PROXY`, token visible to the proxy |
| G4 `GITLAB_TOKEN=tok-ENV`, host configured in file with `tok-B` | `private-token="tok-ENV"` | PASS: env overrides file |
| G4b `GITLAB_HOST` + `GITLAB_TOKEN`, no `--hostname` | `private-token="tok-ENV2"` | PASS |
| G11 `GITLAB_TOKEN` only, empty config dir, `--hostname` | `GITLAB_TOKEN is set, but "gitlab.example.test" is neither the default host nor a configured host, so glab did not send the credential to it.` Request sent with no token, exit 0 | glab withholds `GITLAB_TOKEN` from an unconfigured non-default host |
| G11b `GITLAB_HOST` + `GITLAB_TOKEN`, empty config dir mounted `:ro` | `failed to read configuration: open /cfg/.config.yml...: read-only file system`, exit 2 | FAIL: with no config file glab insists on creating one |
| `GITLAB_HOST` + `GITLAB_TOKEN`, empty writable dir | `private-token="tok-HOSTENV"`; glab wrote an 8489-byte default `config.yml`, `aliases.yml`, and `config.lock` | PASS, but writes files |
| `GITLAB_HOST` + `GITLAB_TOKEN`, `:ro` dir holding a 0600 config for another host | `private-token="tok-HOSTENV2"`, warning `could not update last_seen_version: ... read-only file system`, exit 0 | PASS |
| G5 `SSL_CERT_FILE` empty | `x509: certificate signed by unknown authority`, exit 1 | PASS: `SSL_CERT_FILE` honoured |
| G6 `HTTPS_PROXY=http://proxy:3128` | proxy `CONNECT gitlab.example.test:443` | PASS |
| G9 config `:ro`, uid 1000, file 0600 | `private-token="tok-RO"`, warning `could not update last_seen_version`, exit 0 | PASS |
| G9b config `:ro`, file 0644 | `/cfg/config.yml has the permissions 644, but glab requires 600.`, exit 2 | FAIL by design |
| Secret-volume layout (`config.yml -> ..data/config.yml`, `..data -> ..2026_v1`), file 0400 | `has the permissions 400, but glab requires 600.`, exit 2 | FAIL: 0400 is also rejected |
| Same layout, file 0600, `:ro` | `private-token="tok-K8S"`, exit 0 | PASS: symlinks are followed, mode must be exactly 0600 |
| G10 no token anywhere | request sent with empty `Private-Token`, exit 0 | glab does not refuse |

glab writes into its config dir on ordinary invocations: `glamour_style_auto_migrated` and `last_seen_version` are added to `config.yml`, and `aliases.yml` and `config.lock` are created. On a read-only dir with an existing config these writes fail with a warning and the command still succeeds.

glab contacts `gitlab.com` on every invocation unless disabled. Measured with `HTTPS_PROXY` set, counting `CONNECT gitlab.com:443`, three runs each:

| Knob | gitlab.com CONNECTs per run |
|:--|:--|
| none | 2 |
| `check_update: false` in config | 1 |
| `telemetry: false` in config | 1 |
| both config keys | 0 |
| env `GLAB_CHECK_UPDATE=false` only | 1 |
| env `GLAB_CHECK_UPDATE=false` and `GLAB_SEND_TELEMETRY=false` | 0 |

Without a proxy the update check fails with `failed checking for glab updates: Get "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/releases?page=1&per_page=1"` and the command still exits 0. When the host came from `GITLAB_HOST`, glab also sent `POST /api/v4/usage_data/track_event` with the token to the target host. `NO_PROMPT` is deprecated in favour of `GLAB_NO_PROMPT`. glab sends the token as `Private-Token`.

## 5. npm and pip (bonus)

npm (`node:20`, uid 1000): `NPM_CONFIG_USERCONFIG=/cfg/npmrc` on a `:ro` mount, `npm view spkpkg version` against `npm.example.test`.

```
registry=https://npm.example.test/
//npm.example.test/:_authToken=tok-N1
cafile=/certs/ca.pem
```

| Test | Observed | Result |
|:--|:--|:--|
| N1 `tok-N1` | fake `Authorization: Bearer tok-N1`, prints `1.0.0` | PASS |
| N2 file rewritten to `tok-N2` | `Bearer tok-N2` | PASS: re-read per invocation |
| N3 `HTTPS_PROXY` env | proxy `CONNECT npm.example.test:443` | PASS |
| N4 no `cafile`, `NODE_EXTRA_CA_CERTS=/certs/ca.pem` | `Bearer tok-N4`, success | PASS |
| N5 no `cafile`, `SSL_CERT_FILE` only | `UNABLE_TO_VERIFY_LEAF_SIGNATURE` | npm ignores `SSL_CERT_FILE` |

pip (`python:3.13-alpine`, uid 1000): `PIP_CONFIG_FILE=/cfg/pip.conf` on a `:ro` mount, `pip download --no-deps spkpkg` against `pypi.example.test` (the fake index lists no files, so pip ends with `No matching distribution found`; the request is what is checked).

```
[global]
index-url = https://user:tok-P1@pypi.example.test/simple
cert = /certs/ca.pem
```

| Test | Observed | Result |
|:--|:--|:--|
| P1 | fake `GET /simple/spkpkg/` with `Authorization: Basic dXNlcjp0b2stUDE=` (`user:tok-P1`) | PASS |
| P2 file rewritten to `tok-P2` | `Basic dXNlcjp0b2stUDI=` (`user:tok-P2`) | PASS: re-read per invocation |
| P3 `HTTPS_PROXY` env | proxy `CONNECT pypi.example.test:443` | PASS |

## Cleanup

The full proxy and fake-server logs are saved as `proxy.log` and `fake.log`. All `spk-*` containers and the `spk-int` and `spk-ext` networks were removed at the end. Images were kept. `podium-postgres` and `podium-minio` were not touched.
