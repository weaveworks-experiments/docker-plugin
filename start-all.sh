#!/bin/bash

set -e

weaveexec() {
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
    --entrypoint=./weave weaveworks/plugin $@
}

echo Run weave
weaveexec launch -iprange 10.20.0.0/16 $WEAVE_ARGS

echo Run weaveDNS
weaveexec launch-dns 10.254.254.1/24 --watch=false $WEAVE_DNS_ARGS

echo Make weaved containers routable from weaveDNS
WEAVEDNS_PID=$(docker inspect --format='{{ .State.Pid }}' weavedns)
[ ! -d /var/run/netns ] && sudo mkdir -p /var/run/netns
sudo ln -s /proc/$WEAVEDNS_PID/ns/net /var/run/netns/$WEAVEDNS_PID
sudo ip netns exec $WEAVEDNS_PID sudo ip route add 10.20.0.0/16 dev ethwe
sudo rm -f /var/run/netns/$WEAVEDNS_PID

echo Run weave plugin
sudo rm -f /usr/share/docker/plugins/weave.sock
docker rm -f weaveplugin || true

docker run --name=weaveplugin --privileged -d \
    --net=host -v /var/run/docker.sock:/var/run/docker.sock \
    -v /usr/share/docker/plugins:/usr/share/docker/plugins \
    -v /var/run/weave-plugin:/var/run/weave-plugin \
    -v /proc:/hostproc \
    weaveworks/plugin --nameserver=10.254.254.1 --socket=/usr/share/docker/plugins/weave.sock "$@"
