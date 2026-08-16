package main

import (
	"flag"
	"log"

	"virtualnet/internal/client"
)

func main() {
	ctlAddr := flag.String("ctl", "127.0.0.1:19432", "local control API address")
	configPath := flag.String("config", "", "config file path (default: user config dir)")
	deviceIDFile := flag.String("device-id-file", client.DefaultDeviceIDFile, "path to the stable device identity file (empty to disable)")
	flag.Parse()

	cfg, err := client.LoadConfigAt(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	runDaemon(cfg, *configPath, *ctlAddr, *deviceIDFile)
}
