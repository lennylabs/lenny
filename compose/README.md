# Compose Mode

Compose Mode runs the production gateway against real Postgres, Redis, and
MinIO backends in a single local Docker stack. It sits between Source Mode
(`make run`, in-memory stores) and Embedded Mode (`lenny up`, a real
Kubernetes path). The top-level `docker-compose.yml` defines the stack; the
files in this directory are its supporting configuration.

## Start the stack

```
docker compose up           # or: make compose
```

This starts Postgres, Redis, MinIO, applies the database schema, and starts
the gateway with the built-in echo runtime as the single agent. The gateway
listens on `http://localhost:8080`. The echo runtime replays prompts without
an LLM provider, so no credentials are required.

Stop and remove the stack, including its volumes:

```
docker compose down -v      # or: make compose-down
```

## Smoke test

```
docker compose run smoke-test
```

The smoke test bootstraps a tenant and the echo runtime, creates a session,
sends a prompt, verifies the echo response, and exits. It is the Compose Mode
counterpart of `make test-smoke`.

## Observability profile

```
docker compose --profile observability up
```

Adds Prometheus (scrapes the gateway at `:8080/metrics`), Grafana (anonymous
admin at `http://localhost:3000`, with the Prometheus datasource and the Lenny
overview dashboard provisioned), and Jaeger (`http://localhost:16686`, OTLP on
`:4317`/`:4318`).

## Credential-testing profile

The default profile transmits all traffic over plain HTTP. Do not configure
real LLM provider credentials without enabling TLS. The credentials profile
generates self-signed mTLS material and sets `LENNY_DEV_TLS=true`:

```
make compose-tls
# equivalent to:
#   scripts/dev-certs.sh ./lenny-data/certs
#   LENNY_DEV_TLS=true docker compose --profile credentials up
```

Certificates are written to `./lenny-data/certs/` and regenerated only when
absent (delete the directory to rotate). This material is for local
development only and must not be reused in production.

### Trusting the self-signed CA

To let API clients (CLI tools, test harnesses, CI scripts) verify the
certificate, trust `./lenny-data/certs/ca.crt`:

1. **macOS:** `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ./lenny-data/certs/ca.crt`
2. **Linux:** `sudo cp ./lenny-data/certs/ca.crt /usr/local/share/ca-certificates/lenny-dev-ca.crt && sudo update-ca-certificates`
3. **Per-process (any OS):** set `SSL_CERT_FILE=./lenny-data/certs/ca.crt`, or pass `--cacert ./lenny-data/certs/ca.crt` to curl.
4. **CI:** use the per-process option to avoid modifying the runner's trust store.
