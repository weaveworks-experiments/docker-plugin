#!/bin/sh

set -e

docker run --name=weaveplugin --privileged -d \
    --net=host -v /var/run/docker.sock:/var/run/docker.sock \
    -v /run/docker/plugins:/usr/share/docker/plugins \
    weaveworks/plugin "$@"
