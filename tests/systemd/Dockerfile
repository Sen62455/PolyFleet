ARG BASE_IMAGE=ubuntu:24.04
FROM ${BASE_IMAGE}

ENV container=docker
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates coreutils curl dbus netcat-openbsd openssl passwd systemd systemd-sysv tar util-linux && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* && \
    systemctl mask \
      dev-hugepages.mount \
      sys-fs-fuse-connections.mount \
      systemd-remount-fs.service

STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
