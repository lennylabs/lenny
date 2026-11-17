#!/bin/bash
# The mirror's non-root parent with CAP_CHOWN on NFS, and the helper. V=$1 EXPORT=$2
V=$1; M=/mnt/n; mkdir -p $M; mount -t nfs -o vers=$V,nolock,soft,timeo=30,retrans=2 198.19.70.10:$2 $M; echo "mount vers=$V rc=$?"
d=$M/p-v$V; rm -rf $d; mkdir -p $d; chmod 2770 $d; chown 60000:62001 $d
echo "--- the fetch writes under UID 61002, group 62001"
setpriv --reuid=61002 --regid=62001 --clear-groups sh -c "umask 007; echo x > $d/pack; mkdir $d/sub; echo y > $d/sub/f; ls -ln $d | grep -v total"
echo "--- the parent, non-root at UID 65532 with ambient CAP_CHOWN and the store group: the re-chown walk"
setpriv --reuid=65532 --regid=62001 --groups=62001 --inh-caps=+chown --ambient-caps=+chown sh -c "grep -E 'CapEff|CapAmb' /proc/self/status | tr -s '\t ' ' ' | tr '\n' ' '; echo; /b/mhelper rechown $d 60000"
ls -ln $d | grep -v total
echo "--- the same walk on a local ext4 volume, as the control"
mkdir -p /tmp/l; chmod 2770 /tmp/l; chown 60000:62001 /tmp/l; setpriv --reuid=61002 --regid=62001 --clear-groups sh -c "umask 007; echo x > /tmp/l/pack"; setpriv --reuid=65532 --regid=62001 --groups=62001 --inh-caps=+chown --ambient-caps=+chown /b/mhelper rechown /tmp/l 60000
echo "--- the helper under the fetch UID: with the reader ACL, then without"
setpriv --reuid=61002 --regid=62001 --clear-groups sh -c "echo x > $d/h; chmod 4777 $d/h; mkfifo $d/ff; ln -s /etc/passwd $d/ln"
setpriv --reuid=61002 --regid=62001 --clear-groups /b/mhelper normalize $d -dir 2770 -file 0660 -reader 63001 | cut -c1-420
setpriv --reuid=61002 --regid=62001 --clear-groups sh -c "echo x > $d/h2; chmod 4777 $d/h2; mkfifo $d/ff2"
setpriv --reuid=61002 --regid=62001 --clear-groups /b/mhelper normalize $d -dir 2770 -file 0660 | cut -c1-420; ls -ln $d | grep -v total
