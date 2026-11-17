#!/bin/sh
# t10.sh <image-label>: gh and glab on a runtime image with its own libc, timezone data, and locale. No install.
IMG=$1; export HOME=/tmp/h; mkdir -p $HOME
export HTTPS_PROXY=http://127.0.0.1:3128 https_proxy=http://127.0.0.1:3128 HTTP_PROXY=http://127.0.0.1:3128 SSL_CERT_FILE=/s/bundle.pem GH_NO_UPDATE_NOTIFIER=1 GH_PROMPT_DISABLED=1 GH_PAGER=cat GH_CONFIG_DIR=$HOME/.gh GLAB_CONFIG_DIR=$HOME/.glab GLAB_CHECK_UPDATE=false GLAB_SEND_TELEMETRY=false NO_COLOR=1 PATH=/g:$PATH
G="env GH_HOST=ghes.example.test GH_ENTERPRISE_TOKEN=lenny-injected"; C="env GH_TOKEN=lenny-injected"; L="env GITLAB_HOST=gitlab.example.test GITLAB_TOKEN=lenny-injected"
t(){ l=$1; shift; o=$("$@" 2>&1); r=$?; printf '%s %s rc=%s %s\n' "$IMG" "$l" "$r" "$(echo "$o" | tail -1 | cut -c1-120)"; }
printf '%s tz=[%s] localtime=%s lang=[%s] git=%s\n' "$IMG" "$TZ" "$(ls -la /etc/localtime 2>/dev/null | sed 's/.*-> //' | cut -c1-40)" "$LANG" "$(command -v git || echo none)"
t ghes-list $G gh pr list -R ghes.example.test/acme/widgets
t ghes-view $G gh pr view 7 -R ghes.example.test/acme/widgets --json number
t ghes-create $G gh pr create -R ghes.example.test/acme/widgets --title t --body b --head f-$$
t ghes-diff $G gh pr diff 7 -R ghes.example.test/acme/widgets
t com-list $C gh pr list -R acme/widgets
t com-create $C gh pr create -R acme/widgets --title t --body b --head f-$$-c
t tz-ny env TZ=America/New_York $G gh pr list -R ghes.example.test/acme/widgets
t lang-de env LANG=de_DE.UTF-8 LC_ALL=de_DE.UTF-8 $G gh pr list -R ghes.example.test/acme/widgets
t gl-list $L glab mr list -R gitlab.example.test/acme/widgets
t gl-view $L glab mr view 7 -R gitlab.example.test/acme/widgets
t gl-create $L glab mr create -R gitlab.example.test/acme/widgets --title t --description d --source-branch feature --target-branch main --yes
t gl-tz env TZ=America/New_York $L glab mr list -R gitlab.example.test/acme/widgets
