#!/bin/sh

set -e

docker run --name=weaveplugin --privileged -d \
    --net=host -v /var/run/docker.sock:/var/run/docker.sock \
    -v /usr/share/docker/plugins:/usr/share/docker/plugins \
    -v /var/run/weave-plugin:/var/run/weave-plugin \
    -v /proc:/hostproc \
    weaveworks/plugin "$@"
