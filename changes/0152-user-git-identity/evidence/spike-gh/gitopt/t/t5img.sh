#!/bin/sh
# t5img.sh: the git shipped in a runtime image, with each candidate config. No install: an image without git is reported as such.
IMG=$1; export HOME=/tmp/h; mkdir -p $HOME
export HTTPS_PROXY=http://127.0.0.1:3128 https_proxy=http://127.0.0.1:3128 HTTP_PROXY=http://127.0.0.1:3128 SSL_CERT_FILE=/s/bundle.pem GIT_TERMINAL_PROMPT=0
if ! command -v git >/dev/null; then echo "$IMG: no git in the image"; exit 0; fi
V=$(git --version | cut -d' ' -f3); P=$(git --exec-path)/git-remote-https; L=$( (ldd $P 2>/dev/null || true) | grep -oE 'gnutls|libssl|nss3' | sort -u | tr '\n' ' ')
for c in $CANDS; do
 export GIT_CONFIG_GLOBAL=/t/cfg/$c.gitconfig
 case $c in T|Tb) unset GIT_SSL_CAINFO;; *) export GIT_SSL_CAINFO=/s/bundle.pem;; esac
 D=$(mktemp -d); git clone -q https://ghes.example.test/acme/widgets.git $D/x >/tmp/o 2>&1; rc=$?
 printf '%s git=%s tls=%s cand=%s clone-rc=%s %s\n' "$IMG" "$V" "$L" "$c" "$rc" "$(tail -1 /tmp/o | cut -c1-130)"
done
