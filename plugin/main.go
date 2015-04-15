package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/gorilla/mux"
	. "github.com/weaveworks/weave/common"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
)

var version = "(unreleased version)"

type handshakeResp struct {
	Implements []string
}

type joinInfo struct {
	InterfaceNames []*iface
	Gateway        string
	GatewayIPv6    string
	HostsPath      string
	ResolvConfPath string
}

var (
	network string
	subnet  *net.IPNet
)

func main() {
	var (
		justVersion bool
		address     string
		debug       bool
	)

	flag.BoolVar(&justVersion, "version", false, "print version and exit")
	flag.BoolVar(&debug, "debug", false, "output debugging info to stderr")
	flag.StringVar(&address, "socket", "/var/run/docker-plugin/plugin.sock", "socket on which to listen")

	flag.Parse()

	if justVersion {
		fmt.Printf("weave plugin %s\n", version)
		os.Exit(0)
	}

	InitDefaultLogging(debug)

	peers := flag.Args()

	router := mux.NewRouter()
	router.NotFoundHandler = http.HandlerFunc(notFound)
	router.Methods("GET").Path("/status").HandlerFunc(status)

	router.Methods("POST").Path("/Plugin.Activate").HandlerFunc(handshake)

	router.Methods("POST").Path("/NetworkDriver.CreateNetwork").HandlerFunc(createNetwork)
	router.Methods("POST").Path("/NetworkDriver.DeleteNetwork").HandlerFunc(deleteNetwork)

	router.Methods("POST").Path("/NetworkDriver.CreateEndpoint").HandlerFunc(createEndpoint)
	router.Methods("POST").Path("/NetworkDriver.DeleteEndpoint").HandlerFunc(deleteEndpoint)
	router.Methods("POST").Path("/NetworkDriver.EndpointOperInfo").HandlerFunc(infoEndpoint)

	router.Methods("POST").Path("/NetworkDriver.Join").HandlerFunc(joinEndpoint)
	router.Methods("POST").Path("/NetworkDriver.Leave").HandlerFunc(leaveEndpoint)

	var listener net.Listener

	listener, err := net.Listen("unix", address)
	if err != nil {
		Error.Fatalf("[plugin] Unable to listen on %s: %s", address, err)
	}

	sub := "10.2.0.0/16"

	_, subnet, err = net.ParseCIDR(sub)
	if err != nil {
		Error.Fatalf("Invalid subnet CIDR %s", sub)
	}
	weaveArgs := []string{"launch", "-iprange", subnet.String()}
	if err = runWeaveCmd(append(weaveArgs, peers...)); err != nil {
		Error.Fatal("Problem launching Weave: " + err.Error())
	}

	if err = http.Serve(listener, router); err != nil {
		Error.Fatalf("[plugin] Internal error: %s", err)
	}
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

func handshake(w http.ResponseWriter, r *http.Request) {
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

func status(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, fmt.Sprintln("weave plugin", version))
}

type networkCreate struct {
	NetworkID string
	Options   map[string]interface{}
}

func createNetwork(w http.ResponseWriter, r *http.Request) {
	var create networkCreate
	err := json.NewDecoder(r.Body).Decode(&create)
	if err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	Debug.Printf("Create network request %+v", &create)

	if network != "" {
		errorResponsef(w, "You get just one network, and you already made %s", network)
		return
	}

	network = create.NetworkID
	emptyResponse(w)
	Info.Printf("Create network %s", network)
}

type networkDelete struct {
	NetworkID string
}

func deleteNetwork(w http.ResponseWriter, r *http.Request) {
	var delete networkDelete
	if err := json.NewDecoder(r.Body).Decode(&delete); err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	Debug.Printf("Delete network request: %+v", &delete)
	if delete.NetworkID != network {
		errorResponsef(w, "Network %s not found", delete.NetworkID)
		return
	}
	if _, err := getWeaveCmd([]string{"stop"}); err != nil {
		sendError(w, "Unable to stop weave: "+err.Error(), http.StatusInternalServerError)
		return
	}
	network = ""
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

func createEndpoint(w http.ResponseWriter, r *http.Request) {
	var create endpointCreate
	if err := json.NewDecoder(r.Body).Decode(&create); err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	Debug.Printf("Create endpoint request %+v", &create)
	netID := create.NetworkID
	endID := create.EndpointID

	if netID != network {
		errorResponsef(w, "No such network %s", netID)
		return
	}

	ip, err := getIP(endID)
	if err != nil {
		Warning.Printf("Error allocating IP:", err)
		sendError(w, "Unable to allocate IP", http.StatusInternalServerError)
		return
	}
	prefix, _ := subnet.Mask.Size()
	mac := makeMac(ip)

	respIface := &iface{
		Address: (&net.IPNet{
			ip,
			net.CIDRMask(prefix, 32),
		}).String(),
		MacAddress: mac,
	}
	resp := &endpointResponse{
		Interfaces: []*iface{respIface},
	}

	Debug.Printf("Create: %+v", &resp)
	objectResponse(w, resp)
	Info.Printf("Create endpoint %s %+v", endID, resp)
}

type endpointDelete struct {
	NetworkID  string
	EndpointID string
}

func deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	var delete endpointDelete
	if err := json.NewDecoder(r.Body).Decode(&delete); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Debug.Printf("Delete endpoint request: %+v", &delete)
	// TODO
	emptyResponse(w)
	Info.Printf("Delete endpoint %s", delete.EndpointID)
}

type endpointInfoReq struct {
	NetworkID  string
	EndpointID string
}

type endpointInfo struct {
	Value map[string]interface{}
}

func infoEndpoint(w http.ResponseWriter, r *http.Request) {
	var info endpointInfoReq
	if err := json.NewDecoder(r.Body).Decode(&info); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Debug.Printf("Endpoint info request: %+v", &info)
	objectResponse(w, &endpointInfo{Value: map[string]interface{}{}})
	Info.Printf("Endpoint info %s", info.EndpointID)
}

type join struct {
	NetworkID  string
	EndpointID string
	SandboxKey string
	Options    map[string]interface{}
}

type joinResponse struct {
	HostsPath      string
	ResolvConfPath string
	Gateway        string
	InterfaceNames []*iface
}

func joinEndpoint(w http.ResponseWriter, r *http.Request) {
	var j join
	if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Debug.Printf("Join request: %+v", &j)

	endID := j.EndpointID

	// use the endpoint ID bytes to make veth names, and cross fingers
	localName := "vethwel" + endID[:5]
	guestName := "vethweg" + endID[:5]
	// create and attach local name to the bridge
	ipout, err := doIpCmd([]string{"link", "add", "name", localName, "type", "veth", "peer", "name", guestName})
	if err != nil {
		Warning.Print(ipout)
		sendError(w, "Could not configure net device", http.StatusInternalServerError)
		return
	}
	ipout, err = doIpCmd([]string{"link", "set", localName, "master", "weave"})
	if err != nil {
		Warning.Print(ipout)
		sendError(w, "Could not configure net device", http.StatusInternalServerError)
		return
	}
	ipout, err = doIpCmd([]string{"link", "set", localName, "up"})
	if err != nil {
		Warning.Print(ipout)
		sendError(w, "Could not configure net device", http.StatusInternalServerError)
		return
	}

	ifname := &iface{
		SrcName: guestName,
		DstName: "ethwe",
		ID:      0,
	}
	res := &joinResponse{
		InterfaceNames: []*iface{ifname},
	}

	objectResponse(w, res)
	Info.Printf("Join endpoint %s:%s to %s", j.NetworkID, j.EndpointID, j.SandboxKey)
}

type leave struct {
	NetworkID  string
	EndpointID string
	Options    map[string]interface{}
}

func leaveEndpoint(w http.ResponseWriter, r *http.Request) {
	var l leave
	if err := json.NewDecoder(r.Body).Decode(&l); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	// TODO
	Debug.Printf("Leave request: %+v", &l)
	emptyResponse(w)
	Info.Printf("Leave %s:%s", l.NetworkID, l.EndpointID)
}

// ===

func getWeaveCmd(args []string) (string, error) {
	cmd := exec.Command("./weave", args...)
	cmd.Env = []string{"PATH=/usr/bin:/usr/local/bin", "WEAVE_DEBUG=true"}
	var buf bytes.Buffer
	cmd.Stderr = &buf
	out, err := cmd.Output()
	if err != nil {
		Warning.Print(buf.String())
	}
	return string(out), err
}

func runWeaveCmd(args []string) error {
	cmd := exec.Command("./weave", append([]string{}, args...)...)
	cmd.Env = []string{
		"PATH=/sbin:/usr/sbin:/bin:/usr/bin:/usr/local/bin",
		"WEAVE_DEBUG=true"}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		Warning.Print(err.Error())
	}
	return err
}

func doIpCmd(args []string) (string, error) {
	cmd := exec.Command("ip", args...)
	cmd.Env = []string{"PATH=/usr/bin:/usr/local/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		Warning.Print(string(out))
	}
	return string(out), err
}

// assumed to be in the subnet
func getIP(ID string) (net.IP, error) {
	res, err := getWeaveCmd([]string{"alloc", ID})
	if err != nil {
		return nil, err
	}
	ip, _, err := net.ParseCIDR(res)
	return ip, err
}

func makeMac(ip net.IP) string {
	hw := make(net.HardwareAddr, 6)
	hw[0] = 0x7a
	hw[1] = 0x42
	copy(hw[2:], ip.To4())
	return hw.String()
}
