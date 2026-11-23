#!/bin/sh
# SPDX-License-Identifier: MIT
# Run the token concurrency model with TLC inside a container that has no
# network and sees only the model and the TLC jar.
#
# Usage: tests/formal/tokenconcurrency/run.sh <path-to-tla2tools.jar> <output-dir> <scenario> <variant> [property ...]
#
# <scenario> names a configuration file <scenario>.cfg in this directory and
# <variant> is Main, TF, or I13Lock. With no property named, TLC checks the
# invariants and properties the configuration lists; with properties named,
# it checks only those. TLC also reports a deadlock, a state in which no
# request can take a step, unless TLC_NODEADLOCK=1.
#
# The runner first checks the jar's SHA-256 against the pinned release and
# stops when it differs, then runs the PlusCal translator on a copy of the
# module and stops when the committed translation is not current. It writes
# <scenario>-<variant>.cfg, <scenario>-<variant>.log, and
# <scenario>-<variant>.receipt to the output directory. The log ends with
# TLC's verdict: "No error has been found", or the violated invariant or
# property, or the deadlock, with the behavior that reaches it.
#
# TLC_WORKERS sets the TLC worker count (default 2), TLC_HEAP the JVM heap
# (default 3g). TLC_DETACH=1 starts the container detached under the name
# tlc-<scenario>-<variant> and returns; read the log when it stops.
set -eu

# TLC v1.7.4, https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar
pinned_sha256=936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88

jar=${1:?path to tla2tools.jar}
out=${2:?output directory}
scenario=${3:?scenario}
variant=${4:?variant: Main, TF, or I13Lock}
shift 4

case $variant in
  Main|TF|I13Lock) ;;
  *) echo "run.sh: unknown variant $variant" >&2; exit 2 ;;
esac

here=$(cd "$(dirname "$0")" && pwd)
base="$here/$scenario.cfg"
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

name="$scenario-$variant"
cfg="$out/$name.cfg"

# The effective configuration: the scenario's constants, the variant, and
# either the listed checks or the ones named on the command line.
{
  if [ $# -eq 0 ]; then
    cat "$base"
  else
    sed '/^INVARIANTS/,$d' "$base"
    for p in "$@"; do
      case $p in
        RequestsTerminate|RevocationReachesEveryReplica) echo "PROPERTY $p" ;;
        *) echo "INVARIANT $p" ;;
      esac
    done
  fi
  echo "CONSTANT Variant = \"$variant\""
} > "$cfg"

{
  echo "tlc_release=v1.7.4"
  echo "jar_sha256=$sum"
  echo "scenario=$scenario"
  echo "variant=$variant"
  echo "checks=${*:-all listed in $scenario.cfg}"
  echo "workers=${TLC_WORKERS:-2}"
  echo "started=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
} > "$out/$name.receipt"

deadlock=""
if [ "${TLC_NODEADLOCK:-0}" = 1 ]; then
  deadlock="-deadlock"
fi

script="set -eu
mkdir -p /tmp/pcal
cp /model/TokenConcurrency.tla /tmp/pcal/
cd /tmp/pcal
java -cp /tla/tla2tools.jar pcal.trans -nocfg TokenConcurrency.tla >/dev/null
if ! cmp -s /tmp/pcal/TokenConcurrency.tla /model/TokenConcurrency.tla; then
  echo 'run.sh: the committed PlusCal translation is not current; run pcal.trans on TokenConcurrency.tla' >&2
  exit 1
fi
mkdir -p /tmp/run
cp /model/TokenConcurrency.tla /tmp/run/
cp /out/$name.cfg /tmp/run/$name.cfg
cd /tmp/run
java -XX:+UseParallelGC -Xmx${TLC_HEAP:-3g} -cp /tla/tla2tools.jar tlc2.TLC \
  -workers ${TLC_WORKERS:-2} -cleanup $deadlock \
  -metadir /tmp/run/states -config $name.cfg TokenConcurrency.tla \
  > /out/$name.log 2>&1 || true
tail -n 3 /out/$name.log"

if [ "${TLC_DETACH:-0}" = 1 ]; then
  exec docker run -d --name "tlc-$name" --network none \
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
