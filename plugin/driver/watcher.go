package driver

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/fsouza/go-dockerclient"
	. "github.com/weaveworks/weave/common"
)

const (
	WeaveDNSContainer = "weavedns"
	WeaveDomain       = "weave.local"
)

type watcher struct {
	dockerer
	networks map[string]bool
	events   chan *docker.APIEvents
}

type Watcher interface {
	WatchNetwork(uuid string)
	UnwatchNetwork(uuid string)
}

func NewWatcher(client *docker.Client) (Watcher, error) {
	w := &watcher{
		dockerer: dockerer{
			client: client,
		},
		networks: make(map[string]bool),
		events:   make(chan *docker.APIEvents),
	}
	err := client.AddEventListener(w.events)
	if err != nil {
		return nil, err
	}

	go func() {
		for event := range w.events {
			switch event.Status {
			case "start":
				w.ContainerStart(event.ID)
			case "die":
				w.ContainerDied(event.ID)
			}
		}
	}()

	return w, nil
}

func (w *watcher) WatchNetwork(uuid string) {
	Debug.Printf("Watch network %s", uuid)
	w.networks[uuid] = true
}

func (w *watcher) UnwatchNetwork(uuid string) {
	Debug.Printf("Unwatch network %s", uuid)
	delete(w.networks, uuid)
}

func (w *watcher) ContainerStart(id string) {
	Debug.Printf("Container started %s", id)
	info, err := w.InspectContainer(id)
	if err != nil {
		Warning.Printf("error inspecting container: %s", err)
		return
	}
	// FIXME: check that it's on our network; but, the docker client lib doesn't know about .NetworkID
	if info.Config.Domainname == WeaveDomain {
		// one of ours
		ip := info.NetworkSettings.IPAddress
		fqdn := fmt.Sprintf("%s.%s", info.Config.Hostname, info.Config.Domainname)
		if err := w.registerWithDNS(id, fqdn, ip); err != nil {
			Warning.Printf("unable to register with weaveDNS: %s", err)
		}
	}
}

func (w *watcher) ContainerDied(id string) {
	Debug.Printf("Container died %s", id)
	info, err := w.InspectContainer(id)
	if err != nil {
		Warning.Printf("error inspecting container: %s", err)
		return
	}
	if info.Config.Domainname == WeaveDomain {
		ip := info.NetworkSettings.IPAddress
		if err := w.deregisterWithDNS(id, ip); err != nil {
			Warning.Printf("unable to deregister with weaveDNS: %s", err)
		}
	}
}

// -----

func (watcher *watcher) registerWithDNS(ID string, fqdn string, ip string) error {
	dnsip, err := watcher.getContainerBridgeIP(WeaveDNSContainer)
	if err != nil {
		return fmt.Errorf("nameserver not available: %s", err)
	}
	data := url.Values{}
	data.Add("fqdn", fqdn)

	req, err := http.NewRequest("PUT", fmt.Sprintf("http://%s:6785/name/%s/%s", dnsip, ID, ip), strings.NewReader(data.Encode()))
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

func (watcher *watcher) deregisterWithDNS(ID string, ip string) error {
	dnsip, err := watcher.getContainerBridgeIP(WeaveDNSContainer)
	if err != nil {
		return fmt.Errorf("nameserver not available: %s", err)
	}

	req, err := http.NewRequest("DELETE", fmt.Sprintf("http://%s:6785/name/%s/%s", dnsip, ID, ip), nil)
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
