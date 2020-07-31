FROM ubuntu:20.04

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get -q update && apt-get -qy upgrade && apt-get -qy install nsis

VOLUME /work
WORKDIR /work
