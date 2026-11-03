#!/bin/bash
# usage: runloop.sh TAG EXPIRY LOOP INTERVAL ROTATE_AT
cd "$(dirname "$0")"; . ./env.sh
TAG=$1 EXP=$2 LOOP=$3 IV=$4 ROT=$5
mkdir -p out-$TAG
docker rm -f spike-fakes3 >/dev/null 2>&1
docker run -d --name spike-fakes3 --label spike-formats=1 --network spike-net --network-alias fakes3 -v $PWD/servers:/srv:ro python:3.13-slim python -u /srv/fakes3.py >/dev/null
for c in py go js java; do
  docker rm -f spike-cred-$c spike-cli-$c >/dev/null 2>&1
  printf 'tok1-%s' $c > tok-$c/token
  docker run -d --name spike-cred-$c --label spike-formats=1 --network spike-net -e NAME=$c -e EXPIRY_SECONDS=$EXP -v $PWD/servers:/srv:ro -v $PWD/tok-$c:/tok python:3.13-slim python -u /srv/credsrv.py >/dev/null
done
sleep 2
L=(-e LOOP_SECONDS=$LOOP -e INTERVAL=$IV)
docker run -d --name spike-cli-py --label spike-formats=1 --network container:spike-cred-py -v $PWD/tok-py:/tok:ro "${AENV[@]}" "${L[@]}" spike-a-py python -u /loop.py >/dev/null
docker run -d --name spike-cli-go --label spike-formats=1 --network container:spike-cred-go -v $PWD/tok-go:/tok:ro "${AENV[@]}" "${L[@]}" spike-a-go /loop >/dev/null
docker run -d --name spike-cli-js --label spike-formats=1 --network container:spike-cred-js -v $PWD/tok-js:/tok:ro "${AENV[@]}" "${L[@]}" spike-a-js node /app/loop.mjs >/dev/null
docker run -d --name spike-cli-java --label spike-formats=1 --network container:spike-cred-java -v $PWD/tok-java:/tok:ro "${AENV[@]}" "${L[@]}" spike-a-java java -cp '/app/spike.jar:/app/lib/*' spike.Main >/dev/null
echo "started $(date -u +%T)"
if [ "$ROT" -gt 0 ]; then sleep $ROT; for c in py go js java; do printf 'tok2-%s' $c > tok-$c/token; done; echo "rotated $(date -u +%T)"; fi
for c in py go js java; do docker wait spike-cli-$c >/dev/null; done
echo "done $(date -u +%T)"
for c in py go js java; do docker logs spike-cli-$c > out-$TAG/client-$c.log 2>&1; docker logs spike-cred-$c > out-$TAG/cred-$c.log 2>&1; done
docker logs spike-fakes3 > out-$TAG/s3.log 2>&1
