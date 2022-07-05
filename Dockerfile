#
# To use it, build your image:
# docker build -t glouton .
# docker run --name="glouton" --net=host --pid=host -v /var/lib/glouton:/var/lib/glouton -v /var/run/docker.sock:/var/run/docker.sock -v /:/hostroot:ro glouton
#

FROM --platform=$BUILDPLATFORM busybox:1.34 as build

ARG TARGETARCH

COPY dist/glouton_linux_amd64_v1/glouton /glouton.amd64
COPY dist/glouton_linux_arm64/glouton /glouton.arm64
COPY dist/glouton_linux_arm_6/glouton /glouton.arm

RUN cp -p /glouton.$TARGETARCH /glouton

# We use busybox because we need nsenter and ip commands.
FROM busybox:1.34

LABEL maintainer="Bleemeo Docker Maintainers <packaging-team@bleemeo.com>"

COPY etc/glouton.conf /etc/glouton/glouton.conf
COPY packaging/kubernetes/glouton-k8s-default.conf /etc/glouton/glouton-k8s-default.conf
COPY packaging/common/glouton-05-system.conf /etc/glouton/conf.d/05-system.conf
COPY packaging/docker/60-glouton.conf /etc/glouton/conf.d/
COPY bin/glouton-veths /usr/lib/glouton/glouton-veths
COPY --from=build /glouton /usr/sbin/glouton

CMD ["/usr/sbin/glouton", "--yes-run-as-root"]
