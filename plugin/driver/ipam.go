package driver

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"

	. "github.com/weaveworks/weave/common"
)

func (d *dockerer) ipamOp(ID string, op string) (*net.IPNet, error) {
	weaveip, err := "127.0.0.1", error(nil) //d.getContainerBridgeIP(WeaveContainer)
	Log.Debugf("IPAM operation %s for %s", op, ID)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("http://%s:6784/ip/%s", weaveip, ID)
	Log.Debugf("Attempting to %s to %s", op, url)
	req, err := http.NewRequest(op, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("unexpected HTTP status code from IPAM: %d", res.StatusCode)
	}
	if op == "DELETE" {
		return nil, nil
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	return parseIP(string(body))
}

// returns an IP for the ID given, allocating a fresh one if necessary
func (d *dockerer) allocateIP(ID string) (*net.IPNet, error) {
	return d.ipamOp(ID, "POST")
}

// returns an IP for the ID given, or nil if one has not been
// allocated
func (d *dockerer) lookupIP(ID string) (*net.IPNet, error) {
	return d.ipamOp(ID, "GET")
}

// release an IP which is no longer needed
func (d *dockerer) releaseIP(ID string) error {
	_, err := d.ipamOp(ID, "DELETE")
	return err
}

func parseIP(body string) (*net.IPNet, error) {
	ip, ipnet, err := net.ParseCIDR(string(body))
	if err != nil {
		return nil, err
	}
	ipnet.IP = ip
	return ipnet, nil
}
