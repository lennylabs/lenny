#!/bin/bash
cd "$(dirname "$0")/.."
B=(docker run --rm --network spk-int -v $PWD/dl/gh/gh_2.103.3_linux_arm64/bin/gh:/usr/local/bin/gh:ro -v $PWD/shared:/shared -v $PWD/certs:/certs:ro -e SSL_CERT_FILE=/certs/ca.pem -e GH_NO_UPDATE_NOTIFIER=1 -e GH_PROMPT_DISABLED=1)
echo "== T8 GH_ENTERPRISE_TOKEN only, empty config dir, GH_HOST"; "${B[@]}" -e GH_CONFIG_DIR=/shared/gh-empty -e GH_HOST=ghe.example.test -e GH_ENTERPRISE_TOKEN=tok-ENVONLY alpine:3.22 gh api user; echo " exit=$?"
echo "== T8b GH_ENTERPRISE_TOKEN only, no GH_HOST, --hostname"; "${B[@]}" -e GH_CONFIG_DIR=/shared/gh-empty -e GH_ENTERPRISE_TOKEN=tok-ENVONLY alpine:3.22 gh api --hostname ghe.example.test user; echo " exit=$?"
mkdir -p shared/gh-multi
cat > shared/gh-multi/hosts.yml <<'Y'
ghe.example.test:
    users:
        alice:
            oauth_token: tok-M1
    git_protocol: https
    oauth_token: tok-M1
    user: alice
Y
echo "== T9 multi-account format tok-M1"; "${B[@]}" -e GH_CONFIG_DIR=/shared/gh-multi alpine:3.22 gh api --hostname ghe.example.test user; echo " exit=$?"
sed -i '' 's/tok-M1/tok-M2/g' shared/gh-multi/hosts.yml
echo "== T9b multi-account tok-M2"; "${B[@]}" -e GH_CONFIG_DIR=/shared/gh-multi alpine:3.22 gh api --hostname ghe.example.test user; echo " exit=$?"
echo "== T10 persistent container, two invocations, file rewritten between"
docker run -d --name spk-ghc --network spk-int -v $PWD/dl/gh/gh_2.103.3_linux_arm64/bin/gh:/usr/local/bin/gh:ro -v $PWD/shared:/shared -v $PWD/certs:/certs:ro -e SSL_CERT_FILE=/certs/ca.pem -e GH_CONFIG_DIR=/shared/gh -e GH_NO_UPDATE_NOTIFIER=1 alpine:3.22 sleep 300 >/dev/null
printf 'ghe.example.test:\n    oauth_token: tok-P1\n    user: alice\n' > shared/gh/hosts.yml
docker exec spk-ghc gh api --hostname ghe.example.test user; echo
printf 'ghe.example.test:\n    oauth_token: tok-P2\n    user: alice\n' > shared/gh/hosts.yml.tmp && mv shared/gh/hosts.yml.tmp shared/gh/hosts.yml
docker exec spk-ghc gh api --hostname ghe.example.test user; echo
docker rm -f spk-ghc >/dev/null
