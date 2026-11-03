#!/bin/bash
cd "$(dirname "$0")/.."
B=(docker run --rm --network spk-int -v $PWD/dl/glab/bin/glab:/usr/local/bin/glab:ro -v $PWD/shared:/shared -v $PWD/certs:/certs:ro -e SSL_CERT_FILE=/certs/ca.pem -e GLAB_CONFIG_DIR=/shared/glab -e GLAB_CHECK_UPDATE=false -e NO_PROMPT=1)
mkdir -p shared/glab
writecfg() { cat > shared/glab/config.yml <<Y
hosts:
    gitlab.example.test:
        token: $1
        api_host: gitlab.example.test
        api_protocol: $2
        git_protocol: https
Y
chmod 600 shared/glab/config.yml; }
echo "== version"; "${B[@]}" alpine:3.22 glab version
writecfg tok-A https
echo "== G1 https tok-A"; "${B[@]}" alpine:3.22 glab api --hostname gitlab.example.test user; echo " exit=$?"
writecfg tok-B https
echo "== G2 https tok-B (re-read)"; "${B[@]}" alpine:3.22 glab api --hostname gitlab.example.test user; echo " exit=$?"
writecfg tok-C http
echo "== G3 api_protocol http"; "${B[@]}" alpine:3.22 glab api --hostname gitlab.example.test user; echo " exit=$?"
writecfg tok-B https
echo "== G4 GITLAB_TOKEN env"; "${B[@]}" -e GITLAB_TOKEN=tok-ENV alpine:3.22 glab api --hostname gitlab.example.test user; echo " exit=$?"
echo "== G4b GITLAB_HOST env + GITLAB_TOKEN, no --hostname"; "${B[@]}" -e GITLAB_HOST=gitlab.example.test -e GITLAB_TOKEN=tok-ENV2 alpine:3.22 glab api user; echo " exit=$?"
echo "== G5 SSL_CERT_FILE unset"; "${B[@]}" -e SSL_CERT_FILE= alpine:3.22 glab api --hostname gitlab.example.test user; echo " exit=$?"
echo "== G6 HTTPS_PROXY"; "${B[@]}" -e HTTPS_PROXY=http://proxy:3128 alpine:3.22 glab api --hostname gitlab.example.test user; echo " exit=$?"
echo "== cfg after"; cat shared/glab/config.yml; ls -la shared/glab
