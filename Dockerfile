#
# To use it, build your image:
# docker build -t bleemeo/bleemeo-agent .
# docker run --name="bleemeo-agent" --net=host --pid=host -v /tmp/telegraf:/etc/telegraf/telegraf.d/bleemeo-generated.conf -v /var/lib/bleemeo:/var/lib/bleemeo -v /var/run/docker.sock:/var/run/docker.sock bleemeo/bleemeo-agent
#

FROM scratch

LABEL MAINTAINER="Bleemeo Docker Maintainers <packaging-team@bleemeo.com>"

ENV DEBIAN_FRONTEND noninteractive

ADD packaging/common/bleemeo-05-system.conf /etc/bleemeo/agent.conf.d/05-system.conf
ADD 60-bleemeo.conf /etc/bleemeo/agent.conf.d/
COPY agentgo-x86_64 /agentgo

#USER bleemeo
CMD ["/agentgo", "--yes-run-as-root"]
