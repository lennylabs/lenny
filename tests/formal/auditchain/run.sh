#!/bin/sh
# SPDX-License-Identifier: MIT
# Run the audit chain model with TLC inside a container that has no network
# and sees only the model and the TLC jar.
#
# Usage: tests/formal/auditchain/run.sh <path-to-tla2tools.jar> <output-dir> <configuration|all> [property ...]
#
# <configuration> names a configuration file <configuration>.cfg in this
# directory; "all" runs every configuration in turn. With no property named,
# TLC checks the invariants the configuration lists; with properties named,
# it checks only those. A behavior that has finished has no successor, and
# the model states no liveness property, so TLC runs with -deadlock.
#
# The runner first checks the jar's SHA-256 against the pinned release and
# stops when it differs. It writes <configuration>.cfg,
# <configuration>.log, and <configuration>.receipt to the output directory.
# The log ends with TLC's verdict: "No error has been found", or the
# violated invariant with the behavior that reaches it.
#
# TLC_WORKERS sets the TLC worker count (default 2), TLC_HEAP the JVM heap
# (default 3g). TLC_DETACH=1 starts the container detached under the name
# tlc-auditchain-<configuration> and returns; read the log when it stops.
set -eu

# TLC v1.7.4, https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar
pinned_sha256=936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88

jar=${1:?path to tla2tools.jar}
out=${2:?output directory}
config=${3:?configuration name, or all}
shift 3

here=$(cd "$(dirname "$0")" && pwd)

if [ "$config" = all ]; then
  if [ $# -ne 0 ]; then
    echo "run.sh: properties cannot be named with all" >&2
    exit 2
  fi
  status=0
  for f in "$here"/*.cfg; do
    name=$(basename "$f" .cfg)
    echo "== $name"
    "$here/run.sh" "$jar" "$out" "$name" || status=1
  done
  exit $status
fi

base="$here/$config.cfg"
if [ ! -f "$base" ]; then
  echo "run.sh: no configuration $base" >&2
  exit 2
fi
mkdir -p "$out"
out=$(cd "$out" && pwd)
jar_dir=$(cd "$(dirname "$jar")" && pwd)
jar_name=$(basename "$jar")

if command -v sha256sum >/dev/null 2>&1; then
  sum=$(sha256sum "$jar_dir/$jar_name" | cut -d' ' -f1)
else
  sum=$(shasum -a 256 "$jar_dir/$jar_name" | cut -d' ' -f1)
fi
if [ "$sum" != "$pinned_sha256" ]; then
  echo "run.sh: $jar has SHA-256 $sum, not the pinned $pinned_sha256" >&2
  exit 1
fi

name="$config"
cfg="$out/$name.cfg"

# The effective configuration: the scenario's constants and either the
# listed checks or the ones named on the command line.
if [ $# -eq 0 ]; then
  cp "$base" "$cfg"
else
  {
    sed '/^INVARIANTS/,$d' "$base"
    for p in "$@"; do
      echo "INVARIANT $p"
    done
  } > "$cfg"
fi

{
  echo "tlc_release=v1.7.4"
  echo "jar_sha256=$sum"
  echo "configuration=$config"
  echo "checks=${*:-all listed in $config.cfg}"
  echo "workers=${TLC_WORKERS:-2}"
  echo "started=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
} > "$out/$name.receipt"

script="set -eu
mkdir -p /tmp/run
cp /model/AuditChain.tla /tmp/run/
cp /out/$name.cfg /tmp/run/$name.cfg
cd /tmp/run
java -XX:+UseParallelGC -Xmx${TLC_HEAP:-3g} -cp /tla/tla2tools.jar tlc2.TLC \
  -workers ${TLC_WORKERS:-2} -cleanup -deadlock \
  -metadir /tmp/run/states -config $name.cfg AuditChain.tla \
  > /out/$name.log 2>&1 || true
tail -n 3 /out/$name.log"

if [ "${TLC_DETACH:-0}" = 1 ]; then
  exec docker run -d --name "tlc-auditchain-$name" --network none \
    -v "$jar_dir/$jar_name:/tla/tla2tools.jar:ro" \
    -v "$here:/model:ro" \
    -v "$out:/out" \
    eclipse-temurin:21-jdk sh -c "$script"
fi

exec docker run --rm --network none \
  -v "$jar_dir/$jar_name:/tla/tla2tools.jar:ro" \
  -v "$here:/model:ro" \
  -v "$out:/out" \
  eclipse-temurin:21-jdk sh -c "$script"
