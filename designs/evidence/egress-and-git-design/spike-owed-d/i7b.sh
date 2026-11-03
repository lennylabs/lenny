#!/bin/bash
O=spike-owed-d; cd "$O"
exec > >(tee -a raw/i7-nfs.txt) 2>&1
echo; echo "##### second run: the non-root parent's chown and the helper, $(date)"
docker network create --subnet 198.19.70.0/24 od-nfsn >/dev/null; docker volume create od-nfsdata >/dev/null
docker run -d --name od-nfssrv --privileged --network od-nfsn --ip 198.19.70.10 -v od-nfsdata:/export -v $O/i7:/i7:ro od-nfs bash /i7/server.sh >/dev/null
for i in $(seq 1 30); do docker logs od-nfssrv 2>&1 | grep -q READY && break; /bin/sleep 1; done
for s in "4.2 /" "3 /export"; do set -- $s; echo "=========== vers=$1, export no_root_squash"; docker run --rm --name od-nfscl --privileged --network od-nfsn -v $O/bin:/b:ro -v $O/i7:/i7:ro od-nfs bash /i7/client2.sh $1 $2 2>&1 | cut -c1-420; done
docker exec od-nfssrv sh -c 'exportfs -ua; rpc.nfsd 0; pkill rpc.mountd; pkill rpcbind; umount /proc/fs/nfsd; true'; docker rm -f od-nfssrv >/dev/null; docker network rm od-nfsn >/dev/null; docker volume rm od-nfsdata >/dev/null
