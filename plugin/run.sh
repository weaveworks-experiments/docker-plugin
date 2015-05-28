#!/bin/bash

sudo rm -f /usr/share/docker/plugins/weave.sock
docker rm -f weaveplugin

docker run --name=weaveplugin --privileged -d \
    --net=host -v /var/run/docker.sock:/var/run/docker.sock \
    -v /usr/share/docker/plugins:/usr/share/docker/plugins \
    -v /var/run/weave-plugin:/var/run/weave-plugin \
    -v /proc:/hostproc \
    weaveworks/plugin --socket=/usr/share/docker/plugins/weave.sock  "$@"
