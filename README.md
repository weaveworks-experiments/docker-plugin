# Weave network driver extension for Docker

This program is a
[remote driver](https://github.com/docker/libnetwork/blob/master/docs/remote.md)
plugin for libnetwork which creates networks and endpoints on a Weave
network.

## Running

The driver executable can be run in a container, or just on the
host. It assumes you have Weave and WeaveDNS running already.

The command-line arguments are:

 * `--nameserver=<IP address>`, which is used to provide a static
   route to WeaveDNS (see "workarounds" below). If not supplied, the
   driver will assume that plugins have a route to the nameserver
   (e.g., they will have another interface which is on the same subnet
   as WeaveDNS). Note that this does not affect the `/etc/resolv.conf`
   for containers -- you will still need to use the `--dns` option to
   `docker run`.

 * `--socket=<path>`, which is the socket on which to listen for the
   [plugin protocol](https://github.com/docker/docker/blob/master/experimental/plugin_api.md). This defaults to what Docker expects, that is
   `"/run/docker/plugins/weave.sock"`, but you may wish to
   change it, for instance if you're running more than one instance of
   the driver for some reason.

 * `--debug=<true|false>`, which tells the plugin to emit information
   useful for debugging.

The driver will need elevated privileges (e.g., to be run with
`sudo`), for creating network interfaces, and access to the Docker API
socket (usually `/var/run/docker.sock`).

### Running as a container

If you run the plugin as a Docker container, it needs the
`--privileged` and `--net=host` flags to be able to create network
interfaces on the host.

It will also need to share with Docker the socket on which it listens,
which essentially needs you need to mount the directory
`/run/docker/plugins/`; and, it will need the Docker API socket
bind-mounted.

An example of a Docker command-line for running the driver is given in
`start-plugin.sh`. The script `start-all.sh` gives an example of
running Weave and the driver plugin.

## Caveats and workarounds

There are some tricks to bear in mind when using the driver plugin for
the minute. Ideally these will disappear as the driver interface is
refined, Weave is enhanced, and so on.

### Docker needs a "cluster store"

Weave operates as a "globally scoped" libnetwork driver, which means
libnetwork will assume all networks and endpoints are commonly known
to all hosts. It does this by using a shared database which you must
provide.

As a consequence, you need to supply Docker with the address of a "cluster
store" when you start it; for example, an etcd installation.

There's no specific documentation of using a cluster store, but the
first part of this guide may help:
[https://github.com/docker/docker/blob/master/docs/userguide/networking/get-started-overlay.md][].

### Only one network at a time

In general, libnetwork requires drivers to isolate networks from each
other. However, in Weave all addresses are on the same network;
although they can be on different subnets, the plugin cannot guarantee
that libnetwork's IPAM will arrange that in the appropriate way.

As a result, for the moment you can have only one network at
a time.

### Recovery after restarts

There is no way provided by libnetwork for the plugin to get its
current configuration after a restart; so, it is fairly easy to get
into a situation in which Docker knows about an object (e.g., a
network) that the plugin does not.

As a workaround, you can `rm` objects before creating them, to make
sure the plugin is told about the state.

### WeaveDNS reachability

When using Weave "classic", your containers generally end up with two
network interfaces: one on the weave network, and one on the docker
bridge. WeaveDNS takes advantage of this by binding to the docker
bridge IP, so all containers on the host can talk to it.

However, when used as a plugin, containers will in general only have
one interface -- that supplied by the plugin. This means that they
won't be able to talk to WeaveDNS unless they have a route to
WeaveDNS.

This can be arranged by supplying the _Weave_ IP given to WeaveDNS to
the plugin with the `--nameserver` argument. Doing so will make the
driver plugin configure a static route to the WeaveDNS IP on each
interface it gives to a container. The WeaveDNS container will _also_
need a route back to the containers; but since we are not in control
of IP address allocation, it's up to you to arrange that, until we can
think of a workaround.

### WeaveDNS registration

The driver plugin will assume any container with a domain that is a
subdomain of `.weave.local` should have its (fully-qualified) name and
IP address added to WeaveDNS.

Containers will only be registered with WeaveDNS if the plugin is
running when they are started (and removes names when containers
stop), since the driver plugin listens to the Docker event stream to
see containers come and go. Endpoints added after the fact won't
trigger a DNS registration.

## Building

    make

OK, not always that simple: you will need the same setup as the
[weave repository](https://github.com/weaveworks/weave) needs; easiest
is to use the `Vagrantfile` in there and add another shared folder for
this directory.

Alternatively, you can refer to the image on DockerHub, though be
aware it may not always be up to date: `weaveworks/plugin`.
