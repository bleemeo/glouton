#
# To use it, build your image:
# docker build -t glouton .
# docker run --name="glouton" --net=host --pid=host -v /var/lib/glouton:/var/lib/glouton -v /var/run/docker.sock:/var/run/docker.sock -v /:/hostroot:ro glouton
#

FROM gcr.io/distroless/base

LABEL MAINTAINER="Bleemeo Docker Maintainers <packaging-team@bleemeo.com>"

ADD etc/glouton.conf /etc/glouton/glouton.conf
ADD packaging/kubernetes/glouton-k8s-default.conf /etc/glouton/glouton-k8s-default.conf
ADD packaging/common/glouton-05-system.conf /etc/glouton/conf.d/05-system.conf
ADD packaging/docker/60-glouton.conf /etc/glouton/conf.d/
COPY glouton /usr/sbin/glouton

CMD ["/usr/sbin/glouton", "--yes-run-as-root"]
