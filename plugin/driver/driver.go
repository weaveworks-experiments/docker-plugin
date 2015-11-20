package driver

import (
	"fmt"

	"github.com/docker/libnetwork/drivers/remote/api"
	"github.com/docker/libnetwork/types"

	. "github.com/weaveworks/weave/common"
	"github.com/weaveworks/weave/common/odp"

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
	nameserver string
	watcher    Watcher
}

func New(version string, nameserver string) (skel.Driver, error) {
	client, err := docker.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		return nil, errorf("could not connect to docker: %s", err)
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

func errorf(format string, a ...interface{}) error {
	Log.Errorf(format, a...)
	return fmt.Errorf(format, a...)
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
	driver.watcher.WatchNetwork(create.NetworkID)
	Log.Infof("Create network %s", create.NetworkID)
	return nil
}

func (driver *driver) DeleteNetwork(delete *api.DeleteNetworkRequest) error {
	Log.Debugf("Delete network request: %+v", delete)
	driver.watcher.UnwatchNetwork(delete.NetworkID)
	Log.Infof("Destroy network %s", delete.NetworkID)
	return nil
}

func (driver *driver) CreateEndpoint(create *api.CreateEndpointRequest) (*api.CreateEndpointResponse, error) {
	Log.Debugf("Create endpoint request %+v", create)
	endID := create.EndpointID

	if create.Interface == nil {
		return nil, fmt.Errorf("Not supported: creating an interface from within CreateEndpoint")
	}
	resp := &api.CreateEndpointResponse{}

	Log.Infof("Create endpoint %s %+v", endID, resp)
	return resp, nil
}

func (driver *driver) DeleteEndpoint(delete *api.DeleteEndpointRequest) error {
	Log.Debugf("Delete endpoint request: %+v", delete)
	Log.Infof("Delete endpoint %s", delete.EndpointID)
	return nil
}

func (driver *driver) EndpointInfo(req *api.EndpointInfoRequest) (*api.EndpointInfoResponse, error) {
	Log.Debugf("Endpoint info request: %+v", req)
	Log.Infof("Endpoint info %s", req.EndpointID)
	return &api.EndpointInfoResponse{Value: map[string]interface{}{}}, nil
}

func (driver *driver) JoinEndpoint(j *api.JoinRequest) (*api.JoinResponse, error) {
	endID := j.EndpointID

	// create and attach local name to the bridge
	local := vethPair(endID[:5])
	if err := netlink.LinkAdd(local); err != nil {
		return nil, errorf("could not create veth pair: %s", err)
	}

	if maybeBridge, err := netlink.LinkByName(WeaveBridge); err != nil {
		return nil, errorf(`bridge "%s" not present; did you launch weave?`, WeaveBridge)
	} else {
		switch maybeBridge.(type) {
		case *netlink.Bridge:
			if err := netlink.LinkSetMasterByIndex(local, maybeBridge.Attrs().Index); err != nil {
				return nil, errorf(`unable to set master: %s`, err)
			}
		case *netlink.GenericLink:
			if maybeBridge.Type() != "openvswitch" {
				Log.Errorf("device %s is %+v", WeaveBridge, maybeBridge)
				return nil, errorf(`device "%s" is of type "%s"`, WeaveBridge, maybeBridge.Type())
			}
			odp.AddDatapathInterface(WeaveBridge, local.Name)
		case *netlink.Device:
			Log.Warnf("kernel does not report what kind of device %s is, just %+v", WeaveBridge, maybeBridge)
			// Assume it's our openvswitch device, and the kernel has not been updated to report the kind.
			odp.AddDatapathInterface(WeaveBridge, local.Name)
		default:
			Log.Errorf("device %s is %+v", WeaveBridge, maybeBridge)
			return nil, errorf(`device "%s" not a bridge`, WeaveBridge)
		}
	}
	if err := netlink.LinkSetUp(local); err != nil {
		return nil, errorf(`unable to bring veth up: %s`, err)
	}

	ifname := &api.InterfaceName{
		SrcName:   local.PeerName,
		DstPrefix: "ethwe",
	}

	response := &api.JoinResponse{
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
	return response, nil
}

func (driver *driver) LeaveEndpoint(leave *api.LeaveRequest) error {
	Log.Debugf("Leave request: %+v", leave)

	local := vethPair(leave.EndpointID[:5])
	if err := netlink.LinkDel(local); err != nil {
		Log.Warningf("unable to delete veth on leave: %s", err)
	}
	Log.Infof("Leave %s:%s", leave.NetworkID, leave.EndpointID)
	return nil
}

func (driver *driver) DiscoverNew(disco *api.DiscoveryNotification) error {
	Log.Debugf("Dicovery new notification: %+v", disco)
	return nil
}

func (driver *driver) DiscoverDelete(disco *api.DiscoveryNotification) error {
	Log.Debugf("Dicovery delete notification: %+v", disco)
	return nil
}

// ===

func vethPair(suffix string) *netlink.Veth {
	return &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: "vethwl" + suffix},
		PeerName:  "vethwg" + suffix,
	}
}
