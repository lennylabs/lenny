#!/bin/bash
cd "$(dirname "$0")"
K="kubectl --context kind-lenny-userns"
kind load docker-image spike-userns:4 --name lenny-userns 2>&1 | tail -1 | cut -c1-40
$K -n mir delete pod --all --now >/dev/null 2>&1
mk(){ sed -e "s/NAME/$1/" -e "s/NS/mir/" -e "s/HOSTUSERS/$2/" -e "s#VOLUME#$3#" -e "s/FUVAL/$4/; s/GAVAL/$5/; s/GBVAL/$6/" pod.tmpl; }
mk host2 '{}: {}' 'emptyDir: {}' 100001 70001 70002 | sed '/{}: {}/d' | $K apply -f - >/dev/null
mk userns2 'hostUsers: false' 'persistentVolumeClaim: {claimName: db}' 50001 40001 40002 | $K apply -f - >/dev/null
for p in host2 userns2; do
  for i in $(seq 1 40); do s=$($K -n mir get pod $p -o jsonpath='{.status.phase}'); [ "$s" = Running ] && $K -n mir logs $p 2>/dev/null | grep -q marker && break; [ "$s" = Failed ] && break; /bin/sleep 2; done
  echo "################ $p: phase=$s"; $K -n mir logs $p 2>&1 | cut -c1-170 | sed -n '/fetch child/,$p'
done
echo "################ node view"; docker exec lenny-userns-control-plane sh -c "ps -eo uid,gid,args | grep -E 'sleep 60[01]' | grep -v grep" | cut -c1-70
docker exec lenny-userns-control-plane sh -c 'for d in /var/local-path-provisioner/*mir_db; do ls -ldn $d/a $d/b | awk "{print \"  \",\$1,\$3,\$4}"; ls -ln $d/a | awk "NR>1{print \"   a/\",\$1,\$3,\$4,\$9}"; done'
echo "################ Kubernetes 1.31.4, where the feature gate is off by default"
kind create cluster --name lenny-userns31 --image kindest/node:v1.31.4 --wait 90s 2>&1 | tail -1 | cut -c1-60
K31="kubectl --context kind-lenny-userns31"; kind load docker-image spike-userns:4 --name lenny-userns31 2>&1 | tail -1 | cut -c1-40
$K31 create ns mir >/dev/null; $K31 -n mir create secret generic s --from-literal=key=x >/dev/null
mk old 'hostUsers: false' 'emptyDir: {}' 100001 70001 70002 | $K31 apply -f - 2>&1 | cut -c1-120
/bin/sleep 8; echo "stored hostUsers: [$($K31 -n mir get pod old -o jsonpath='{.spec.hostUsers}')] phase=$($K31 -n mir get pod old -o jsonpath='{.status.phase}')"; $K31 -n mir logs old 2>&1 | sed -n 2,3p | cut -c1-90
kind delete cluster --name lenny-userns31 2>&1 | tail -1 | cut -c1-50; kind delete cluster --name lenny-userns 2>&1 | tail -1 | cut -c1-50
