#!/bin/bash
# kata-config.sh: the kata (virtio-fs, cache auto) and kata-block (emptydir_mode block-plain) handlers for runtime-rs
KC=/opt/kata/share/defaults/kata-containers/runtime-rs
SHIM=/opt/kata/runtime-rs/bin/containerd-shim-kata-v2
F=$KC/configuration-qemu-runtime-rs.toml
grep -q 'max_user_watches' $F || sed -i 's|^kernel_params = "\(.*\)"|kernel_params = "\1 sysctl.fs.inotify.max_user_watches=1048576"|' $F
cp $F $KC/configuration-qemu-block-runtime-rs.toml
sed -i 's|^emptydir_mode = "shared-fs"|emptydir_mode = "block-plain"|' $KC/configuration-qemu-block-runtime-rs.toml
sed -i "s|runtime_path = '/opt/kata/bin/containerd-shim-kata-v2'|runtime_path = '$SHIM'|; s|ConfigPath = '/opt/kata/share/defaults/kata-containers/configuration.toml'|ConfigPath = '$F'|; s|ConfigPath = '/opt/kata/share/defaults/kata-containers/configuration-block.toml'|ConfigPath = '$KC/configuration-qemu-block-runtime-rs.toml'|" /etc/containerd/config.toml
grep -n -A6 "runtimes.kata" /etc/containerd/config.toml | grep -E 'runtime_path|ConfigPath'
grep -n -E '^kernel_params|^emptydir_mode' $F $KC/configuration-qemu-block-runtime-rs.toml
systemctl restart containerd; sleep 3; systemctl is-active containerd
