#!/bin/sh
# SPDX-License-Identifier: MIT
#
# §17.4 line 276 Compose Mode smoke test. Mirrors the Source Mode
# TestSourceModeSmoke (tests/tier4_integration/source_mode_smoke_test.go):
# bootstrap the tenant + echo runtime, create an echo session, send a
# prompt, verify the echo response reflects it, and terminate. Driven
# over HTTP with the §17.4 dev-mode header auth path.
#
# Run via `docker compose run smoke-test`. Exits 0 on success, non-zero
# on the first failed step.
set -eu

BASE="${LENNY_BASE_URL:-http://gateway:8080}"
PROMPT="smoke test hello"
TENANT="acme"
USER_ID="alice@acme.com"

# Common headers (§17.4 dev-mode auth).
H_CT="Content-Type: application/json"
H_TENANT="X-Lenny-Tenant-ID: ${TENANT}"
H_USER="X-Lenny-User-ID: ${USER_ID}"

# Wait for the gateway to accept connections (depends_on only guarantees
# the container started, not that the HTTP listener is ready and the
# stores are reachable).
echo "smoke: waiting for ${BASE}/healthz"
i=0
until curl -fsS -o /dev/null "${BASE}/healthz"; do
  i=$((i + 1))
  if [ "$i" -ge 60 ]; then
    echo "smoke: gateway did not become ready" >&2
    exit 1
  fi
  sleep 1
done

# Bootstrap the tenant + echo runtime (injection enabled so the
# mid-session prompt is accepted), as a platform-admin.
echo "smoke: bootstrap tenant + echo runtime"
curl -fsS -X POST "${BASE}/v1/admin/bootstrap" \
  -H "${H_CT}" -H "${H_TENANT}" -H "${H_USER}" \
  -H "X-Lenny-Roles: platform-admin" \
  -d '{
        "tenants": [{"id": "acme", "displayName": "Acme Corp"}],
        "runtimes": [{
          "name": "echo",
          "image": "lenny/echo@sha256:abc",
          "capabilities": {"injection": {"supported": true, "modes": ["immediate", "queued"]}}
        }]
      }' >/dev/null

# Create + start the echo session.
echo "smoke: start session"
CREATED=$(curl -fsS -X POST "${BASE}/v1/sessions/start" \
  -H "${H_CT}" -H "${H_TENANT}" -H "${H_USER}" \
  -d "{\"runtimeRef\": \"echo\", \"userId\": \"${USER_ID}\"}")
# Extract the session id without a JSON parser dependency.
SID=$(printf '%s' "$CREATED" | sed -n 's/.*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
if [ -z "$SID" ]; then
  echo "smoke: session id missing in response: $CREATED" >&2
  exit 1
fi

# Send a prompt; the echo runtime reflects it back.
echo "smoke: send prompt to session ${SID}"
RESP=$(curl -fsS -X POST "${BASE}/v1/sessions/${SID}/messages" \
  -H "${H_CT}" -H "${H_TENANT}" -H "${H_USER}" \
  -d "{\"messages\": [{\"role\": \"user\", \"content\": \"${PROMPT}\"}]}")
case "$RESP" in
  *"$PROMPT"*) : ;;
  *)
    echo "smoke: echo response did not contain the prompt: $RESP" >&2
    exit 1
    ;;
esac

# Terminate cleanly.
echo "smoke: terminate session ${SID}"
curl -fsS -X POST "${BASE}/v1/sessions/${SID}/terminate" \
  -H "${H_CT}" -H "${H_TENANT}" -H "${H_USER}" >/dev/null

echo "smoke: OK"
