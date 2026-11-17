#!/bin/bash
cd "$(dirname "$0")"
K="kubectl --context kind-lenny-userns"
sed -i.bak 's/,"SETPCAP"//' pod.tmpl
$K create ns mir >/dev/null
$K -n mir create secret generic s --from-literal=key=sekret >/dev/null
for n in a b; do cat <<Y | $K apply -f - >/dev/null
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: d$n, namespace: mir}
spec: {accessModes: [ReadWriteOnce], resources: {requests: {storage: 100Mi}}}
Y
done
mk(){ # NAME HOSTUSERS CLAIM
  sed -e "s/NAME/$1/" -e "s/NS/mir/" -e "s/HOSTUSERS/$2/" -e "s/VOLUME/persistentVolumeClaim: {claimName: $3}/" pod.tmpl; }
mk host '{}: {}' da | sed '/{}: {}/d' | $K apply -f - >/dev/null
mk userns 'hostUsers: false' db | $K apply -f - >/dev/null
for p in host userns; do
  for i in $(seq 1 60); do s=$($K -n mir get pod $p -o jsonpath='{.status.phase}'); [ "$s" = Running ] && $K -n mir logs $p 2>/dev/null | grep -q marker && break; /bin/sleep 2; done
  echo "################ $p: phase=$s"; $K -n mir logs $p 2>&1 | cut -c1-170
  [ "$s" = Running ] || $K -n mir describe pod $p | tail -6 | cut -c1-200
done
echo "################ node view of the same processes"
docker exec lenny-userns-control-plane sh -c "ps -eo uid,gid,args | grep -E 'mirror-marker|sleep 600' | grep -v grep" | cut -c1-90
for p in host userns; do cid=$($K -n mir get pod $p -o jsonpath='{.status.containerStatuses[0].containerID}' | sed 's#.*//##'); [ -n "$cid" ] && echo "$p uid_map on node: $(docker exec lenny-userns-control-plane sh -c "pid=\$(crictl inspect --output go-template --template '{{.info.pid}}' $cid); cat /proc/\$pid/uid_map" | tr -s ' ')"; done
echo "################ on-disk owners, as the node sees them"
docker exec lenny-userns-control-plane sh -c 'for d in /var/local-path-provisioner/*mir_d*; do echo $d | sed "s#.*mir_##"; ls -ln $d | awk "NR>1{print \"  \",\$1,\$3,\$4,\$9}"; ls -ln $d/a | awk "NR>1{print \"    a/\",\$1,\$3,\$4,\$9}"; done'
echo "################ Pod Security admission (server dry run)"
for lvl in restricted baseline; do $K create ns ps-$lvl >/dev/null; $K label ns ps-$lvl pod-security.kubernetes.io/enforce=$lvl >/dev/null; $K -n ps-$lvl create secret generic s --from-literal=key=x >/dev/null
  for hu in unset false; do
    H='{}: {}'; [ $hu = false ] && H='hostUsers: false'
    sed -e "s/NAME/p-$hu/" -e "s/NS/ps-$lvl/" -e "s/HOSTUSERS/$H/" -e "s/VOLUME/emptyDir: {}/" pod.tmpl | sed '/{}: {}/d' > /tmp/p.yaml
    R=$($K apply --dry-run=server -f /tmp/p.yaml 2>&1 | tr '\n' ' ' | sed -E 's/.*(forbidden: violates PodSecurity[^:]*:)/\1/' | cut -c1-175)
    echo "$lvl hostUsers=$hu, mirror with three capabilities: $R"
    sed -e 's/add: \["SETUID","SETGID","CHOWN"\]/add: []/' /tmp/p.yaml > /tmp/p2.yaml
    R2=$($K apply --dry-run=server -f /tmp/p2.yaml 2>&1 | tr '\n' ' ' | sed -E 's/.*(forbidden: violates PodSecurity[^:]*:)/\1/' | cut -c1-175)
    echo "$lvl hostUsers=$hu, root with no capabilities  : $R2"
  done
done
echo "################ Does the three-capability root start work without the exception's capabilities"
sed -e "s/NAME/nocaps/" -e "s/NS/mir/" -e 's/HOSTUSERS/hostUsers: false/' -e "s/VOLUME/emptyDir: {}/" -e 's/add: \["SETUID","SETGID","CHOWN"\]/add: []/' pod.tmpl | $K apply -f - >/dev/null; /bin/sleep 8; $K -n mir logs nocaps 2>&1 | head -3 | cut -c1-140; $K -n mir get pod nocaps -o jsonpath='{.status.phase}{"\n"}'
