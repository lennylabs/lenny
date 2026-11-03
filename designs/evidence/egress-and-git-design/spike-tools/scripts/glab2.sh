#!/bin/bash
cd "$(dirname "$0")/.."
B=(docker run --rm --network spk-int -v $PWD/dl/glab/bin/glab:/usr/local/bin/glab:ro -v $PWD/certs:/certs:ro -e SSL_CERT_FILE=/certs/ca.pem -e GLAB_NO_PROMPT=1)
cnt() { docker logs spk-proxy 2>&1 | grep -c "CONNECT gitlab.com"; }
mk() { rm -rf shared/$1; mkdir -p shared/$1; cat > shared/$1/config.yml <<Y
hosts:
    gitlab.example.test:
        token: $2
        api_host: gitlab.example.test
        api_protocol: $3
        git_protocol: https
$4
Y
chmod 600 shared/$1/config.yml; }
mk glab-p1 tok-P https ""
echo "== G7 gitlab.com contact baseline (proxy set, no knobs) before=$(cnt)"; "${B[@]}" -v $PWD/shared:/shared -e GLAB_CONFIG_DIR=/shared/glab-p1 -e HTTPS_PROXY=http://proxy:3128 alpine:3.22 glab api --hostname gitlab.example.test user >/dev/null 2>&1; echo " after=$(cnt)"
echo "== G7b second run same dir (last_seen_version set) before=$(cnt)"; "${B[@]}" -v $PWD/shared:/shared -e GLAB_CONFIG_DIR=/shared/glab-p1 -e HTTPS_PROXY=http://proxy:3128 alpine:3.22 glab api --hostname gitlab.example.test user >/dev/null 2>&1; echo " after=$(cnt)"
mk glab-p2 tok-P https "check_update: false
telemetry: false"
echo "== G7c check_update:false telemetry:false in config before=$(cnt)"; "${B[@]}" -v $PWD/shared:/shared -e GLAB_CONFIG_DIR=/shared/glab-p2 -e HTTPS_PROXY=http://proxy:3128 alpine:3.22 glab api --hostname gitlab.example.test user >/dev/null 2>&1; echo " after=$(cnt)"
mk glab-p3 tok-P http ""
echo "== G8 api_protocol http + HTTP_PROXY"; "${B[@]}" -v $PWD/shared:/shared -e GLAB_CONFIG_DIR=/shared/glab-p3 -e HTTP_PROXY=http://proxy:3128 -e HTTPS_PROXY=http://proxy:3128 alpine:3.22 glab api --hostname gitlab.example.test user; echo " exit=$?"
mk glab-ro tok-RO https "check_update: false
telemetry: false"
echo "== G9 read-only config dir, uid 1000"; "${B[@]}" -u 1000:1000 -v $PWD/shared/glab-ro:/cfg:ro -e GLAB_CONFIG_DIR=/cfg alpine:3.22 glab api --hostname gitlab.example.test user; echo " exit=$?"
echo "== G9b read-only config dir, uid 1000, 0644 perms"; chmod 644 shared/glab-ro/config.yml; "${B[@]}" -u 1000:1000 -v $PWD/shared/glab-ro:/cfg:ro -e GLAB_CONFIG_DIR=/cfg alpine:3.22 glab api --hostname gitlab.example.test user; echo " exit=$?"
rm -rf shared/glab-none; mkdir -p shared/glab-none
echo "== G10 no token at all"; "${B[@]}" -v $PWD/shared:/shared -e GLAB_CONFIG_DIR=/shared/glab-none alpine:3.22 glab api --hostname gitlab.example.test user; echo " exit=$?"
rm -rf shared/glab-none; mkdir -p shared/glab-none
echo "== G11 GITLAB_TOKEN only, empty config dir"; "${B[@]}" -v $PWD/shared:/shared -e GLAB_CONFIG_DIR=/shared/glab-none -e GITLAB_TOKEN=tok-ENVONLY alpine:3.22 glab api --hostname gitlab.example.test user; echo " exit=$?"
echo "== G11b GITLAB_TOKEN + GITLAB_HOST only, empty ro dir"; rm -rf shared/glab-none; mkdir -p shared/glab-none; "${B[@]}" -u 1000:1000 -v $PWD/shared/glab-none:/cfg:ro -e GLAB_CONFIG_DIR=/cfg -e GITLAB_HOST=gitlab.example.test -e GITLAB_TOKEN=tok-ENVONLY2 alpine:3.22 glab api user; echo " exit=$?"
ls -la shared/glab-p1 shared/glab-ro; cat shared/glab-p2/config.yml
