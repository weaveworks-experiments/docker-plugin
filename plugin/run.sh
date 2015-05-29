#!/bin/bash

set -e

docker run --rm --privileged \
    -e VERSION \
    -e WEAVE_DEBUG \
    -e WEAVE_DOCKER_ARGS \
    -e WEAVE_DNS_DOCKER_ARGS \
    -e WEAVE_PASSWORD \
    -e WEAVE_PORT \
    -e WEAVE_CONTAINER_NAME \
    -e DOCKER_BRIDGE \
    -v /var/run/docker.sock:/var/run/docker.sock \
    --entrypoint=./weave weaveworks/plugin \
    launch -iprange 10.20.0.0/16 $WEAVE_ARGS

sudo rm -f /usr/share/docker/plugins/weave.sock
docker rm -f weaveplugin
docker run --name=weaveplugin --privileged -d \
    --net=host -v /var/run/docker.sock:/var/run/docker.sock \
    -v /usr/share/docker/plugins:/usr/share/docker/plugins \
    -v /var/run/weave-plugin:/var/run/weave-plugin \
    -v /proc:/hostproc \
    weaveworks/plugin --socket=/usr/share/docker/plugins/weave.sock "$@"
