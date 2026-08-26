package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"snet/internal/protocol"
	"snet/internal/server"
)

func main() {
	addr := flag.String("addr", "0.0.0.0:8090", "listen address")
	probeAddr := flag.String("probe-addr", "0.0.0.0:8091", "UDP probe/echo address for public-IP discovery")
	dbPath := flag.String("db", "", "bbolt database path (empty = in-memory, not persistent)")
	adminToken := flag.String("admin-token", os.Getenv("SNET_ADMIN_TOKEN"), "admin API token (env SNET_ADMIN_TOKEN)")
	adminUser := flag.String("admin-user", os.Getenv("SNET_ADMIN_USER"), "admin login username (env SNET_ADMIN_USER)")
	adminPass := flag.String("admin-pass", os.Getenv("SNET_ADMIN_PASSWORD"), "admin login password (env SNET_ADMIN_PASSWORD)")
	zombieTTL := flag.Duration("zombie-ttl", 72*time.Hour, "delete networks idle for this long; 0 disables reaping")
	trustProxy := flag.Bool("behind-proxy", false, "read client IP from X-Forwarded-For (reverse proxy deployments)")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (PEM); enables HTTPS when set with -tls-key")
	tlsKey := flag.String("tls-key", "", "TLS key file (PEM)")
	relayHost := flag.String("relay-host", "", "public relay host (IP) advertised to peers; enables UDP relay mode")
	relayBase := flag.Int("relay-base", protocol.DefaultWGPort, "base UDP relay port (one per network)")
	relayCount := flag.Int("relay-count", 64, "number of assignable UDP relay ports")
	requireDeviceAuth := flag.Bool("require-device-auth", os.Getenv("SNET_REQUIRE_DEVICE_AUTH") == "1", "only allow devices that bound an admin-generated authorization code to create/join networks (env SNET_REQUIRE_DEVICE_AUTH=1)")
	adminReset := flag.Bool("admin-reset", false, "force-reset admin password from env/admin-pass and exit")
	flag.Parse()

	store, err := server.NewStoreAt(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if *adminReset {
		if *adminUser == "" || *adminPass == "" {
			log.Fatal("-admin-reset requires -admin-user and -admin-pass (or SNET_ADMIN_USER/SNET_ADMIN_PASSWORD env)")
		}
		if _, err := store.SetAdminPassword(*adminUser, *adminPass); err != nil {
			log.Fatalf("reset admin password: %v", err)
		}
		log.Printf("admin password reset for user %q", *adminUser)
		return
	}

	store.SetRelay(*relayHost, *relayBase, *relayCount)

	var relay *server.Relay
	if *relayHost != "" {
		relay = server.NewRelay(*relayBase, *relayCount)
		relay.SetActivityHook(store.MarkRelayActivity)
		if err := relay.Start(); err != nil {
			log.Fatalf("relay: %v", err)
		}
		defer relay.Close()
		log.Printf("relay: %d UDP ports from %d via %s", *relayCount, *relayBase, *relayHost)
	}

	if *zombieTTL > 0 {
		go func() {
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				if victims, err := store.SweepZombies(*zombieTTL); err != nil {
					log.Printf("zombie sweep: %v", err)
				} else if len(victims) > 0 {
					log.Printf("zombie sweep: removed %d idle networks: %v", len(victims), victims)
				}
			}
		}()
		log.Printf("zombie sweep: networks idle > %s will be deleted", *zombieTTL)
	}

	probe, err := server.StartProbeServer(*probeAddr)
	if err != nil {
		log.Fatalf("probe server: %v", err)
	}
	defer probe.Close()

	adminEnabled := *adminToken != "" || (*adminUser != "" && *adminPass != "")
	if !adminEnabled {
		log.Printf("admin: no credentials configured yet; initialize via the web setup page (http://<host>:%s/admin)", strings.Split(*addr, ":")[len(strings.Split(*addr, ":"))-1])
	}
	httpSrv := &http.Server{
		Addr: *addr,
		Handler: server.NewHandler(store, server.Options{
			AdminToken:        *adminToken,
			AdminUser:         *adminUser,
			AdminPass:         *adminPass,
			ZombieTTL:         *zombieTTL,
			TrustProxy:        *trustProxy,
			RequireDeviceAuth: *requireDeviceAuth,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	if *tlsCert == "" || *tlsKey == "" {
		log.Printf("warning: TLS is not configured; admin credentials would cross the network in cleartext. Consider -tls-cert/-tls-key.")
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("Snet server listening on %s (db: %s, admin: %s, require-device-auth: %v)", *addr, dbPathOrMem(*dbPath), adminState(adminEnabled), *requireDeviceAuth)
		var err error
		if *tlsCert != "" && *tlsKey != "" {
			err = httpSrv.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			err = httpSrv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			serveErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-serveErr:
		// Return normally so the deferred store/relay/probe closes still run.
		log.Fatalf("server: %v", err)
	case <-stop:
	}

	log.Printf("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func dbPathOrMem(p string) string {
	if p == "" {
		return "memory"
	}
	return p
}

func adminState(enabled bool) string {
	if !enabled {
		return "disabled"
	}
	return "enabled"
}
