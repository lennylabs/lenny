FROM debian:trixie-slim
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends nfs-kernel-server nfs-common rpcbind acl attr nfs4-acl-tools procps util-linux git >/dev/null
