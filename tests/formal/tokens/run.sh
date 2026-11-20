#!/bin/sh
# Run the token-rules model with the Alloy command-line runner inside a
# container that has no network and sees only the model and the Alloy jar.
#
# Usage: tests/formal/tokens/run.sh <path-to-org.alloytools.alloy.dist.jar> <output-dir> [command-pattern] [model-file]
#
# The output directory receives receipt.json and one text file per solution.
# The runner prints one line per command: its index, kind, name, and result.
# For a check, UNSAT means no counterexample within the scope and SAT means a
# counterexample was found. For a run, SAT means the scenario exists.
#
# ALLOY_SOLVER selects the SAT solver (default sat4j). The native solvers,
# such as glucose and minisat, ship for linux/amd64 only; on another host set
# ALLOY_PLATFORM=linux/amd64 to run the container under emulation.
set -eu

jar=${1:?path to org.alloytools.alloy.dist.jar}
out=${2:?output directory}
pattern=${3:-*}
model=${4:-tokens.als}
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$out"
out=$(cd "$out" && pwd)
jar_dir=$(cd "$(dirname "$jar")" && pwd)
jar_name=$(basename "$jar")

solver=${ALLOY_SOLVER:-sat4j}
platform=${ALLOY_PLATFORM:-}

exec docker run --rm --network none ${platform:+--platform "$platform"} \
  -v "$jar_dir/$jar_name:/alloy/alloy.jar:ro" \
  -v "$here:/model:ro" \
  -v "$out:/out" \
  eclipse-temurin:21-jdk \
  java -Xmx4g -jar /alloy/alloy.jar exec -f -s "$solver" -c "$pattern" -t text -o /out/solutions "/model/$model"
