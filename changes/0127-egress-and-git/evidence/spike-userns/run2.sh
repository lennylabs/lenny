#!/bin/bash
cd "$(dirname "$0")"
K="kubectl --context kind-lenny-userns"
kind load docker-image spike-userns:3 --name lenny-userns 2>&1 | tail -1 | cut -c1-40
docker exec lenny-userns-control-plane sh -c 'ls -ld /kind /kind/bin /kind/bin/mount-product-files.sh | cut -c1-70; chmod a+rx /kind /kind/bin /kind/bin/mount-product-files.sh'
sed -i.bak3 's/spike-userns:[0-9]/spike-userns:3/; s#"/usr/bin/setpriv"#"setpriv"#' pod.tmpl
$K -n mir delete pod --all --now >/dev/null 2>&1; $K delete ns ps-restricted ps-baseline --now >/dev/null 2>&1
mk(){ sed -e "s/NAME/$1/" -e "s/NS/mir/" -e "s/HOSTUSERS/$2/" -e "s#VOLUME#$3#" pod.tmpl; }
mk host '{}: {}' 'persistentVolumeClaim: {claimName: da}' | sed '/{}: {}/d' | $K apply -f - >/dev/null
mk userns 'hostUsers: false' 'persistentVolumeClaim: {claimName: db}' | $K apply -f - >/dev/null
for p in host userns; do
  for i in $(seq 1 40); do s=$($K -n mir get pod $p -o jsonpath='{.status.phase}'); [ "$s" = Running ] && $K -n mir logs $p 2>/dev/null | grep -q marker && break; [ "$s" = Failed ] && break; /bin/sleep 2; done
  echo "################ $p: phase=$s"; $K -n mir logs $p 2>&1 | cut -c1-175
  [ "$s" = Running ] || $K -n mir get events --field-selector involvedObject.name=$p -o jsonpath='{range .items[*]}{.reason}: {.message}{"\n"}{end}' | grep -i fail | cut -c1-400
done
echo "################ node view of the same processes"
docker exec lenny-userns-control-plane sh -c "ps -eo uid,gid,args | grep -E 'mirror-marker|sleep 600' | grep -v grep" | cut -c1-90
for p in host userns; do cid=$($K -n mir get pod $p -o jsonpath='{.status.containerStatuses[0].containerID}' | sed 's#.*//##'); [ -n "$cid" ] && echo "$p uid_map on node: $(docker exec lenny-userns-control-plane sh -c "pid=\$(crictl inspect --output go-template --template '{{.info.pid}}' $cid); cat /proc/\$pid/uid_map" | tr -s ' ')"; done
echo "################ on-disk owners, as the node sees them"
docker exec lenny-userns-control-plane sh -c 'for d in /var/local-path-provisioner/*mir_d*; do echo $d | sed "s#.*mir_##"; ls -ln $d/a 2>/dev/null | awk "NR>1{print \"    a/\",\$1,\$3,\$4,\$9}"; ls -ldn $d/a $d/b 2>/dev/null | awk "{print \"   \",\$1,\$3,\$4}"; done'
echo "################ user namespace with no added capabilities: can the sequence run at all"
mk nocaps 'hostUsers: false' 'emptyDir: {}' | sed -e 's/add: \["SETUID","SETGID","CHOWN"\]/add: []/' | $K apply -f - >/dev/null; /bin/sleep 6; $K -n mir get pod nocaps -o jsonpath='{.status.phase}{"\n"}'; $K -n mir logs nocaps 2>&1 | head -3 | cut -c1-130
