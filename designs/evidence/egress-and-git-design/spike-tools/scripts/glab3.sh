#!/bin/bash
cd "$(dirname "$0")/.."
B=(docker run --rm --network spk-int -v $PWD/dl/glab/bin/glab:/usr/local/bin/glab:ro -v $PWD/certs:/certs:ro -e SSL_CERT_FILE=/certs/ca.pem -e GLAB_NO_PROMPT=1 -e HTTPS_PROXY=http://proxy:3128)
cnt() { docker logs spk-proxy 2>&1 | grep -c "CONNECT gitlab.com"; }
mk() { rm -rf shared/$1; mkdir -p shared/$1; printf "hosts:\n    gitlab.example.test:\n        token: tok-X\n        api_host: gitlab.example.test\n        api_protocol: https\n$2" > shared/$1/config.yml; chmod 600 shared/$1/config.yml; }
mk g-a ""; echo "== env GLAB_CHECK_UPDATE=false only: before=$(cnt)"; "${B[@]}" -v $PWD/shared:/shared -e GLAB_CONFIG_DIR=/shared/g-a -e GLAB_CHECK_UPDATE=false alpine:3.22 glab api --hostname gitlab.example.test user >/dev/null 2>&1; echo " after=$(cnt)"
mk g-b "check_update: false\n"; echo "== config check_update:false only: before=$(cnt)"; "${B[@]}" -v $PWD/shared:/shared -e GLAB_CONFIG_DIR=/shared/g-b alpine:3.22 glab api --hostname gitlab.example.test user >/dev/null 2>&1; echo " after=$(cnt)"
mk g-c "telemetry: false\n"; echo "== config telemetry:false only: before=$(cnt)"; "${B[@]}" -v $PWD/shared:/shared -e GLAB_CONFIG_DIR=/shared/g-c alpine:3.22 glab api --hostname gitlab.example.test user >/dev/null 2>&1; echo " after=$(cnt)"
echo "== telemetry POSTs to target so far: $(docker logs spk-fake 2>&1 | grep -c track_event)"
rm -rf shared/g-d; mkdir -p shared/g-d
echo "== GITLAB_HOST+GITLAB_TOKEN, empty writable dir"; "${B[@]}" -v $PWD/shared:/shared -e GLAB_CONFIG_DIR=/shared/g-d -e GITLAB_HOST=gitlab.example.test -e GITLAB_TOKEN=tok-HOSTENV alpine:3.22 glab api user 2>&1 | grep -v DEPREC; echo; ls -la shared/g-d
mk g-e "check_update: false\n"; sed -i '' 's/gitlab.example.test/other.example.test/g' shared/g-e/config.yml
echo "== GITLAB_HOST+GITLAB_TOKEN, RO dir with existing 0600 config for another host"; "${B[@]}" -u 1000:1000 -v $PWD/shared/g-e:/cfg:ro -e GLAB_CONFIG_DIR=/cfg -e GITLAB_HOST=gitlab.example.test -e GITLAB_TOKEN=tok-HOSTENV2 alpine:3.22 glab api user 2>&1 | grep -v DEPREC; echo
# k8s Secret-volume style layout: config.yml -> ..data/config.yml, file 0600 / 0400
rm -rf shared/g-k8s; mkdir -p shared/g-k8s/..2026_v1; printf "hosts:\n    gitlab.example.test:\n        token: tok-K8S\n        api_host: gitlab.example.test\n        api_protocol: https\ncheck_update: false\n" > shared/g-k8s/..2026_v1/config.yml; chmod 400 shared/g-k8s/..2026_v1/config.yml; ln -s ..2026_v1 shared/g-k8s/..data; ln -s ..data/config.yml shared/g-k8s/config.yml
echo "== k8s-secret-style symlinked config 0400, RO"; "${B[@]}" -u 1000:1000 -v $PWD/shared/g-k8s:/cfg:ro -e GLAB_CONFIG_DIR=/cfg alpine:3.22 glab api --hostname gitlab.example.test user 2>&1 | grep -v DEPREC; echo
