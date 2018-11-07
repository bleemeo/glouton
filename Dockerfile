#
# To use it, build your image:
# docker build -t bleemeo/bleemeo-agent .
# docker run --name="bleemeo-agent" --net=host --pid=host -v /tmp/telegraf:/etc/telegraf/telegraf.d/bleemeo-generated.conf -v /var/lib/bleemeo:/var/lib/bleemeo -v /var/run/docker.sock:/var/run/docker.sock bleemeo/bleemeo-agent
#

FROM ubuntu:18.04

LABEL MAINTAINER="Bleemeo Docker Maintainers <packaging-team@bleemeo.com>"

ENV DEBIAN_FRONTEND noninteractive

RUN apt-get -y update && apt-get -y dist-upgrade && apt-get install --no-install-recommends -y wget ca-certificates && \
    mkdir /etc/bleemeo && \
    wget -qO- https://packages.bleemeo.com/bleemeo-agent/ubuntu/bleemeo-keyring.asc | awk '/^$/{ x = 1; } /^[^=-]/{ if (x) { print $0; } ; }' | base64 -d > /etc/bleemeo/bleemeo-keyring.gpg && \
    echo "deb [signed-by=/etc/bleemeo/bleemeo-keyring.gpg] http://packages.bleemeo.com/bleemeo-agent bionic main" >> /etc/apt/sources.list.d/bleemeo-agent.list && \
    ls -lh /etc/bleemeo && \
    apt-get update && \
    apt-get install -y bleemeo-agent-single bleemeo-agent && \
    mkdir -p /etc/telegraf/telegraf.d/ && touch /etc/telegraf/telegraf.d/bleemeo-generated.conf && \
    chown bleemeo /etc/telegraf/telegraf.d/bleemeo-generated.conf && \
    rm -fr /var/lib/apt/lists/*

ADD 60-bleemeo.conf /etc/bleemeo/agent.conf.d/

#USER bleemeo
CMD ["/usr/bin/bleemeo-agent", "--yes-run-as-root"]
