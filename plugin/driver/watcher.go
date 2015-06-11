package driver

import (
	"github.com/fsouza/go-dockerclient"
	. "github.com/weaveworks/weave/common"
)

type watcher struct {
	client   *docker.Client
	networks map[string]bool
	events   chan *docker.APIEvents
}

type Watcher interface {
	WatchNetwork(uuid string)
	UnwatchNetwork(uuid string)
}

func NewWatcher(client *docker.Client) (Watcher, error) {
	w := &watcher{
		client:   client,
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
	w.networks[uuid] = true
}

func (w *watcher) UnwatchNetwork(uuid string) {
	delete(w.networks, uuid)
}

func (w *watcher) ContainerStart(id string) {
	Debug.Printf("Container started %s", id)
}

func (w *watcher) ContainerDied(id string) {
	Debug.Printf("Container died %s", id)
}
