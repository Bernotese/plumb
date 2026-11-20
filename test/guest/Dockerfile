ARG DISTRO=debian:13@sha256:9c6e381421d0f48d1b8133e573f33c185ee16cbf505eda9c4a8a4791b25ea8a6
FROM ${DISTRO}
ENV container=docker
# Retry transient mirror failures during both image builds and guest package tests.
RUN printf 'Acquire::Retries "5";\n' > /etc/apt/apt.conf.d/80-retries
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends systemd-sysv dbus ca-certificates iproute2 nftables cloud-init openssh-server rsyslog procps gnupg \
 && mkdir -p /opt/plumb-test-debs/partial \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends --download-only -o Dir::Cache::archives=/opt/plumb-test-debs curl \
 && rm -rf /opt/plumb-test-debs/partial /opt/plumb-test-debs/lock /var/lib/apt/lists/*
# T-276: the curl packages for TestGuestInstallCommandFailsLoudly come with the
# image (fetched at build time, installed by dpkg -i), so the test makes no
# call to an outside mirror; the image itself still has no curl.
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
