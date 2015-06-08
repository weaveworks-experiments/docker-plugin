package driver

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/libnetwork/types"

	. "github.com/weaveworks/weave/common"

	docker "github.com/fsouza/go-dockerclient"
	"github.com/gorilla/mux"
	"github.com/vishvananda/netlink"
)

const (
	MethodReceiver    = "NetworkDriver"
	WeaveContainer    = "weave"
	WeaveBridge       = "weave"
	WeaveDNSContainer = "weavedns"
)

type Driver interface {
	Listen(string) error
}

type driver struct {
	version    string
	network    string
	confDir    string
	nameserver string
	client     *docker.Client
}

func New(version string, nameserver string, confDir string) (Driver, error) {
	client, err := docker.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		return nil, fmt.Errorf("could not connect to docker: %s", err)
	}

	nameserverIP := net.ParseIP(nameserver)
	if nameserverIP == nil {
		return nil, fmt.Errorf(`could not parse nameserver IP "%s"`, nameserver)
	}

	return &driver{
		version:    version,
		nameserver: nameserver,
		client:     client,
		confDir:    confDir,
	}, nil
}

func (driver *driver) Listen(socket string) error {
	router := mux.NewRouter()
	router.NotFoundHandler = http.HandlerFunc(notFound)

	router.Methods("GET").Path("/status").HandlerFunc(driver.status)
	router.Methods("POST").Path("/Plugin.Activate").HandlerFunc(driver.handshake)

	handleMethod := func(method string, h http.HandlerFunc) {
		router.Methods("POST").Path(fmt.Sprintf("/%s.%s", MethodReceiver, method)).HandlerFunc(h)
	}

	handleMethod("CreateNetwork", driver.createNetwork)
	handleMethod("DeleteNetwork", driver.deleteNetwork)
	handleMethod("CreateEndpoint", driver.createEndpoint)
	handleMethod("DeleteEndpoint", driver.deleteEndpoint)
	handleMethod("EndpointOperInfo", driver.infoEndpoint)
	handleMethod("Join", driver.joinEndpoint)
	handleMethod("Leave", driver.leaveEndpoint)

	// Put the docker bridge IP into a resolv.conf to be used later.
	ioutil.WriteFile(driver.resolvConfPath(), []byte("nameserver "+driver.nameserver), os.ModePerm)

	var (
		listener net.Listener
		err      error
	)

	listener, err = net.Listen("unix", socket)
	if err != nil {
		return err
	}

	return http.Serve(listener, router)
}

func notFound(w http.ResponseWriter, r *http.Request) {
	Warning.Printf("[plugin] Not found: %+v", r)
	http.NotFound(w, r)
}

func sendError(w http.ResponseWriter, msg string, code int) {
	Error.Printf("%d %s", code, msg)
	http.Error(w, msg, code)
}

func errorResponsef(w http.ResponseWriter, fmtString string, item ...interface{}) {
	json.NewEncoder(w).Encode(map[string]string{
		"Err": fmt.Sprintf(fmtString, item...),
	})
}

func objectResponse(w http.ResponseWriter, obj interface{}) {
	if err := json.NewEncoder(w).Encode(obj); err != nil {
		sendError(w, "Could not JSON encode response", http.StatusInternalServerError)
		return
	}
}

func emptyResponse(w http.ResponseWriter) {
	json.NewEncoder(w).Encode(map[string]string{})
}

// === protocol handlers

type handshakeResp struct {
	Implements []string
}

func (driver *driver) handshake(w http.ResponseWriter, r *http.Request) {
	err := json.NewEncoder(w).Encode(&handshakeResp{
		[]string{"NetworkDriver"},
	})
	if err != nil {
		Error.Fatal("handshake encode:", err)
		sendError(w, "encode error", http.StatusInternalServerError)
		return
	}
	Info.Printf("Handshake completed")
}

func (driver *driver) status(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, fmt.Sprintln("weave plugin", driver.version))
}

type networkCreate struct {
	NetworkID string
	Options   map[string]interface{}
}

func (driver *driver) createNetwork(w http.ResponseWriter, r *http.Request) {
	var create networkCreate
	err := json.NewDecoder(r.Body).Decode(&create)
	if err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	Debug.Printf("Create network request %+v", &create)

	if driver.network != "" {
		errorResponsef(w, "You get just one network, and you already made %s", driver.network)
		return
	}

	driver.network = create.NetworkID
	emptyResponse(w)
	Info.Printf("Create network %s", driver.network)
}

type networkDelete struct {
	NetworkID string
}

func (driver *driver) deleteNetwork(w http.ResponseWriter, r *http.Request) {
	var delete networkDelete
	if err := json.NewDecoder(r.Body).Decode(&delete); err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	Debug.Printf("Delete network request: %+v", &delete)
	if delete.NetworkID != driver.network {
		errorResponsef(w, "Network %s not found", delete.NetworkID)
		return
	}
	driver.network = ""
	emptyResponse(w)
	Info.Printf("Destroy network %s", delete.NetworkID)
}

type endpointCreate struct {
	NetworkID  string
	EndpointID string
	Interfaces []*iface
	Options    map[string]interface{}
}

type iface struct {
	ID         int
	SrcName    string
	DstName    string
	Address    string
	MacAddress string
}

type endpointResponse struct {
	Interfaces []*iface
}

func (driver *driver) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var create endpointCreate
	if err := json.NewDecoder(r.Body).Decode(&create); err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	Debug.Printf("Create endpoint request %+v", &create)
	netID := create.NetworkID
	endID := create.EndpointID

	if netID != driver.network {
		errorResponsef(w, "No such network %s", netID)
		return
	}

	ip, err := driver.ipamOp(endID, "POST")
	if err != nil {
		Warning.Printf("Error allocating IP:", err)
		sendError(w, "Unable to allocate IP", http.StatusInternalServerError)
		return
	}

	mac := makeMac(ip.IP)

	respIface := &iface{
		Address:    ip.String(),
		MacAddress: mac,
	}
	resp := &endpointResponse{
		Interfaces: []*iface{respIface},
	}

	Debug.Printf("Create: %+v", &resp)
	objectResponse(w, resp)

	domainname, ok := create.Options["io.docker.network.domainname"]
	if ok && domainname.(string) == "weave.local" {
		hostname := create.Options["io.docker.network.hostname"]
		fqdn := fmt.Sprintf("%s.%s", hostname, domainname)
		if err := driver.registerWithDNS(endID, fqdn, ip); err != nil {
			Warning.Printf("unable to register with DNS: %s", err)
		}
	}

	Info.Printf("Create endpoint %s %+v", endID, resp)
}

type endpointDelete struct {
	NetworkID  string
	EndpointID string
}

func (driver *driver) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	var delete endpointDelete
	if err := json.NewDecoder(r.Body).Decode(&delete); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Debug.Printf("Delete endpoint request: %+v", &delete)
	emptyResponse(w)
	if err := driver.deregisterWithDNS(delete.EndpointID); err != nil {
		Warning.Printf("unable to deregister with DNS: %s", err)
	}
	if err := driver.releaseIP(delete.EndpointID); err != nil {
		Warning.Printf("error releasing IP: %s", err)
	}
	Info.Printf("Delete endpoint %s", delete.EndpointID)
}

type endpointInfoReq struct {
	NetworkID  string
	EndpointID string
}

type endpointInfo struct {
	Value map[string]interface{}
}

func (driver *driver) infoEndpoint(w http.ResponseWriter, r *http.Request) {
	var info endpointInfoReq
	if err := json.NewDecoder(r.Body).Decode(&info); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Debug.Printf("Endpoint info request: %+v", &info)
	objectResponse(w, &endpointInfo{Value: map[string]interface{}{}})
	Info.Printf("Endpoint info %s", info.EndpointID)
}

type joinInfo struct {
	InterfaceNames []*iface
	Gateway        string
	GatewayIPv6    string
	HostsPath      string
	ResolvConfPath string
}

type join struct {
	NetworkID  string
	EndpointID string
	SandboxKey string
	Options    map[string]interface{}
}

type staticRoute struct {
	Destination string
	RouteType   int
	NextHop     string
	InterfaceID int
}

type joinResponse struct {
	HostsPath      string
	ResolvConfPath string
	Gateway        string
	InterfaceNames []*iface
	StaticRoutes   []*staticRoute
}

func (driver *driver) joinEndpoint(w http.ResponseWriter, r *http.Request) {
	var j join
	if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Debug.Printf("Join request: %+v", &j)

	endID := j.EndpointID

	// create and attach local name to the bridge
	local := vethPair(endID[:5])
	if err := netlink.LinkAdd(local); err != nil {
		Error.Print(err)
		errorResponsef(w, "could not create veth pair")
		return
	}

	var bridge *netlink.Bridge
	if maybeBridge, err := netlink.LinkByName(WeaveBridge); err != nil {
		Error.Print(err)
		errorResponsef(w, `bridge "%s" not present`, WeaveBridge)
		return
	} else {
		var ok bool
		if bridge, ok = maybeBridge.(*netlink.Bridge); !ok {
			Error.Printf("%s is %+v", WeaveBridge, maybeBridge)
			errorResponsef(w, `device "%s" not a bridge`, WeaveBridge)
			return
		}
	}
	if netlink.LinkSetMaster(local, bridge) != nil || netlink.LinkSetUp(local) != nil {
		errorResponsef(w, `unable to bring veth up`)
		return
	}

	ifname := &iface{
		SrcName: local.PeerName,
		DstName: "ethwe",
		ID:      0,
	}
	routeToDNS := &staticRoute{
		Destination: driver.nameserver + "/32",
		RouteType:   types.CONNECTED,
		NextHop:     "",
		InterfaceID: 0,
	}
	res := &joinResponse{
		InterfaceNames: []*iface{ifname},
		ResolvConfPath: driver.resolvConfPath(),
		StaticRoutes:   []*staticRoute{routeToDNS},
	}

	objectResponse(w, res)
	Info.Printf("Join endpoint %s:%s to %s", j.NetworkID, j.EndpointID, j.SandboxKey)
}

type leave struct {
	NetworkID  string
	EndpointID string
	Options    map[string]interface{}
}

func (driver *driver) leaveEndpoint(w http.ResponseWriter, r *http.Request) {
	var l leave
	if err := json.NewDecoder(r.Body).Decode(&l); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Debug.Printf("Leave request: %+v", &l)

	local := vethPair(l.EndpointID[:5])
	if err := netlink.LinkDel(local); err != nil {
		Warning.Printf("unable to delete veth on leave: %s", err)
	}
	emptyResponse(w)
	Info.Printf("Leave %s:%s", l.NetworkID, l.EndpointID)
}

// ===

func vethPair(suffix string) *netlink.Veth {
	return &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: "vethwl" + suffix},
		PeerName:  "vethwg" + suffix,
	}
}

func (driver *driver) getContainerBridgeIP(nameOrID string) (string, error) {
	info, err := driver.client.InspectContainer(nameOrID)
	if err != nil {
		return "", err
	}
	return info.NetworkSettings.IPAddress, nil
}

func (driver *driver) resolvConfPath() string {
	return filepath.Join(driver.confDir, "resolv.conf")
}

func (driver *driver) ipamOp(ID string, op string) (*net.IPNet, error) {
	weaveip, err := driver.getContainerBridgeIP(WeaveContainer)
	if err != nil {
		return nil, err
	}

	var res *http.Response
	url := fmt.Sprintf("http://%s:6784/ip/%s", weaveip, ID)
	if op == "POST" {
		res, err = http.Post(url, "", nil)
	} else if op == "GET" {
		res, err = http.Get(url)
	}

	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("received status %d from IPAM", res.StatusCode)
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	ip, ipnet, err := net.ParseCIDR(string(body))
	if err == nil {
		ipnet.IP = ip
	}
	return ipnet, err
}

func (driver *driver) releaseIP(ID string) error {
	weaveip, err := driver.getContainerBridgeIP(WeaveContainer)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("DELETE", fmt.Sprintf("http://%s:6784/ip/%s", weaveip, ID), nil)
	if err != nil {
		return err
	}
	cl := &http.Client{}
	res, err := cl.Do(req)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected HTTP status code from IP release: %d", res.StatusCode)
	}
	return nil
}

func makeMac(ip net.IP) string {
	hw := make(net.HardwareAddr, 6)
	hw[0] = 0x7a
	hw[1] = 0x42
	copy(hw[2:], ip.To4())
	return hw.String()
}

func (driver *driver) registerWithDNS(endpointID string, fqdn string, ip *net.IPNet) error {
	dnsip, err := driver.getContainerBridgeIP(WeaveDNSContainer)
	if err != nil {
		return fmt.Errorf("nameserver not available: %s", err)
	}
	data := url.Values{}
	data.Add("fqdn", fqdn)

	req, err := http.NewRequest("PUT", fmt.Sprintf("http://%s:6785/name/%s/%s", dnsip, endpointID, ip.IP.String()), strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	cl := &http.Client{}
	res, err := cl.Do(req)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("non-OK status from nameserver: %d", res.StatusCode)
	}
	return nil
}

func (driver *driver) deregisterWithDNS(endpointID string) error {
	dnsip, err := driver.getContainerBridgeIP(WeaveDNSContainer)
	if err != nil {
		return fmt.Errorf("nameserver not available: %s", err)
	}

	ip, err := driver.ipamOp(endpointID, "GET")
	if err != nil {
		return err
	}

	req, err := http.NewRequest("DELETE", fmt.Sprintf("http://%s:6785/name/%s/%s", dnsip, endpointID, ip.IP.String()), nil)
	if err != nil {
		return err
	}

	cl := &http.Client{}
	res, err := cl.Do(req)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("non-OK status from nameserver: %d", res.StatusCode)
	}
	return nil
}
