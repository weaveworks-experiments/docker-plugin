package driver

import (
	"net"

	"github.com/docker/libnetwork/ipamapi"
	. "github.com/weaveworks/weave/common"
)

type ipam struct {
	d dockerer
}

func NewIpam(version string) (ipamapi.Ipam, error) {
	return &ipam{}, nil
}

func (i *ipam) GetDefaultAddressSpaces() (string, string, error) {
	Log.Debugln("GetDefaultAddressSpaces")
	return "weavelocal", "weaveglobal", nil
}

func (i *ipam) RequestPool(addressSpace, pool, subPool string, options map[string]string, v6 bool) (string, *net.IPNet, map[string]string, error) {
	Log.Debugln("RequestPool", addressSpace, pool, subPool, options)
	_, cidr, _ := net.ParseCIDR("10.32.0.0/12")
	return "weavepool", cidr, nil, nil
}

func (i *ipam) ReleasePool(poolID string) error {
	Log.Debugln("ReleasePool", poolID)
	return nil
}

func (i *ipam) RequestAddress(poolID string, address net.IP, options map[string]string) (*net.IPNet, map[string]string, error) {
	Log.Debugln("RequestAddress", poolID, address, options)
	ip, err := i.d.allocateIP("HACK")
	Log.Debugln("allocateIP returned", ip, err)
	return ip, nil, err
}

func (i *ipam) ReleaseAddress(poolID string, address net.IP) error {
	Log.Debugln("ReleaseAddress", poolID, address)
	return i.d.releaseIP("HACK")
}
