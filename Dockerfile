#
# To use it, build your image:
# docker build -t bleemeo/bleemeo-agent .
# docker run --name="bleemeo-agent" --net=host --pid=host -v /tmp/telegraf:/etc/telegraf/telegraf.d/bleemeo-generated.conf -v /var/lib/bleemeo:/var/lib/bleemeo -v /var/run/docker.sock:/var/run/docker.sock bleemeo/bleemeo-agent
#

FROM ubuntu:16.04

MAINTAINER Lionel Porcheron <lionel.porcheron@bleemeo.com>

RUN echo "deb http://packages.bleemeo.com/bleemeo-agent xenial main" >> /etc/apt/sources.list.d/bleemeo-agent.list && \
    apt-key adv --recv-keys --keyserver keyserver.ubuntu.com 9B8BDA4BE10E9F2328D40077E848FD17FC23F27E && \
    apt-get -y update && apt-get -y dist-upgrade && \
    apt-get install -y bleemeo-agent-single bleemeo-agent && \
    mkdir -p /etc/telegraf/telegraf.d/ && touch /etc/telegraf/telegraf.d/bleemeo-generated.conf && \
    chown bleemeo /etc/telegraf/telegraf.d/bleemeo-generated.conf

ADD 60-bleemeo.conf /etc/bleemeo/agent.conf.d/

#USER bleemeo
CMD ["/usr/bin/bleemeo-agent", "--yes-run-as-root"]
