#!/bin/bash
R=spike-owed-b; cd $R; . ./kc.sh pki
OUT=out/s5; mkdir -p $OUT; NODE=ob-pki-control-plane; M="$K -n mirror"
/opt/homebrew/bin/kind load docker-image ob-mirror:3 --name ob-pki 2>&1 | tail -1 | cut -c1-80
docker exec $NODE chmod a+rx /kind /kind/bin
$K create ns mirror --dry-run=client -o yaml | $K apply -f - >/dev/null
$M create secret generic s --from-literal=key=topsecret --dry-run=client -o yaml | $K apply -f - >/dev/null
$K get storageclass
pvc(){ cat <<EOF | $K apply -f - >/dev/null
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: $1, namespace: mirror}
spec: {accessModes: [ReadWriteOnce], storageClassName: standard, resources: {requests: {storage: 1Gi}}}
EOF
}
pvc vtrue; pvc vfalse
run(){ # run NAME HOSTUSERS CMD ARGS VOLUME
  sed -e "s/NAME/$1/;s/HOSTUSERS/$2/;s/\"CMD\"ARGS/\"$3\"$4/;s|VOLUME|$5|;s|\\\\/|/|g" k8s/mirror.tmpl | $K apply -f - >/dev/null
  for i in $(seq 1 120); do ph=$($M get pod $1 -o jsonpath='{.status.phase}'); [ "$ph" = Succeeded ] || [ "$ph" = Failed ] && break; sleep 1; done
  echo "$1: phase=$ph"; $M logs $1 > $OUT/$1.log 2>&1; $M get pod $1 -o jsonpath='{.status.containerStatuses[0].state}' | cut -c1-200; echo
}
$M delete pod --all --wait=true >/dev/null; $M delete pvc --all --wait=true >/dev/null
pvc unset; pvc userns
run m-unset "hostUsers: true" scenario "" "persistentVolumeClaim: {claimName: unset}"
run m-userns "hostUsers: false" scenario "" "persistentVolumeClaim: {claimName: userns}"
run m-stripped "hostUsers: true" stripped "" "emptyDir: {}"
pv=$($M get pvc unset -o jsonpath='{.spec.volumeName}'); $K get pv $pv -o jsonpath='{.spec.hostPath.path}{"\n"}'
docker exec $NODE sh -c "findmnt -T /var/local-path-provisioner -o TARGET,FSTYPE,OPTIONS -n; ls -ln /var/local-path-provisioner | head -5"

echo "### volume types: ACL behaviour"
docker exec $NODE sh -c 'mkdir -p /var/ob-noacl && [ -f /var/ob-noacl.img ] || { dd if=/dev/zero of=/var/ob-noacl.img bs=1M count=64 2>/dev/null && mkfs.ext4 -q -F /var/ob-noacl.img; }; mountpoint -q /var/ob-noacl || mount -o loop,noacl /var/ob-noacl.img /var/ob-noacl; chmod 0777 /var/ob-noacl; grep ob-noacl /proc/mounts'
for hu in true false; do
 run v-emptydir-$hu "hostUsers: $hu" aclprobe ',"\/data"' "emptyDir: {}"
 run v-tmpfs-$hu "hostUsers: $hu" aclprobe ',"\/data"' "emptyDir: {medium: Memory}"
 run v-noacl-$hu "hostUsers: $hu" aclprobe ',"\/data"' "hostPath: {path: \/var\/ob-noacl, type: Directory}"
 run v-localpath-$hu "hostUsers: $hu" aclprobe ',"\/data"' "persistentVolumeClaim: {claimName: v$hu}"
done
for f in $OUT/v-*.log; do echo "--- $f"; cut -c1-180 $f; done
docker exec $NODE sh -c 'umount /var/ob-noacl; rm -f /var/ob-noacl.img; rmdir /var/ob-noacl'
