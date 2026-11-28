#!/bin/sh
# SPDX-License-Identifier: MIT
# Run the session lifecycle model with TLC, on the host's Java by default or
# inside a container that has no network and sees only the model and the
# TLC jar.
#
# Usage: tests/formal/sessionlifecycle/run.sh <path-to-tla2tools.jar> <output-dir> <configuration|all> [property ...]
#
# <configuration> names a configuration file <configuration>.cfg in this
# directory; "all" runs every configuration in this directory in turn. With
# no property named, TLC checks the invariants and properties the
# configuration lists; with properties named, it checks only those. TLC also
# reports a deadlock, a state in which no step is enabled, unless
# TLC_NODEADLOCK=1; the module's Done step lets a behavior in which every
# session has ended stutter, so a deadlock is a modelling error.
#
# The runner first checks the jar's SHA-256 against the pinned release and
# stops when it differs. For each configuration it writes <configuration>.cfg
# (the effective configuration), <configuration>.log, and
# <configuration>.receipt to the output directory. The log ends with TLC's
# verdict: "No error has been found", or the violated invariant or property,
# or the deadlock, with the behavior that reaches it.
#
# TLC_WORKERS sets the TLC worker count (default 8), TLC_HEAP the JVM heap
# (default 12g). The runner uses TLC_JAVA, else $JAVA_HOME/bin/java, else
# java on the PATH, and stops when that runtime does not start; TLC 1.7.4
# needs Java 11 or later. TLC_DOCKER=1 runs TLC in the eclipse-temurin:21-jdk
# container instead, with no network. With TLC_DOCKER=1,
# TLC_DETACH=1 starts each container detached under the name
# tlc-sessionlifecycle-<configuration> and returns; read the log when it stops.
set -eu

# TLC v1.7.4, https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar
pinned_sha256=936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88

jar=${1:?path to tla2tools.jar}
out=${2:?output directory}
config=${3:?configuration name or all}
shift 3

here=$(cd "$(dirname "$0")" && pwd)
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

if [ "${TLC_DOCKER:-0}" != 1 ]; then
  java=${TLC_JAVA:-${JAVA_HOME:+$JAVA_HOME/bin/java}}
  java=${java:-java}
  if ! "$java" -version >/dev/null 2>&1; then
    echo "run.sh: no Java runtime starts at $java; set TLC_JAVA or JAVA_HOME, or run with TLC_DOCKER=1" >&2
    exit 1
  fi
  java_version=$("$java" -version 2>&1 | head -n 1)
fi

# The temporal properties of the module; every other name is an invariant.
is_property() {
  case $1 in
    TerminalStays|LegalMoves|OnlyRootClientLifts|SuspensionCutsTurn) return 0 ;;
    IdleKeepsPodForRunningDescendant|CloseRule|ACANoExpiryWhileAncestorPaused) return 0 ;;
    ParentActsOnlyWhenRunning|ParentEndReachesChildren|ForceReachesDescendants) return 0 ;;
    TreeResumeReachesBlocked|PartialSuspensionCompletes) return 0 ;;
    *) return 1 ;;
  esac
}

run_one() {
  name=$1
  shift
  base="$here/$name.cfg"
  if [ ! -f "$base" ]; then
    echo "run.sh: no configuration $base" >&2
    exit 2
  fi
  cfg="$out/$name.cfg"

  # The effective configuration: the configuration's constants, and either
  # the listed checks or the ones named on the command line.
  if [ $# -eq 0 ]; then
    cat "$base" > "$cfg"
  else
    {
      sed '/^INVARIANTS/,$d' "$base"
      for p in "$@"; do
        if is_property "$p"; then echo "PROPERTY $p"; else echo "INVARIANT $p"; fi
      done
    } > "$cfg"
  fi

  {
    echo "tlc_release=v1.7.4"
    echo "jar_sha256=$sum"
    echo "configuration=$name"
    echo "checks=${*:-all listed in $name.cfg}"
    echo "workers=${TLC_WORKERS:-8}"
    echo "started=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  } > "$out/$name.receipt"

  deadlock=""
  if [ "${TLC_NODEADLOCK:-0}" = 1 ]; then
    deadlock="-deadlock"
  fi

  script="set -eu
mkdir -p /tmp/run
cp /model/SessionLifecycle.tla /tmp/run/
cp /out/$name.cfg /tmp/run/$name.cfg
cd /tmp/run
java -XX:+UseParallelGC -Xmx${TLC_HEAP:-12g} -cp /tla/tla2tools.jar tlc2.TLC \
  -workers ${TLC_WORKERS:-8} -cleanup $deadlock \
  -metadir /tmp/run/states -config $name.cfg SessionLifecycle.tla \
  > /out/$name.log 2>&1 || true
tail -n 3 /out/$name.log"

  if [ "${TLC_DOCKER:-0}" != 1 ]; then
    echo "java=$java_version" >> "$out/$name.receipt"
    work=$(mktemp -d)
    cp "$here/SessionLifecycle.tla" "$work/"
    cp "$out/$name.cfg" "$work/$name.cfg"
    (cd "$work" && "$java" -XX:+UseParallelGC -Xmx${TLC_HEAP:-12g} -cp "$jar_dir/$jar_name" tlc2.TLC \
      -workers ${TLC_WORKERS:-8} -cleanup $deadlock \
      -metadir "$work/states" -config "$name.cfg" SessionLifecycle.tla > "$out/$name.log" 2>&1) || true
    rm -rf "$work"
    tail -n 3 "$out/$name.log"
    echo "finished=$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$out/$name.receipt"
    return 0
  fi

  if [ "${TLC_DETACH:-0}" = 1 ]; then
    docker run -d --name "tlc-sessionlifecycle-$name" --network none \
      -v "$jar_dir/$jar_name:/tla/tla2tools.jar:ro" \
      -v "$here:/model:ro" \
      -v "$out:/out" \
      eclipse-temurin:21-jdk sh -c "$script"
  else
    docker run --rm --network none \
      -v "$jar_dir/$jar_name:/tla/tla2tools.jar:ro" \
      -v "$here:/model:ro" \
      -v "$out:/out" \
      eclipse-temurin:21-jdk sh -c "$script"
  fi
  echo "finished=$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$out/$name.receipt"
}

if [ "$config" = all ]; then
  for f in "$here"/*.cfg; do
    run_one "$(basename "$f" .cfg)" "$@"
  done
else
  run_one "$config" "$@"
fi
