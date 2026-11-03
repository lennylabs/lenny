#!/bin/bash
# Item 3 part 1: the Go ls-refs client with credentials against a real Gitea, directly and through Envoy's raw tunnel and DFP route.
D=$(cd "$(dirname "$0")" && pwd); O=$D/..; S=$O/..; . $D/tokens.env
G=gitea.internal.oa.test; PR=https://$G/alice/priv.git; PU=https://$G/alice/pub.git
docker rm -f oa-authz oa-envoy oa-cl1 >/dev/null 2>&1
docker run -d --name oa-authz --network oa-net --ip 198.19.50.11 --cpus 0.5 -m 128m -e NAME=authz1 -e PIN=172.30.50.22:443 -v $S/spike-lsrefs/bin:/b:ro alpine:3.22 /b/authz >/dev/null
docker run -d --name oa-cl1 --network oa-net --ip 198.19.50.30 --cpus 1 -v $O/bin:/b:ro -v $O/certs:/c:ro oa-git sleep infinity >/dev/null
bash $O/item2/envoy-gen.sh >/dev/null 2>&1
docker create --name oa-envoy --network oa-net --ip 198.19.50.41 --cpus 2 -m 512m --ulimit nofile=65536:65536 -v $O/item2/envoy/final.json:/etc/envoy/envoy.yaml:ro envoyproxy/envoy:v1.40.2 -c /etc/envoy/envoy.yaml --concurrency 2 --log-level warn >/dev/null
docker network connect --ip 172.30.50.41 oa-priv oa-envoy; docker start oa-envoy >/dev/null
for i in $(seq 1 40); do docker exec oa-cl1 curl -sf http://198.19.50.41:9901/ready >/dev/null 2>&1 && break; /bin/sleep 0.5; done
PSHA=$(docker exec -e GIT_SSL_CAINFO=/c/ca.pem oa-cl2 git ls-remote https://alice:$A@$G/alice/priv.git refs/heads/main | cut -f1)
PUSHA=$(docker exec -e GIT_SSL_CAINFO=/c/ca.pem oa-cl2 git ls-remote https://$G/alice/pub.git refs/heads/main | cut -f1)
PTAG=$(docker exec -e GIT_SSL_CAINFO=/c/ca.pem oa-cl2 git ls-remote https://alice:$A@$G/alice/priv.git 'refs/tags/v1^{}' | cut -f1)
echo "git ls-remote: priv main=$PSHA priv v1 peeled=$PTAG pub main=$PUSHA"
one(){ # one PATH LABEL auth repo ref [flags]
  p=$1; l=$2; a=$3; r=$4; f=$5; shift 5
  case $p in
   direct) c=oa-cl2; X="";;
   raw) c=oa-cl1; X="-proxy http://198.19.50.41:3128 -token pin";;
   dfp) c=oa-cl1; X="-proxy http://198.19.50.41:3128 -token good";;
  esac
  printf '%-7s %-36s ' "$p" "$l"
  docker exec $c /b/lsrefs -mode one -ca /c/ca.pem $X ${a:+-auth "$a"} -repo $r -ref $f "$@" | jq -c '{class,code,reason,sha:(.sha[0:10]),proto,host_status,requests,ms,err}'
}
for p in direct raw dfp; do
  [ $p = dfp ] && { echo "--- dfp resolves the name inside Envoy, which is on the private network here"; }
  for v in "" -v0; do
    echo "=== path=$p protocol=${v:-v2 then v0}"
    one $p "valid full token, private, main" "alice:$A" $PR main $v
    one $p "valid read-only token, private" "alice:$AR" $PR main $v
    one $p "token as x-access-token user" "x-access-token:$AR" $PR main $v
    one $p "valid token, annotated tag" "alice:$AR" $PR v1 $v
    one $p "valid token, absent ref" "alice:$AR" $PR nosuch $v
    one $p "invalid token, private" "alice:0000000000000000000000000000000000000000" $PR main $v
    one $p "no credential, private" "" $PR main $v
    one $p "token without repository scope" "alice:$AU" $PR main $v
    one $p "other user's token, private" "bob:$B" $PR main $v
    one $p "valid token, absent repo" "alice:$AR" https://$G/alice/none.git main $v
    one $p "no credential, absent repo" "" https://$G/alice/none.git main $v
    one $p "no credential, public" "" $PU main $v
    one $p "invalid token, public" "alice:0000000000000000000000000000000000000000" $PU main $v
    one $p "without .git, valid token" "alice:$AR" https://$G/alice/priv main $v
  done
done
echo "=== egress refusals carry the classification, and the host credential is never tried"
printf 'CONNECT 403 ' ; docker exec oa-cl1 /b/lsrefs -mode one -ca /c/ca.pem -proxy http://198.19.50.41:3128 -token bad -auth "alice:$A" -repo $PR | jq -c '{class,code,reason,host_status,err}'
printf 'CONNECT 407 ' ; docker exec oa-cl1 /b/lsrefs -mode one -ca /c/ca.pem -proxy http://198.19.50.41:3128 -token "" -auth "alice:$A" -repo $PR | jq -c '{class,code,reason,host_status,err}'
docker stop -t0 oa-authz >/dev/null; /bin/sleep 1
printf 'decision down ' ; docker exec oa-cl1 /b/lsrefs -mode one -ca /c/ca.pem -proxy http://198.19.50.41:3128 -token pin -auth "alice:$A" -repo $PR | jq -c '{class,code,reason,host_status,err,ms}'
docker start oa-authz >/dev/null; /bin/sleep 2
docker stop -t0 oa-gitea >/dev/null; /bin/sleep 1
printf 'host stopped, raw route ' ; docker exec oa-cl1 /b/lsrefs -mode one -ca /c/ca.pem -proxy http://198.19.50.41:3128 -token pin -auth "alice:$A" -repo $PR | jq -c '{class,code,reason,host_status,err,ms}'
docker start oa-gitea >/dev/null
for i in $(seq 1 60); do docker exec oa-cl2 curl -s --cacert /c/ca.pem -o /dev/null https://$G/api/healthz && break; /bin/sleep 1; done
echo "=== a repository that forces v0: a front that strips Git-Protocol, then one that answers v2 with an error"
docker rm -f oa-fault >/dev/null 2>&1
docker run -d --name oa-fault --network oa-priv --ip 172.30.50.23 --network-alias fault.oa.test --cpus 1 -m 128m -v $O/bin:/b:ro -v $O/certs:/c:ro alpine:3.22 /b/faultfe https://172.30.50.22:443 /c/gitea/tls.crt /c/gitea/tls.key /c/target/tls.crt /c/target/tls.key >/dev/null
/bin/sleep 1
F="docker exec oa-cl2 curl -sS http://172.30.50.23:9100/set?mode="
fc(){ docker exec oa-cl2 /b/lsrefs -mode one -insecure -auth "$1" -repo https://172.30.50.23/alice/priv.git -ref $2 | jq -c '{class,code,reason,sha:(.sha[0:10]),proto,host_status,requests,ms,err}'; }
for m in v0only fail-v2:400 fail-v2:403 fail-v2:404 fail-v2:415 fail-v2:500; do $F$m >/dev/null; echo "--- $m"; printf 'valid token main   '; fc "alice:$AR" main; printf 'valid token absent '; fc "alice:$AR" nosuch; printf 'invalid token      '; fc "alice:badbadbad" main; done
$F"pass" >/dev/null
