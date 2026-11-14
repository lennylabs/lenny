#!/bin/bash
# node-setup.sh BUCKET: Amazon Linux 2023 node for the attribution and project quota spike. containerd 2.4.2, runc 1.5.3,
# gVisor release-20261109.0 (systrap, directfs), Kata 4.3.1 on QEMU, single-node k3s v1.37.2+k3s1 on
# the external containerd, kubelet root on XFS with reflink=1 on the gp3 data volume.
B=$1
exec >> /var/log/aq-setup.log 2>&1
set -x
t0=$(date +%s)
dnf install -y -q xfsprogs git tar jq python3 zstd bzip2 sysstat iotop e2fsprogs >/dev/null
# the data volume: the nvme device that is not the root disk
ROOTDEV=$(findmnt -no SOURCE / | sed 's/p[0-9]*$//')
DEV=$(lsblk -dpno NAME,TYPE | awk '$2=="disk"{print $1}' | grep -v "^$ROOTDEV$" | head -1)
mkfs.xfs -f -m reflink=1 $DEV
mkdir -p /data && mount -o noatime,prjquota $DEV /data
findmnt -no OPTIONS /data
for d in kubelet containerd rancher; do mkdir -p /data/$d /var/lib/$d; mount --bind /data/$d /var/lib/$d; done
mkdir -p /data/ws /data/results /data/bin
xfs_info /data | grep -o 'reflink=[01]'
# containerd and runc
curl -sSfL https://github.com/containerd/containerd/releases/download/v2.4.2/containerd-2.4.2-linux-amd64.tar.gz | tar xz -C /usr/local
curl -sSfL -o /usr/local/sbin/runc https://github.com/opencontainers/runc/releases/download/v1.5.3/runc.amd64 && chmod 755 /usr/local/sbin/runc
curl -sSfL -o /etc/systemd/system/containerd.service https://raw.githubusercontent.com/containerd/containerd/v2.4.2/containerd.service
mkdir -p /etc/systemd/system/containerd.service.d
printf '[Service]\nLimitNOFILE=65535\n' > /etc/systemd/system/containerd.service.d/nofile.conf
# gVisor
cd /root
curl -sSfLO https://github.com/google/gvisor/releases/download/release-20261109.0/gvisor-x86_64.tar.bz2
curl -sSfLO https://github.com/google/gvisor/releases/download/release-20261109.0/SHA512SUMS
grep gvisor-x86_64.tar.bz2 SHA512SUMS | sha512sum -c - && mkdir -p gv && tar xjf gvisor-x86_64.tar.bz2 -C gv
cp gv/runsc gv/containerd-shim-runsc-v1 /usr/local/bin/ 2>/dev/null || cp gv/*/runsc gv/*/containerd-shim-runsc-v1 /usr/local/bin/
cp -r gv/gvisor-bin /usr/local/bin/ 2>/dev/null || cp -r gv/*/gvisor-bin /usr/local/bin/
ls -la /usr/local/bin/gvisor-bin; runsc --version
mkdir -p /etc/containerd && printf '[runsc_config]\n' > /etc/containerd/runsc.toml && printf '[runsc_config]\n  network = "host"\n' > /etc/containerd/runsc-host.toml
# Kata
curl -sSfL -o /root/kata.tar.zst https://github.com/kata-containers/kata-containers/releases/download/4.3.1/kata-static-4.3.1-amd64.tar.zst
tar --zstd -xf /root/kata.tar.zst -C / && rm -f /root/kata.tar.zst
ln -sf /opt/kata/bin/containerd-shim-kata-v2 /usr/local/bin/containerd-shim-kata-v2
ls /opt/kata/share/defaults/kata-containers/ /opt/kata/bin
KC=/opt/kata/share/defaults/kata-containers
cat > /etc/containerd/config.toml <<'T'
version = 3
[plugins.'io.containerd.cri.v1.runtime'.containerd]
  default_runtime_name = 'runc'
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
  runtime_type = 'io.containerd.runc.v2'
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runsc]
  runtime_type = 'io.containerd.runsc.v1'
  pod_annotations = ['dev.gvisor.*']
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runsc.options]
  TypeUrl = 'io.containerd.runsc.v1.options'
  ConfigPath = '/etc/containerd/runsc.toml'
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runsc-host]
  runtime_type = 'io.containerd.runsc.v1'
  pod_annotations = ['dev.gvisor.*']
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runsc-host.options]
  TypeUrl = 'io.containerd.runsc.v1.options'
  ConfigPath = '/etc/containerd/runsc-host.toml'
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.kata]
  runtime_type = 'io.containerd.kata.v2'
  runtime_path = '/opt/kata/bin/containerd-shim-kata-v2'
  privileged_without_host_devices = true
  pod_annotations = ['io.katacontainers.*']
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.kata.options]
  ConfigPath = '/opt/kata/share/defaults/kata-containers/configuration.toml'
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.kata-block]
  runtime_type = 'io.containerd.kata.v2'
  runtime_path = '/opt/kata/bin/containerd-shim-kata-v2'
  privileged_without_host_devices = true
  pod_annotations = ['io.katacontainers.*']
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.kata-block.options]
  ConfigPath = '/opt/kata/share/defaults/kata-containers/configuration-block.toml'
[plugins.'io.containerd.cri.v1.runtime'.cni]
  bin_dirs = ['/var/lib/rancher/k3s/data/cni']
  conf_dir = '/var/lib/rancher/k3s/agent/etc/cni/net.d'
T
systemctl daemon-reload && systemctl enable --now containerd && sleep 2 && systemctl is-active containerd
containerd --version; runc --version | head -1
# k3s on the external containerd
curl -sSfL -o /usr/local/bin/k3s https://github.com/k3s-io/k3s/releases/download/v1.37.2%2Bk3s1/k3s && chmod 755 /usr/local/bin/k3s
cat > /etc/systemd/system/k3s.service <<'U'
[Unit]
After=containerd.service network-online.target
[Service]
ExecStart=/usr/local/bin/k3s server --container-runtime-endpoint unix:///run/containerd/containerd.sock --disable traefik,servicelb,metrics-server,local-storage --write-kubeconfig-mode 644
Restart=always
LimitNOFILE=1048576
[Install]
WantedBy=multi-user.target
U
systemctl daemon-reload && systemctl enable --now k3s
for i in $(seq 1 60); do [ -e /var/lib/rancher/k3s/data/current/bin/cni ] && break; sleep 5; done
ln -sfn /var/lib/rancher/k3s/data/current/bin /var/lib/rancher/k3s/data/cni
ln -sf /usr/local/bin/k3s /usr/local/bin/kubectl
for i in $(seq 1 60); do kubectl get nodes 2>/dev/null | grep -q ' Ready' && break; sleep 5; done
kubectl get nodes -o wide
# the harness image
until aws s3 ls s3://$B/bin/ready >/dev/null 2>&1; do sleep 15; done
aws s3 cp s3://$B/bin/aq-img-amd64.tar /root/aq-img.tar && ctr -n k8s.io images import /root/aq-img.tar && rm -f /root/aq-img.tar
ctr -n k8s.io images ls -q | grep aqspike
aws s3 cp s3://$B/bin/aq-amd64 /usr/local/bin/aq && chmod 755 /usr/local/bin/aq
aws s3 sync s3://$B/scripts /data/scripts && chmod +x /data/scripts/*.sh
kubectl apply -f /data/scripts/ns.yaml
echo "setup done in $(( $(date +%s)-t0 ))s"
touch /var/tmp/aq-setup-done
