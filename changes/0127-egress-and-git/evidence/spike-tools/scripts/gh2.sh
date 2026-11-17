#!/bin/bash
cd "$(dirname "$0")/.."
B=(docker run --rm --network spk-int -v $PWD/dl/gh/gh_2.103.3_linux_arm64/bin/gh:/usr/local/bin/gh:ro -v $PWD/certs:/certs:ro -e SSL_CERT_FILE=/certs/ca.pem -e GH_NO_UPDATE_NOTIFIER=1 -e GH_PROMPT_DISABLED=1)
mkdir -p shared/gh-ro shared/gh-empty
printf 'ghe.example.test:\n    oauth_token: tok-RO\n    user: alice\n    git_protocol: https\n' > shared/gh-ro/hosts.yml
echo "== T5 HTTPS_PROXY=http://proxy:3128"; "${B[@]}" -v $PWD/shared:/shared -e GH_CONFIG_DIR=/shared/gh -e HTTPS_PROXY=http://proxy:3128 alpine:3.22 gh api --hostname ghe.example.test user; echo " exit=$?"
echo "== T5b https_proxy lowercase"; "${B[@]}" -v $PWD/shared:/shared -e GH_CONFIG_DIR=/shared/gh -e https_proxy=http://proxy:3128 alpine:3.22 gh api --hostname ghe.example.test user; echo " exit=$?"
echo "== T5c HTTPS_PROXY + NO_PROXY=.example.test"; "${B[@]}" -v $PWD/shared:/shared -e GH_CONFIG_DIR=/shared/gh -e HTTPS_PROXY=http://proxy:3128 -e NO_PROXY=.example.test alpine:3.22 gh api --hostname ghe.example.test user; echo " exit=$?"
echo "== T5d github.com via proxy (unresolvable from client), bogus GH_TOKEN, system CAs"; "${B[@]}" -v $PWD/shared:/shared -e GH_CONFIG_DIR=/shared/gh-empty -e SSL_CERT_FILE= -e GH_TOKEN=bogus -e HTTPS_PROXY=http://proxy:3128 alpine:3.22 gh api zen; echo " exit=$?"
echo "== T6 no token at all (empty config dir)"; "${B[@]}" -v $PWD/shared:/shared -e GH_CONFIG_DIR=/shared/gh-empty alpine:3.22 gh api --hostname ghe.example.test user; echo " exit=$?"
echo "== T6b no token, github.com"; "${B[@]}" -v $PWD/shared:/shared -e GH_CONFIG_DIR=/shared/gh-empty alpine:3.22 gh api zen; echo " exit=$?"
echo "== T7 read-only bind mount, non-root uid"; "${B[@]}" -u 1000:1000 -v $PWD/shared/gh-ro:/shared/gh:ro -e GH_CONFIG_DIR=/shared/gh alpine:3.22 gh api --hostname ghe.example.test user; echo " exit=$?"
echo "== T7b read-only + gh auth status"; "${B[@]}" -u 1000:1000 -v $PWD/shared/gh-ro:/shared/gh:ro -e GH_CONFIG_DIR=/shared/gh alpine:3.22 gh auth status --hostname ghe.example.test; echo " exit=$?"
echo "== T7c read-only + gh config set (expected write)"; "${B[@]}" -u 1000:1000 -v $PWD/shared/gh-ro:/shared/gh:ro -e GH_CONFIG_DIR=/shared/gh alpine:3.22 gh config set editor vi; echo " exit=$?"
ls -la shared/gh-empty shared/gh-ro
