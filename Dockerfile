#
# To use it, build your image:
# docker build -t glouton .
# docker run --name="glouton" --net=host --pid=host -v /var/lib/glouton:/var/lib/glouton -v /var/run/docker.sock:/var/run/docker.sock -v /:/hostroot:ro glouton
#

FROM --platform=$BUILDPLATFORM busybox as build

ARG TARGETARCH

ADD dist/glouton_linux_amd64_v1/glouton /glouton.amd64
ADD dist/glouton_linux_arm64/glouton /glouton.arm64
ADD dist/glouton_linux_arm_6/glouton /glouton.arm6

RUN if [ "$TARGETARCH" = "arm" ]; then cp -p /glouton.arm6 /glouton; else cp -p /glouton.$TARGETARCH /glouton; fi

FROM gcr.io/distroless/base

LABEL MAINTAINER="Bleemeo Docker Maintainers <packaging-team@bleemeo.com>"

ADD etc/glouton.conf /etc/glouton/glouton.conf
ADD packaging/kubernetes/glouton-k8s-default.conf /etc/glouton/glouton-k8s-default.conf
ADD packaging/common/glouton-05-system.conf /etc/glouton/conf.d/05-system.conf
ADD packaging/docker/60-glouton.conf /etc/glouton/conf.d/
COPY --from=build /glouton /usr/sbin/glouton

CMD ["/usr/sbin/glouton", "--yes-run-as-root"]
