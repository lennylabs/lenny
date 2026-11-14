#!/bin/bash
# t2.sh PHASE [args]: project quotas on the kubelet's XFS (prjquota) emptyDir
PH=$1; shift
R=/data/results/t2-$PH
mkdir -p $R
exec > >(tee -a $R/run.log) 2>&1
echo "=== t2 $PH $* $(date -u +%FT%TZ)"
MiB=1048576

qagent() { # privileged node agent, DaemonSet-style
  kubectl -n aqpriv get pod qagent >/dev/null 2>&1 && return
  cat <<Y | kubectl apply -f - >/dev/null
apiVersion: v1
kind: Pod
metadata: {name: qagent, namespace: aqpriv}
spec:
  terminationGracePeriodSeconds: 1
  containers:
  - name: a
    image: docker.io/library/aqspike:1
    imagePullPolicy: Never
    command: ["aq","sleep"]
    securityContext: {privileged: true}
    volumeMounts:
    - {name: kl, mountPath: /var/lib/kubelet, mountPropagation: HostToContainer}
  volumes:
  - {name: kl, hostPath: {path: /var/lib/kubelet}}
---
apiVersion: v1
kind: Pod
metadata: {name: qagent-capsys, namespace: aqpriv}
spec:
  terminationGracePeriodSeconds: 1
  containers:
  - name: a
    image: docker.io/library/aqspike:1
    imagePullPolicy: Never
    command: ["aq","sleep"]
    securityContext: {runAsUser: 0, capabilities: {drop: [ALL], add: [SYS_ADMIN]}, seccompProfile: {type: RuntimeDefault}}
    volumeMounts:
    - {name: kl, mountPath: /var/lib/kubelet, mountPropagation: HostToContainer}
  volumes:
  - {name: kl, hostPath: {path: /var/lib/kubelet}}
Y
  kubectl -n aqpriv wait --for=condition=Ready pod/qagent pod/qagent-capsys --timeout=120s >/dev/null
}
qa() { kubectl -n aqpriv exec qagent -- "$@"; }

qpod() { # name runtimeclass hostUsers gvisor-pod-share(yes|no) sizeLimit
  local N=$1 RC=$2 HU=$3 PS=$4 SL=${5:-1Gi} RCL="" ANN=""
  [ "$RC" != runc ] && RCL="runtimeClassName: $RC"
  [ "$PS" = yes ] && ANN='annotations: {dev.gvisor.spec.mount.ws.share: pod, dev.gvisor.spec.mount.ws.type: bind, dev.gvisor.spec.mount.ws.options: rw, dev.gvisor.spec.mount.ws.source: ws}'
  cat <<Y > $R/$N.yaml
apiVersion: v1
kind: Pod
metadata:
  name: $N
  namespace: aq
  $ANN
spec:
  $RCL
  hostUsers: $HU
  terminationGracePeriodSeconds: 1
  restartPolicy: Never
  containers:
  - name: slot
    image: docker.io/library/aqspike:1
    imagePullPolicy: Never
    command: ["aq","sleep"]
    securityContext: {runAsUser: 20000, runAsGroup: 20000, runAsNonRoot: true, allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}, seccompProfile: {type: RuntimeDefault}}
    resources: {requests: {cpu: 100m, memory: 64Mi, ephemeral-storage: 3Gi}, limits: {memory: 1536Mi}}
    volumeMounts: [{name: ws, mountPath: /workspace}, {name: other, mountPath: /other}]
  - name: adapter
    image: docker.io/library/aqspike:1
    imagePullPolicy: Never
    command: ["aq","sleep"]
    securityContext: {runAsUser: 10000, runAsGroup: 10000, runAsNonRoot: true, allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}, seccompProfile: {type: RuntimeDefault}}
    resources: {requests: {cpu: 50m, memory: 32Mi}, limits: {memory: 256Mi}}
    volumeMounts: [{name: ws, mountPath: /adapter/ws}]
  volumes:
  - {name: ws, emptyDir: {sizeLimit: $SL}}
  - {name: other, emptyDir: {sizeLimit: $SL}}
Y
  kubectl apply -f $R/$N.yaml >/dev/null
  kubectl -n aq wait --for=condition=Ready pod/$N --timeout=300s >/dev/null || { kubectl -n aq describe pod $N | tail -15; return 1; }
  PUID=$(kubectl -n aq get pod $N -o jsonpath='{.metadata.uid}')
  HP=/var/lib/kubelet/pods/$PUID/volumes/kubernetes.io~empty-dir/ws
  HO=/var/lib/kubelet/pods/$PUID/volumes/kubernetes.io~empty-dir/other
  echo "pod $N uid $PUID rc=$RC hostUsers=$HU podshare=$PS"
  echo "host ws: $(stat -c '%U:%G %a' $HP) projid: $(xfs_io -c 'lsproj' $HP 2>&1) flags: $(xfs_io -c 'lsattr' $HP 2>&1 | cut -c1-30)"
  ls -la $HP | head -5
}
sx() { kubectl -n aq exec $N -c slot -- "$@"; }
ax() { kubectl -n aq exec $N -c adapter -- "$@"; }
hrep() { echo "host report proj $1: $(xfs_quota -x -c "quota -p -N -b $1" /data 2>&1 | tr -s ' ') | du ws: $(du -sb $HP 2>/dev/null | cut -f1) B"; }
evwait() { # wait up to $1 s for the pod to leave Running
  local t0=$(date +%s)
  for i in $(seq 1 $(($1/2))); do
    ph=$(kubectl -n aq get pod $N -o jsonpath='{.status.phase}/{.status.reason}' 2>/dev/null)
    case "$ph" in Running/*) sleep 2;; *) echo "pod phase $ph after $(( $(date +%s)-t0 )) s"; kubectl -n aq get events --field-selector involvedObject.name=$N -o custom-columns=R:.reason,M:.message --no-headers | tail -3; return;; esac
  done
  echo "pod still $ph after $1 s (not evicted)"
}

full_runc() { # hostUsers
  local HU=$1; N=q-runc-$HU
  qpod $N runc $HU no 1Gi || return
  echo "--- (a) detection before assignment, unprivileged slot"; sx aq probe /workspace /other
  echo "--- (a) adapter container"; ax aq probe /adapter/ws
  echo "--- (a) privileged agent"; qa aq probe $HP
  echo "--- (a) CAP_SYS_ADMIN-only agent under RuntimeDefault seccomp"; kubectl -n aqpriv exec qagent-capsys -- aq probe $HP
  local ID=$((50000 + RANDOM % 1000))
  echo "--- (b) assign project $ID, hard 256 MiB, from the privileged agent"; qa aq qset -path $HP -id $ID -hard $((256*MiB))
  echo "--- (b) CAP_SYS_ADMIN-only agent: set limit on the same project"; kubectl -n aqpriv exec qagent-capsys -- aq qset -path $HP -limit-only -hard $((256*MiB))
  hrep $ID; grep -c . /etc/projects 2>/dev/null
  echo "--- (c) statfs etc. after assignment"; sx aq probe /workspace; sx mkdir -p /workspace/sub; sx aq probe /workspace/sub; sx sh -c 'echo x > /workspace/sub/f'; sx aq probe /workspace/sub/f; ax aq probe /adapter/ws
  echo "--- write 40 MiB"; sx aq fill -path /workspace -mb 40 -name f40; sx aq probe /workspace; hrep $ID
  echo "--- (d) 50 MiB under a dir then chmod 000"; sx aq hide -path /workspace -mb 50; sx aq probe /workspace; hrep $ID; echo "host projid of hidden: $(xfs_io -c lsproj $HP/hidden 2>&1)"
  echo "--- escape attempts by the slot"; sx aq escape -path /workspace -mb 32; hrep $ID; for d in $HP/esc-*; do echo "host $d: $(xfs_io -c lsproj $d 2>&1); file: $(xfs_io -c lsproj $d/f 2>&1)"; done
  echo "--- (e) fill to the limit"; sx aq fill -path /workspace -mb 400 -name big; sx aq probe /workspace; hrep $ID
  echo "--- (e) fsync-each fill on a full project"; sx aq fill -path /workspace -mb 10 -fsync-each -name big2
  evwait 40
  echo "--- (e) limit 2 GiB above the 1 GiB sizeLimit, fill 1100 MiB: kubelet eviction?"
  sx rm -f /workspace/big /workspace/big2; qa aq qset -path $HP -limit-only -hard $((2048*MiB)) >/dev/null; hrep $ID
  sx aq fill -path /workspace -mb 1100 -name over; hrep $ID
  evwait 120
  kubectl -n aq delete pod $N --wait=true --timeout=60s >/dev/null 2>&1
}

sandboxed() { # label runtimeclass podshare
  local L=$1 RC=$2 PS=$3; N=q-$L
  qpod $N $RC true $PS 1Gi || return
  echo "--- mounts in the slot"; sx sh -c 'grep -E " /workspace | /other " /proc/self/mountinfo'
  echo "--- (a) before"; sx aq probe /workspace /other; ax aq probe /adapter/ws
  local ID=$((51000 + RANDOM % 1000))
  echo "--- (b) assign $ID 256 MiB"; qa aq qset -path $HP -id $ID -hard $((256*MiB)); hrep $ID
  echo "--- (c) after"; sx aq probe /workspace; sx mkdir -p /workspace/sub; sx aq probe /workspace/sub; ax aq probe /adapter/ws
  echo "--- write 40 MiB"; sx aq fill -path /workspace -mb 40 -name f40; sleep 2; sx aq probe /workspace; hrep $ID
  echo "--- (d) hidden"; sx aq hide -path /workspace -mb 50; sleep 2; sx aq probe /workspace; hrep $ID
  echo "--- escape"; sx aq escape -path /workspace -mb 16; hrep $ID
  echo "--- (e) fill, no fsync per chunk"; sx aq fill -path /workspace -mb 400 -name big; sleep 2; sx aq probe /workspace; hrep $ID; ls -la $HP | head
  echo "--- (e) fill with fsync each chunk"; sx rm -f /workspace/big; sleep 2; hrep $ID; sx aq fill -path /workspace -mb 400 -fsync-each -name big2; hrep $ID
  echo "--- (e) read back the end of the file"; sx sh -c 'ls -la /workspace; tail -c 1048576 /workspace/big2 | od -An -tx1 | head -2'
  evwait 30
  kubectl -n aq delete pod $N --wait=true --timeout=120s >/dev/null 2>&1
}

case $PH in
host)
  findmnt -no SOURCE,FSTYPE,OPTIONS /data; findmnt -no SOURCE,FSTYPE,OPTIONS /var/lib/kubelet
  xfs_quota -x -c 'state -p' /data; ls -la /etc/projects /etc/projid 2>&1
  aq probe /data /var/lib/kubelet
  ;;
runc) qagent; full_runc true ;;
runcns) qagent; full_runc false ;;
gvisor) qagent; sandboxed gvisor gvisor no ;;
gvisorps) qagent; sandboxed gvisorps gvisor yes ;;
kata) qagent; sandboxed kata kata no ;;
katablock) qagent; N=q-katablock; qpod $N kata-block true no 1Gi || exit 1
  ls -la $HP; find $HP -maxdepth 2 | head; sx sh -c 'grep -E " /workspace " /proc/self/mountinfo'; sx aq probe /workspace
  sx aq fill -path /workspace -mb 64 -name f64; ls -las $HP | head; du -sb $HP
  ID=$((52000 + RANDOM % 1000)); qa aq qset -path $HP -id $ID -hard $((128*MiB)); hrep $ID
  echo "the image file existed before the assignment; new blocks of an existing file keep its old projid:"; for f in $HP/*; do echo "$f $(xfs_io -c lsproj $f 2>&1)"; done
  sx aq fill -path /workspace -mb 300 -name f300; sx sh -c 'dmesg 2>&1 | tail -5; grep " /workspace " /proc/mounts'; hrep $ID
  evwait 20; kubectl -n aq delete pod $N --wait=true --timeout=120s >/dev/null 2>&1 ;;
gate) # kubelet feature gate on: what does the kubelet do itself
  qagent
  for HU in true false; do
    N=q-gate-$HU; qpod $N runc $HU no 1Gi || continue
    echo "kubelet-assigned: ws $(xfs_io -c lsproj $HP 2>&1) $(xfs_io -c lsattr $HP 2>&1 | cut -c1-25) | other $(xfs_io -c lsproj $HO 2>&1)"
    sx aq probe /workspace
    KID=$(xfs_io -c lsproj $HP 2>/dev/null | grep -o '[0-9]*' | tail -1)
    [ -n "$KID" ] && [ "$KID" != 0 ] && hrep $KID
    sx aq fill -path /workspace -mb 100 -name f100; sleep 15
    echo "kubelet stats summary for ws: $(kubectl get --raw /api/v1/nodes/$(hostname)/proxy/stats/summary | jq -c ".pods[]|select(.podRef.name==\"$N\")|.volume[]|select(.name==\"ws\" or .name==\"other\")|{name,usedBytes,inodesUsed}")"
    [ -n "$KID" ] && [ "$KID" != 0 ] && { hrep $KID; echo "Lenny sets 256 MiB on the kubelet's project $KID"; qa aq qset -path $HP -limit-only -hard $((256*MiB)); sx aq fill -path /workspace -mb 400 -name big; hrep $KID; sleep 30; echo "after 30 s the kubelet left the limit as:"; hrep $KID; sx aq probe /workspace; }
    echo "Lenny assigns its own project over the kubelet's (other volume)"
    LID=$((53000 + RANDOM % 1000)); qa aq qset -path $HO -id $LID -hard $((256*MiB)); sx aq fill -path /other -mb 120 -name f120; sleep 15
    echo "kubelet stats after re-assignment: $(kubectl get --raw /api/v1/nodes/$(hostname)/proxy/stats/summary | jq -c ".pods[]|select(.podRef.name==\"$N\")|.volume[]|select(.name==\"other\")|{name,usedBytes}")"; hrep $LID
    echo "/etc/projects:"; cat /etc/projects 2>&1 | tail -4; echo "/etc/projid:"; cat /etc/projid 2>&1 | tail -4
    kubectl -n aq delete pod $N --wait=true --timeout=60s >/dev/null 2>&1
    echo "after delete: /etc/projects $(wc -l < /etc/projects 2>/dev/null)"; [ -n "$KID" ] && hrep $KID
  done ;;
gateevict) # gate on: does the kubelet's eviction count from the quota, and evict at sizeLimit
  N=q-gev; qpod $N runc false no 512Mi || exit 1
  sx aq fill -path /workspace -mb 700 -name over; evwait 120 ;;
ext4)
  mkdir -p /ext4q; [ -f /data/ext4.img ] || { truncate -s 4G /data/ext4.img; mkfs.ext4 -q -O quota,project -E quotatype=usrquota:grpquota:prjquota /data/ext4.img; }
  mountpoint -q /ext4q || mount -o loop,prjquota /data/ext4.img /ext4q
  findmnt -no SOURCE,FSTYPE,OPTIONS /ext4q; mkdir -p /ext4q/ws; chmod 1777 /ext4q/ws
  HP=/ext4q/ws; ID=54001; aq qset -path $HP -id $ID -hard $((256*MiB))
  N=q-ext4
  cat <<Y | kubectl apply -f - >/dev/null
apiVersion: v1
kind: Pod
metadata: {name: $N, namespace: aq}
spec:
  terminationGracePeriodSeconds: 1
  containers:
  - name: slot
    image: docker.io/library/aqspike:1
    imagePullPolicy: Never
    command: ["aq","sleep"]
    securityContext: {runAsUser: 20000, runAsGroup: 20000, runAsNonRoot: true, allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}, seccompProfile: {type: RuntimeDefault}}
    volumeMounts: [{name: ws, mountPath: /workspace}]
  volumes:
  - {name: ws, hostPath: {path: /ext4q/ws}}
Y
  kubectl -n aq wait --for=condition=Ready pod/$N --timeout=120s >/dev/null
  sx aq probe /workspace; sx mkdir -p /workspace/sub; sx aq probe /workspace/sub
  sx aq fill -path /workspace -mb 40 -name f40; sx aq probe /workspace
  sx aq hide -path /workspace -mb 50; sx aq probe /workspace; repquota -P /ext4q 2>&1 | grep -E "#$ID|^#54001" ; aq probe $HP | jq -c .xgetquota
  sx aq escape -path /workspace -mb 32; aq probe $HP | jq -c .xgetquota; for d in $HP/esc-*; do echo "$d $(lsattr -pd $d 2>&1)"; done
  sx aq fill -path /workspace -mb 400 -name big; aq probe $HP | jq -c .xgetquota
  kubectl -n aq delete pod $N --wait=true --timeout=60s >/dev/null 2>&1 ;;
esac
echo "=== t2 $PH done $(date -u +%FT%TZ)"
