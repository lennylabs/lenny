#!/bin/bash
cd "$(dirname "$0")/.."
B=(docker run --rm --network spk-int -v $PWD/dl/glab/bin/glab:/usr/local/bin/glab:ro -v $PWD/certs:/certs:ro -e SSL_CERT_FILE=/certs/ca.pem -e GLAB_NO_PROMPT=1 -e HTTPS_PROXY=http://proxy:3128)
cnt() { docker logs spk-proxy 2>&1 | grep -c "CONNECT gitlab.com"; }
mk() { rm -rf shared/$1; mkdir -p shared/$1; printf "hosts:\n    gitlab.example.test:\n        token: tok-X\n        api_host: gitlab.example.test\n        api_protocol: https\n$2" > shared/$1/config.yml; chmod 600 shared/$1/config.yml; }
for v in "" "check_update: false\n" "telemetry: false\n" "check_update: false\ntelemetry: false\n"; do
 for i in 1 2 3; do mk g-r "$v"; b=$(cnt); "${B[@]}" -v $PWD/shared:/shared -e GLAB_CONFIG_DIR=/shared/g-r alpine:3.22 glab api --hostname gitlab.example.test user >/dev/null 2>&1; echo "cfg=[$(printf "$v"|tr '\n' ' ')] run$i gitlab.com CONNECTs=$(( $(cnt) - b ))"; done
done
mk g-r ""; b=$(cnt); "${B[@]}" -v $PWD/shared:/shared -e GLAB_CONFIG_DIR=/shared/g-r -e GLAB_CHECK_UPDATE=false -e GLAB_SEND_TELEMETRY=false alpine:3.22 glab api --hostname gitlab.example.test user >/dev/null 2>&1; echo "env GLAB_CHECK_UPDATE=false GLAB_SEND_TELEMETRY=false: CONNECTs=$(( $(cnt) - b ))"
mk g-r ""; "${B[@]}" -v $PWD/shared:/shared -e GLAB_CONFIG_DIR=/shared/g-r -e HTTPS_PROXY= alpine:3.22 sh -c 'glab api --hostname gitlab.example.test user >/dev/null; echo ---; glab check-update 2>&1 | head -3' 2>&1 | grep -v DEPREC
rm -rf shared/g-k8s; mkdir -p shared/g-k8s/..2026_v1; printf "hosts:\n    gitlab.example.test:\n        token: tok-K8S\n        api_host: gitlab.example.test\n        api_protocol: https\ncheck_update: false\n" > shared/g-k8s/..2026_v1/config.yml; chmod 600 shared/g-k8s/..2026_v1/config.yml; ln -s ..2026_v1 shared/g-k8s/..data; ln -s ..data/config.yml shared/g-k8s/config.yml
echo "== k8s-secret-style symlinked config 0600, RO"; "${B[@]}" -u 1000:1000 -v $PWD/shared/g-k8s:/cfg:ro -e GLAB_CONFIG_DIR=/cfg alpine:3.22 glab api --hostname gitlab.example.test user 2>&1 | grep -v DEPREC; echo
