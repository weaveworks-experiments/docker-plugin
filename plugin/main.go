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
		confdir     string
		nameserver  string
		debug       bool
	)

	flag.BoolVar(&justVersion, "version", false, "print version and exit")
	flag.BoolVar(&debug, "debug", false, "output debugging info to stderr")
	flag.StringVar(&address, "socket", "/var/run/docker-plugin/plugin.sock", "socket on which to listen")
	flag.StringVar(&nameserver, "nameserver", "", "nameserver to provide to containers")
	flag.StringVar(&confdir, "configdir", "/var/run/weave-plugin", "path in which to store temporary config files, e.g., resolv.conf")

	flag.Parse()

	if justVersion {
		fmt.Printf("weave plugin %s\n", version)
		os.Exit(0)
	}

	InitDefaultLogging(debug)

	var d driver.Driver
	d, err := driver.New(version, nameserver, confdir)
	if err != nil {
		Error.Fatalf("unable to create driver: %s", err)
	}

	if err := d.Listen(address); err != nil {
		Error.Fatal(err)
	}
}
