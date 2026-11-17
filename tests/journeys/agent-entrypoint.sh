#!/bin/sh
# SPDX-License-Identifier: MIT
#
# The journey agent container's entrypoint. It runs the command it was
# given under timeout(1) when JOURNEY_TIME_LIMIT names a limit in seconds,
# so the agent stops at the scenario's time limit even if the harness that
# started it is gone. timeout sends SIGTERM at the limit and SIGKILL 30
# seconds later.
set -eu

if [ -n "${JOURNEY_TIME_LIMIT:-}" ]; then
  exec timeout --kill-after=30 "${JOURNEY_TIME_LIMIT}" "$@"
fi
exec "$@"
