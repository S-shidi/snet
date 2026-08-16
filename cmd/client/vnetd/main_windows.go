//go:build windows

package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"golang.org/x/sys/windows/svc"

	"virtualnet/internal/client"
)

// daemonService implements svc.Handler so vnetd.exe runs as a native Windows
// service under LocalSystem. The /ctl/shutdown endpoint stops the HTTP server,
// which makes Execute return and reports SERVICE_STOPPED to the SCM — so the
// crash-recovery actions (sc failure ... restart) do not fire on a clean stop.
type daemonService struct {
	cfg          *client.Config
	configPath   string
	ctlAddr      string
	deviceIDFile string
}

func (s *daemonService) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		d := client.NewDaemonAt(s.cfg, s.configPath)
		d.SetDeviceIDFile(s.deviceIDFile)
		if err := d.Start(); err != nil {
			log.Printf("start: %v", err)
		}
		log.Printf("vnetd daemon control API on %s", s.ctlAddr)
		if err := client.ServeCtl(d, s.ctlAddr, func(srv *http.Server) {
			_ = srv.Shutdown(context.Background())
			select {
			case <-stop:
			default:
				close(stop)
			}
		}); err != nil && err != http.ErrServerClosed {
			log.Printf("serve ctl: %v", err)
		}
	}()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				<-done
				return false, 0
			}
		case <-stop:
			<-done
			return false, 0
		}
	}
}

// runDaemon starts the daemon as a Windows service when launched by the
// service control manager, and as a plain foreground process otherwise (useful
// for debugging under an elevated console).
func runDaemon(cfg *client.Config, configPath, ctlAddr, deviceIDFile string) {
	if isService, err := svc.IsWindowsService(); err == nil && isService {
		if err := svc.Run("vnetd", &daemonService{cfg: cfg, configPath: configPath, ctlAddr: ctlAddr, deviceIDFile: deviceIDFile}); err != nil {
			log.Fatalf("service run: %v", err)
		}
		return
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
