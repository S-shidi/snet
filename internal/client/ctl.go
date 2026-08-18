package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"snet/internal/protocol"
)

// CtlReq is the request body for create/join control endpoints.
type CtlReq struct {
	Server string `json:"server"`
	Port   int    `json:"port"`
	Link   string `json:"link"`
	Nid    string `json:"nid"`
	Code   string `json:"code"`
	CA     string `json:"ca"`
	Name   string `json:"name"`
	Subnet string `json:"subnet"`
	NodeID string `json:"nodeId"`

	ApprovalRequired *bool  `json:"approvalRequired"`
	PendingID        string `json:"pendingId"`
}

// ServeCtl exposes the local control API for snetctl and the Tauri UI.
// onShutdown is invoked (with the running server) shortly after the
// /ctl/shutdown handler has flushed its response; the caller decides how to
// terminate: a foreground daemon exits the process, while a Windows service
// stops the HTTP server so the service control manager sees a clean stop.
func ServeCtl(d *Daemon, addr string, onShutdown func(*http.Server)) error {
	var srv *http.Server
	mux := http.NewServeMux()

	mux.HandleFunc("POST /ctl/create", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		d.mu.Lock()
		prevCA := d.cfg.ServerCAPath
		d.cfg.ServerCAPath = req.CA
		d.mu.Unlock()
		approval := false
		if req.ApprovalRequired != nil {
			approval = *req.ApprovalRequired
		}
		resp, err := d.Create(req.Server, req.Port, req.Name, req.Subnet, approval)
		if err != nil {
			d.mu.Lock()
			d.cfg.ServerCAPath = prevCA
			d.mu.Unlock()
			writeCtlErr(w, 500, err)
			return
		}
		writeCtlJSON(w, 200, resp)
	})

	mux.HandleFunc("POST /ctl/join", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		nid, code := req.Nid, req.Code
		linkServer := ""
		if req.Link != "" {
			var err error
			nid, code, linkServer, err = protocol.ParseLink(req.Link)
			if err != nil {
				writeCtlErr(w, 400, err)
				return
			}
		}
		// An invite link that carries its own server address wins over the
		// request field: the join targets the network's own server.
		server := req.Server
		if linkServer != "" {
			server = linkServer
		}
		d.mu.Lock()
		prevCA := d.cfg.ServerCAPath
		d.cfg.ServerCAPath = req.CA
		d.mu.Unlock()
		resp, err := d.Join(server, req.Port, nid, code)
		if err != nil {
			d.mu.Lock()
			d.cfg.ServerCAPath = prevCA
			d.mu.Unlock()
			writeCtlErr(w, 500, err)
			return
		}
		writeCtlJSON(w, 200, resp)
	})

	mux.HandleFunc("GET /ctl/status", func(w http.ResponseWriter, r *http.Request) {
		st, err := d.Status()
		if err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		writeCtlJSON(w, 200, st)
	})

	// Bind links this device to the server using an admin-generated device
	// authorization code (custom server mode). The code is accepted once over
	// the control channel and never stored.
	mux.HandleFunc("POST /ctl/bind", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if req.Code == "" {
			writeCtlErr(w, 400, errors.New("缺少设备授权码"))
			return
		}
		if err := d.Bind(req.Server, req.CA, req.Code); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		writeCtlJSON(w, 200, map[string]string{"bound": "true"})
	})

	mux.HandleFunc("POST /ctl/leave", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if err := d.Leave(req.Nid); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/rejoin", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.Rejoin(req.Nid); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/remove", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.Remove(req.Nid); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/rename", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.UpdateSettings(req.Nid, req.Name, "", nil); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/settings", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.UpdateSettings(req.Nid, req.Name, req.Subnet, req.ApprovalRequired); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/approve", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.ApprovePending(req.Nid, req.PendingID); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/deny", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.DenyPending(req.Nid, req.PendingID); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/cancel-pending", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.CancelPending(req.PendingID); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/delete", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.DeleteNetwork(req.Nid); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/kick", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.Kick(req.Nid, req.NodeID); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/reset-code", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		code, err := d.ResetCode(req.Nid)
		if err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		writeCtlJSON(w, 200, map[string]string{"pairingCode": code})
	})

	mux.HandleFunc("GET /ctl/netinfo", func(w http.ResponseWriter, r *http.Request) {
		nid := r.URL.Query().Get("nid")
		info, err := d.Info(nid)
		if err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		writeCtlJSON(w, 200, info)
	})

	mux.HandleFunc("GET /ctl/peers", func(w http.ResponseWriter, r *http.Request) {
		nid := r.URL.Query().Get("nid")
		peers, err := d.Peers(nid)
		if err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		writeCtlJSON(w, 200, peers)
	})

	mux.HandleFunc("POST /ctl/device-id", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DeviceID string `json:"deviceId"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if req.DeviceID != "" {
			if err := d.SetDeviceID(req.DeviceID); err != nil {
				writeCtlErr(w, 500, err)
				return
			}
		}
		d.mu.Lock()
		id := d.cfg.DeviceID
		d.mu.Unlock()
		writeCtlJSON(w, 200, map[string]string{"deviceId": id})
	})

	mux.HandleFunc("POST /ctl/claim", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.Claim(req.Nid); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/shutdown", func(w http.ResponseWriter, r *http.Request) {
		// Gracefully stop the daemon: close all tunnels in memory (config on
		// disk keeps Active=true so a later start restores them), then let the
		// caller terminate the process. A foreground daemon exits with code 0
		// so launchd (KeepAlive SuccessfulExit=false) does not restart the job;
		// a Windows service stops the HTTP server so SCM records a clean
		// SERVICE_STOPPED and does not trigger crash-recovery restarts.
		d.Close()
		w.WriteHeader(204)
		go func() {
			time.Sleep(200 * time.Millisecond)
			onShutdown(srv)
		}()
	})

	srv = &http.Server{Addr: addr, Handler: mux}
	return srv.ListenAndServe()
}

func writeCtlJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeCtlErr(w http.ResponseWriter, status int, err error) {
	writeCtlJSON(w, status, map[string]string{"error": err.Error()})
}
