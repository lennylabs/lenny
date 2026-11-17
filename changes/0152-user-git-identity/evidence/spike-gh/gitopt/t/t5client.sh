#!/bin/sh
# t5client.sh <candidate> <client>: a git library with its defaults and the design's profile. Runs inside the client's own image.
. /t/env.sh 2>/dev/null
C=$1; K=$2; U=https://ghes.example.test/acme/widgets.git; D=$(mktemp -d)/x
cp /t/cfg/$C.gitconfig $HOME/.gitconfig
export GIT_SSL_CAINFO=/s/bundle.pem
S=$(date -u +%FT%T.%NZ)
case $K in
 go-git) timeout -k 3 45 /b/goclient $U $D 2>&1 | tail -2 | cut -c1-260;;
 pygit2-default) timeout -k 3 45 python3 -I -c "
import pygit2,sys
print('libgit2',pygit2.LIBGIT2_VERSION,'features',pygit2.features)
r=pygit2.clone_repository('$U','$D'); print('cloned',str(r.head.target)[:12])" 2>&1 | tail -3 | cut -c1-260;;
 pygit2-proxy-auto) timeout -k 3 45 python3 -I -c "
import pygit2
r=pygit2.clone_repository('$U','$D',proxy=True); print('cloned',str(r.head.target)[:12])" 2>&1 | tail -2 | cut -c1-260;;
 pygit2-proxy-explicit) case $C in P*) PX=http://127.0.0.1:3129;; *) PX=http://127.0.0.1:3128;; esac; timeout -k 3 45 python3 -I -c "
import pygit2
r=pygit2.clone_repository('$U','$D',proxy='$PX'); print('cloned',str(r.head.target)[:12])" 2>&1 | tail -2 | cut -c1-260;;
 jgit) T=$(mktemp -d)/t.p12; i=0; awk -v d=$(dirname $T) '/BEGIN/{i++} {print > d"/c"i".pem"}' /s/bundle.pem; for f in $(dirname $T)/c*.pem; do keytool -importcert -noprompt -alias $(basename $f) -file $f -keystore $T -storepass changeit -storetype PKCS12 >/dev/null 2>&1; done
  export JAVA_TOOL_OPTIONS="-Dhttps.proxyHost=127.0.0.1 -Dhttps.proxyPort=3128 -Dhttp.proxyHost=127.0.0.1 -Dhttp.proxyPort=3128 -Djavax.net.ssl.trustStore=$T -Djavax.net.ssl.trustStorePassword=changeit -Djavax.net.ssl.trustStoreType=PKCS12"
  cd /t/jgit && timeout -k 3 60 java -cp "/j/*" J.java $U $D 2>&1 | grep -vE "SLF4J|Picked up|^\s+at " | head -6 | cut -c1-300;;
esac
RC=$?
E=$(date -u +%FT%T.%NZ)
[ -n "$STEPS" ] && printf '{"label":"%s-%s","start":"%s","end":"%s","rc":%s}\n' "$C" "$K" "$S" "$E" "$RC" >> $STEPS
[ -d "$D/.git" ] && echo "clone-dir-present rc=$RC" || echo "clone-dir-absent rc=$RC"
