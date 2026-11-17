#!/bin/bash
cd "$(dirname "$0")/.."
GH=(docker run --rm --network spk-int -v $PWD/dl/gh/gh_2.103.3_linux_arm64/bin/gh:/usr/local/bin/gh:ro -v $PWD/shared:/shared -v $PWD/certs:/certs:ro -e GH_CONFIG_DIR=/shared/gh -e SSL_CERT_FILE=/certs/ca.pem -e GH_NO_UPDATE_NOTIFIER=1 -e GH_PROMPT_DISABLED=1)
cat > shared/gh/hosts.yml <<'Y'
ghe.example.test:
    oauth_token: tok-A
    user: alice
    git_protocol: https
Y
echo "== version"; "${GH[@]}" alpine:3.22 gh --version
echo "== T1 tok-A direct"; "${GH[@]}" alpine:3.22 gh api --hostname ghe.example.test user; echo " exit=$?"
sed -i '' 's/tok-A/tok-B/' shared/gh/hosts.yml
echo "== T2 tok-B direct"; "${GH[@]}" alpine:3.22 gh api --hostname ghe.example.test user; echo " exit=$?"
echo "== T3 GH_ENTERPRISE_TOKEN"; "${GH[@]}" -e GH_ENTERPRISE_TOKEN=tok-ENV alpine:3.22 gh api --hostname ghe.example.test user; echo " exit=$?"
echo "== T3b GH_TOKEN"; "${GH[@]}" -e GH_TOKEN=tok-GHTOKEN alpine:3.22 gh api --hostname ghe.example.test user; echo " exit=$?"
echo "== T3c GH_HOST, no --hostname"; "${GH[@]}" -e GH_HOST=ghe.example.test alpine:3.22 gh api user; echo " exit=$?"
echo "== T4 SSL_CERT_FILE unset"; "${GH[@]}" -e SSL_CERT_FILE= alpine:3.22 gh api --hostname ghe.example.test user; echo " exit=$?"
echo "== cat hosts.yml after"; cat shared/gh/hosts.yml; ls -la shared/gh
