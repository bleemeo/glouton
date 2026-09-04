FROM ubuntu:24.04

# Ubuntu 24.04 is the platform PRODUCT-3181 was reported on. What matters here is
# systemd running as PID 1 (so /run/systemd/system exists and the postinst reaches its
# enable/restart path) and init-system-helpers, which provides deb-systemd-helper.
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      systemd systemd-sysv dpkg-dev init-system-helpers \
 && rm -rf /var/lib/apt/lists/*

# Ubuntu's Docker images ship /usr/sbin/policy-rc.d with "exit 101" so that installing a
# package never starts its daemon during an image build. deb-systemd-invoke honours that
# policy, so leaving it in place would stop the postinst from ever starting Glouton and
# every "did it start?" assertion would fail for a reason that has nothing to do with the
# code under test. A real Ubuntu 24.04 host has no such file.
RUN rm -f /usr/sbin/policy-rc.d

COPY . /src/
RUN chmod +x /src/scenarios.sh

STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
