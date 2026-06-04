#!/bin/sh
# SPDX-License-Identifier: MIT
#
# §17.4 self-signed mTLS material for the Compose Mode credentials
# profile. Generates a development CA and a leaf certificate into the
# target directory (default ./lenny-data/certs). Used by `make
# compose-tls` and by the docker-compose `dev-certs` service.
#
#   scripts/dev-certs.sh [dir]
#
# The material is regenerated only when ca.crt is absent, so an existing
# trust setup survives repeated runs. Delete the directory to rotate.
# This material is for local development only and MUST NOT be reused in
# production (§17.4 line 247).
set -eu

DIR="${1:-./lenny-data/certs}"
DAYS=825   # leaf validity; under the 825-day limit modern clients enforce.
SUBJ_CA="/CN=Lenny Dev CA"
SUBJ_LEAF="/CN=lenny-gateway"
# SANs cover the loopback names and the compose service hostnames so a
# client verifying against ca.crt accepts the gateway and the backends.
SAN="subjectAltName=DNS:localhost,DNS:gateway,DNS:lenny-gateway,DNS:minio,DNS:redis,DNS:postgres,IP:127.0.0.1"

mkdir -p "$DIR"

if [ -f "$DIR/ca.crt" ]; then
  echo "dev-certs: $DIR/ca.crt already exists; leaving it in place (delete to rotate)"
  exit 0
fi

echo "dev-certs: generating CA and leaf certificate in $DIR"

# Self-signed CA.
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "$DIR/ca.key" -out "$DIR/ca.crt" \
  -days "$DAYS" -subj "$SUBJ_CA" >/dev/null 2>&1

# Leaf key + CSR.
openssl req -newkey rsa:2048 -nodes \
  -keyout "$DIR/tls.key" -out "$DIR/tls.csr" \
  -subj "$SUBJ_LEAF" >/dev/null 2>&1

# Sign the leaf with the CA, carrying the SAN extension.
openssl x509 -req -in "$DIR/tls.csr" \
  -CA "$DIR/ca.crt" -CAkey "$DIR/ca.key" -CAcreateserial \
  -out "$DIR/tls.crt" -days "$DAYS" \
  -extfile /dev/stdin <<EOF >/dev/null 2>&1
$SAN
EOF

rm -f "$DIR/tls.csr" "$DIR/ca.srl"
chmod 600 "$DIR/ca.key" "$DIR/tls.key"

echo "dev-certs: wrote $DIR/ca.crt $DIR/tls.crt $DIR/tls.key"
