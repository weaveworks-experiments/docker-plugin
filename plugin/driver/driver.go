package driver

import (
	"fmt"
	"net"

	"github.com/docker/libnetwork/drivers/remote/api"
	"github.com/docker/libnetwork/types"

	. "github.com/weaveworks/weave/common"

	docker "github.com/fsouza/go-dockerclient"
	"github.com/vishvananda/netlink"

	"github.com/weaveworks/docker-plugin/plugin/skel"
)

const (
	WeaveContainer = "weave"
	WeaveBridge    = "weave"
)

type driver struct {
	dockerer
	version    string
	network    string
	nameserver string
	watcher    Watcher
}

func New(version string, nameserver string) (skel.Driver, error) {
	client, err := docker.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		return nil, fmt.Errorf("could not connect to docker: %s", err)
	}

	watcher, err := NewWatcher(client)
	if err != nil {
		return nil, err
	}

	return &driver{
		dockerer: dockerer{
			client: client,
		},
		nameserver: nameserver,
		version:    version,
		watcher:    watcher,
	}, nil
}

// === protocol handlers

var caps = &api.GetCapabilityResponse{
	Scope: "global",
}

func (driver *driver) GetCapabilities() (*api.GetCapabilityResponse, error) {
	Log.Debugf("Get capabilities: responded with %+v", caps)
	return caps, nil
}

func (driver *driver) CreateNetwork(create *api.CreateNetworkRequest) error {
	Log.Debugf("Create network request %+v", create)
	if driver.network != "" {
		return fmt.Errorf("you get just one network, and you already made %s", driver.network)
	}
	driver.network = create.NetworkID
	driver.watcher.WatchNetwork(driver.network)
	Log.Infof("Create network %s", driver.network)
	return nil
}

func (driver *driver) DeleteNetwork(delete *api.DeleteNetworkRequest) error {
	Log.Debugf("Delete network request: %+v", delete)
	if delete.NetworkID != driver.network {
		return fmt.Errorf("network %s not found", delete.NetworkID)
	}
	driver.network = ""
	driver.watcher.UnwatchNetwork(delete.NetworkID)
	Log.Infof("Destroy network %s", delete.NetworkID)
	return nil
}

func (driver *driver) CreateEndpoint(create *api.CreateEndpointRequest) (*api.CreateEndpointResponse, error) {
	Log.Debugf("Create endpoint request %+v", &create)
	netID := create.NetworkID
	endID := create.EndpointID

	if netID != driver.network {
		return nil, fmt.Errorf("no such network %s", netID)
	}

	var respIface *api.EndpointInterface

	if create.Interface == nil {
		ip, err := driver.allocateIP(endID)
		if err != nil {
			Log.Warningf("Error allocating IP: %s", err)
			return nil, fmt.Errorf("unable to allocate IP: %s", err)
		}
		Log.Debugf("Got IP from IPAM %s", ip.String())
		mac := makeMac(ip.IP)
		respIface = &api.EndpointInterface{
			Address:    ip.String(),
			MacAddress: mac,
		}
	}
	resp := &api.CreateEndpointResponse{
		Interface: respIface,
	}

	Log.Infof("Create endpoint %s %+v", endID, resp)
	return resp, nil
}

func (driver *driver) DeleteEndpoint(delete *api.DeleteEndpointRequest) error {
	Log.Debugf("Delete endpoint request: %+v", &delete)
	if err := driver.releaseIP(delete.EndpointID); err != nil {
		return fmt.Errorf("error releasing IP: %s", err)
	}
	Log.Infof("Delete endpoint %s", delete.EndpointID)
	return nil
}

func (driver *driver) EndpointInfo(req *api.EndpointInfoRequest) (*api.EndpointInfoResponse, error) {
	Log.Debugf("Endpoint info request: %+v", req)
	Log.Infof("Endpoint info %s", req.EndpointID)
	return &api.EndpointInfoResponse{Value: map[string]interface{}{}}, nil
}

func (driver *driver) JoinEndpoint(j *api.JoinRequest) (response *api.JoinResponse, error error) {
	endID := j.EndpointID

	// create and attach local name to the bridge
	local := vethPair(endID[:5])
	if err := netlink.LinkAdd(local); err != nil {
		error = fmt.Errorf("could not create veth pair: %s", err)
		return
	}

	var bridge *netlink.Bridge
	if maybeBridge, err := netlink.LinkByName(WeaveBridge); err != nil {
		err = fmt.Errorf(`bridge "%s" not present`, WeaveBridge)
		return
	} else {
		var ok bool
		if bridge, ok = maybeBridge.(*netlink.Bridge); !ok {
			Log.Errorf("%s is %+v", WeaveBridge, maybeBridge)
			err = fmt.Errorf(`device "%s" not a bridge`, WeaveBridge)
			return
		}
	}
	if netlink.LinkSetMaster(local, bridge) != nil || netlink.LinkSetUp(local) != nil {
		error = fmt.Errorf(`unable to bring veth up`)
		return
	}

	ifname := &api.InterfaceName{
		SrcName:   local.PeerName,
		DstPrefix: "ethwe",
	}

	response = &api.JoinResponse{
		InterfaceName: ifname,
	}
	if driver.nameserver != "" {
		routeToDNS := api.StaticRoute{
			Destination: driver.nameserver + "/32",
			RouteType:   types.CONNECTED,
			NextHop:     "",
		}
		response.StaticRoutes = []api.StaticRoute{routeToDNS}
	}
	Log.Infof("Join endpoint %s:%s to %s", j.NetworkID, j.EndpointID, j.SandboxKey)
	return
}

func (driver *driver) LeaveEndpoint(leave *api.LeaveRequest) error {
	Log.Debugf("Leave request: %+v", &leave)

	local := vethPair(leave.EndpointID[:5])
	if err := netlink.LinkDel(local); err != nil {
		Log.Warningf("unable to delete veth on leave: %s", err)
	}
	Log.Infof("Leave %s:%s", leave.NetworkID, leave.EndpointID)
	return nil
}

func (driver *driver) DiscoverNew(disco *api.DiscoveryNotification) error {
	Log.Debugf("Dicovery new notification: %+v", &disco)
	return nil
}

func (driver *driver) DiscoverDelete(disco *api.DiscoveryNotification) error {
	Log.Debugf("Dicovery delete notification: %+v", &disco)
	return nil
}

// ===

func vethPair(suffix string) *netlink.Veth {
	return &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: "vethwl" + suffix},
		PeerName:  "vethwg" + suffix,
	}
}

func makeMac(ip net.IP) string {
	hw := make(net.HardwareAddr, 6)
	hw[0] = 0x7a
	hw[1] = 0x42
	copy(hw[2:], ip.To4())
	return hw.String()
}
