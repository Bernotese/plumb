ARG DISTRO=debian:13@sha256:9c6e381421d0f48d1b8133e573f33c185ee16cbf505eda9c4a8a4791b25ea8a6
FROM ${DISTRO}
ENV container=docker
# Retry transient mirror failures during both image builds and guest package tests.
RUN printf 'Acquire::Retries "5";\n' > /etc/apt/apt.conf.d/80-retries
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends systemd-sysv dbus ca-certificates iproute2 nftables cloud-init openssh-server rsyslog procps gnupg && rm -rf /var/lib/apt/lists/*
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
