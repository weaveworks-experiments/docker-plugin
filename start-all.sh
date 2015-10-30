#!/bin/bash

set -e

weaveexec() {
    docker run --rm --privileged \
    -e VERSION \
    -e WEAVE_NO_FASTDP=true \
    -e WEAVE_DEBUG \
    -e WEAVE_DOCKER_ARGS \
    -e WEAVE_DNS_DOCKER_ARGS \
    -e WEAVE_PASSWORD \
    -e WEAVE_PORT \
    -e WEAVE_CONTAINER_NAME \
    -e DOCKER_BRIDGE \
    -v /var/run/docker.sock:/var/run/docker.sock \
    --entrypoint=./weave weaveworks/plugin $@
}

echo Run weave
weaveexec launch-router --ipalloc-range 10.20.0.0/16 $WEAVE_ARGS

echo Run weave plugin
docker rm -f weaveplugin 2>&1 || true
`dirname $0`/start-plugin.sh "$@"
