//go:build !windows

package main

import (
	"log"
	"net/http"
	"os"

	"virtualnet/internal/client"
)

// runDaemon starts the daemon in the foreground. A supervisor (launchd on
// macOS) keeps it alive; /ctl/shutdown exits with code 0 so the supervisor
// does not restart the job.
func runDaemon(cfg *client.Config, configPath, ctlAddr, deviceIDFile string) {
	if os.Geteuid() != 0 {
		log.Printf("warning: not running as root, TUN device creation will fail (tunnel networks stay in retry)")
	}
	d := client.NewDaemonAt(cfg, configPath)
	d.SetDeviceIDFile(deviceIDFile)
	if err := d.Start(); err != nil {
		log.Printf("start: %v", err)
	}

	log.Printf("vnetd daemon control API on %s", ctlAddr)
	if err := client.ServeCtl(d, ctlAddr, func(_ *http.Server) { os.Exit(0) }); err != nil {
		log.Fatal(err)
	}
}
