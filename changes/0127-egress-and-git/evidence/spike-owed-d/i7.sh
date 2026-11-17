#!/bin/bash
# i7.sh: POSIX default ACLs on NFS, with a kernel NFS server and client in containers. One attempt.
O=spike-owed-d; cd "$O"
exec > >(tee -a raw/i7-nfs.txt) 2>&1
echo "kernel: $(docker run --rm --name od-k alpine:3.22 uname -r)"; docker run --rm --name od-k --privileged alpine:3.22 sh -c 'grep nfs /proc/filesystems | tr -s "\t " " " | tr "\n" ";"; zcat /proc/config.gz | grep -E "CONFIG_(NFS_V3_ACL|NFSD_V3_ACL|NFSD_V2_ACL|NFS_ACL_SUPPORT|NFSD_V4|NFS_V4|NFS_V4_1|NFS_V4_2)[= ]" | tr "\n" ";"; echo'
docker build -q -t od-nfs -f i7/Dockerfile i7 | tail -1
docker network create --subnet 198.19.70.0/24 od-nfsn >/dev/null; docker volume create od-nfsdata >/dev/null
docker run -d --name od-nfssrv --privileged --network od-nfsn --ip 198.19.70.10 -v od-nfsdata:/export -v $O/i7:/i7:ro od-nfs bash /i7/server.sh >/dev/null
for i in $(seq 1 30); do docker logs od-nfssrv 2>&1 | grep -q READY && break; /bin/sleep 1; done; docker logs od-nfssrv 2>&1 | cut -c1-160
for s in "4.2 /" "4.1 /" "3 /export"; do set -- $s; echo; echo "=========== client vers=$1"; docker run --rm --name od-nfscl --privileged --network od-nfsn -v $O/i7:/i7:ro od-nfs bash /i7/client.sh $1 $2 2>&1 | cut -c1-420; done
echo; echo "=========== server side: what the export holds"; docker exec od-nfssrv sh -c 'for d in /export/s-v*; do echo $d; getfacl -n $d 2>&1 | grep -v "^$" | tr "\n" " "; echo; [ -e $d/g ] && { getfacl -n $d/g 2>&1 | grep -v "^$" | tr "\n" " "; echo; }; done'
echo; echo "=========== an ACL set on the server, through a mount: v3 and v4.2"; docker exec od-nfssrv sh -c 'mkdir -p /export/srv; chown 60000:62001 /export/srv; chmod 2770 /export/srv; setfacl -m g:63001:rx,d:g:63001:rx,d:g::rwx,d:u::rwx,d:m:rwx,d:o::- /export/srv; getfacl -n /export/srv | grep -v "^$" | tr "\n" " "; echo'
for s in "4.2 /srv" "3 /export/srv"; do set -- $s; docker run --rm --name od-nfscl --privileged --network od-nfsn od-nfs bash -c "mkdir -p /m; mount -t nfs -o vers=$1,nolock,soft 198.19.70.10:$2 /m; echo vers=$1 mount rc=\$?; getfacl -n /m 2>&1 | grep -v '^$' | tr '\n' ' '; echo; nfs4_getfacl /m 2>&1 | grep -v '^#' | grep -v '^$' | tr '\n' ' ' | cut -c1-300; echo; setpriv --reuid=61002 --regid=62001 --clear-groups sh -c 'umask 007; echo x > /m/f$1; mkdir /m/d$1; echo y > /m/d$1/f'; setpriv --reuid=64001 --regid=63001 --clear-groups sh -c 'ls /m >/dev/null; echo reader-list rc=\$?; cat /m/f$1 >/dev/null; echo reader-read rc=\$?; cat /m/d$1/f > /dev/null; echo reader-read-sub rc=\$?; echo z > /m/evil$1; echo reader-create rc=\$?'" 2>&1 | cut -c1-320; done
docker exec od-nfssrv sh -c 'for f in /export/srv/*; do echo "$f: $(getfacl -n $f 2>/dev/null | grep -v "^$" | grep -v "^#" | tr "\n" " ")"; done'
docker exec od-nfssrv sh -c 'exportfs -ua; rpc.nfsd 0; pkill rpc.mountd; pkill rpcbind; umount /proc/fs/nfsd; true'; docker rm -f od-nfssrv >/dev/null; docker network rm od-nfsn >/dev/null; docker volume rm od-nfsdata >/dev/null; echo done > raw/i7-done
