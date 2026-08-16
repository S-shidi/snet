//go:build windows

package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"

	"virtualnet/internal/client"
)

// serviceLogPath is where the service-mode daemon writes its log. The SCM
// discards a service's stderr, so without this the reason a service failed to
// start would be completely invisible.
const serviceLogPath = `C:\ProgramData\SNET\daemon.log`

// daemonService implements svc.Handler so vnetd.exe runs as a native Windows
// service under LocalSystem. The /ctl/shutdown endpoint stops the HTTP server,
// which makes Execute return and reports SERVICE_STOPPED to the SCM — so the
// crash-recovery actions (sc failure ... restart) do not fire on a clean stop.
// A control-API bind failure instead terminates the service with a non-zero
// exit code, so the SCM's restart policy and the event log record the fault
// instead of leaving a phantom "running but not listening" service.
type daemonService struct {
	cfg          *client.Config
	configPath   string
	ctlAddr      string
	deviceIDFile string
}

func (s *daemonService) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	stop := make(chan struct{})
	failed := make(chan struct{})
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
			select {
			case <-failed:
			default:
				close(failed)
			}
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
		case <-failed:
			<-done
			return false, 1
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
		if err := setupServiceLog(); err != nil {
			log.Printf("service log: %v", err)
		}
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

// setupServiceLog redirects the daemon log to a file under the shared program
// data dir so service-mode failures (which would otherwise go to a discarded
// stderr) are captured for diagnosis.
func setupServiceLog() error {
	dir := filepath.Dir(serviceLogPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(serviceLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	log.SetOutput(f)
	return nil
}
