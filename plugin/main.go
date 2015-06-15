package main

import (
	"flag"
	"fmt"

	"github.com/weaveworks/docker-plugin/plugin/driver"
	. "github.com/weaveworks/weave/common"
	"os"
)

var version = "(unreleased version)"

func main() {
	var (
		justVersion bool
		address     string
		nameserver  string
		debug       bool
	)

	flag.BoolVar(&justVersion, "version", false, "print version and exit")
	flag.BoolVar(&debug, "debug", false, "output debugging info to stderr")
	flag.StringVar(&address, "socket", "/usr/share/docker/plugins/weave.sock", "socket on which to listen")
	flag.StringVar(&nameserver, "nameserver", "", "nameserver to provide to containers")

	flag.Parse()

	if justVersion {
		fmt.Printf("weave plugin %s\n", version)
		os.Exit(0)
	}

	InitDefaultLogging(debug)

	var d driver.Driver
	d, err := driver.New(version)
	if err != nil {
		Error.Fatalf("unable to create driver: %s", err)
	}

	if nameserver != "" {
		if err := d.SetNameserver(nameserver); err != nil {
			Error.Fatalf("could not set nameserver: %s", err)
		}
	}

	if err := d.Listen(address); err != nil {
		Error.Fatal(err)
	}
}
