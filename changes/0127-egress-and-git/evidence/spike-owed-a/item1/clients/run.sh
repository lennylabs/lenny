#!/bin/bash
# Runs every client from a throwaway container; raw per-client output in raw/.
O=$(cd "$(dirname "$0")/../.." && pwd); I=$O/item1; R=$I/raw; . $I/clients/urls.sh
run(){ n=$1; shift; docker run --rm --name oa-c-$n --network oa-net --cpus 2 -m 2g -v $O/bin:/b:ro -v $I:/i:ro -v $I/certs:/c:ro "$@" >$R/$n.out 2>$R/$n.err; echo "$n exit=$? lines=$(wc -l <$R/$n.out)"; }
run alpine alpine:3.22 sh /i/clients/alpine.sh
run debian oa-py bash /i/clients/debian.sh
run node node:24-slim bash /i/clients/node.sh
run java eclipse-temurin:21-jdk bash /i/clients/java.sh
run go -e SSL_CERT_FILE=/c/bundle.pem alpine:3.22 sh -c "/b/ncgo $URLS | sed 's/^/go-net-http /'"
run rust rust:1-slim-bookworm bash -c "apt-get update -qq >/dev/null 2>&1; apt-get install -y -qq libssl3 >/dev/null 2>&1; rustc --version >&2; for m in rustls native-tls; do /i/rust/target/release/ncrust \$m $URLS | sed \"s/^/rust-reqwest-\$m /\"; done; SSL_CERT_FILE=/c/bundle.pem /i/rust/target/release/ncrust rustls-native-roots $URLS | sed 's/^/rust-reqwest-rustls-native-roots /'"
run deno -e DENO_CERT=/c/bundle.pem denoland/deno:2.5.0 bash -c "deno --version >&2; cp /i/clients/node.js /tmp/n.js; sed -i 's/process.argv.slice(2)/Deno.args/' /tmp/n.js; deno run -A /tmp/n.js $URLS | sed 's/^NAME/deno-fetch-rustls/'"
run bun -e NODE_EXTRA_CA_CERTS=/c/bundle.pem oven/bun:1.2 bash -c "bun --version >&2; bun /i/clients/node.js $URLS | sed 's/^NAME/bun-fetch/'"
