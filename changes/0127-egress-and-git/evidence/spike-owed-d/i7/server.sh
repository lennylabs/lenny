#!/bin/bash
mkdir -p /export/s /run/rpcbind /var/lib/nfs/rpc_pipefs /var/lib/nfs/v4recovery; chmod 755 /export
mount -t nfsd nfsd /proc/fs/nfsd 2>&1; echo "mount nfsd rc=$?"
mount -t rpc_pipefs sunrpc /var/lib/nfs/rpc_pipefs 2>&1; echo "rpc_pipefs rc=$?"
rpcbind -w; echo "rpcbind rc=$?"
echo "/export *(rw,fsid=0,sync,no_subtree_check,no_root_squash,insecure,acl)" > /etc/exports
exportfs -ra; echo "exportfs rc=$?"
rpc.nfsd -d 8; echo "rpc.nfsd rc=$?"
rpc.mountd --no-udp; echo "rpc.mountd rc=$?"
cat /proc/fs/nfsd/versions; grep ' /export ' /proc/mounts
rpcinfo -p 2>&1 | head -12; exportfs -v
echo READY; exec sleep 864000
