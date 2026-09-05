package client

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"snet/internal/protocol"
)

// shutdownGraceDelay is the pause between flushing the shutdown HTTP
// response and actually stopping the server.  Tests may override it.
var shutdownGraceDelay = 200 * time.Millisecond

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

	ApprovalRequired *bool    `json:"approvalRequired"`
	PendingID        string   `json:"pendingId"`
	Subnets          []string `json:"subnets"`
}

// ServeCtl exposes the local control API for snetctl and the Tauri UI.
// The listener is bound to 127.0.0.1 only (no network exposure).
// ctlToken is a shared secret written by the daemon to a 0644 file (readable
// by the desktop GUI which runs as a different privilege domain than the root
// / LocalSystem daemon) and read by all clients; every operational /ctl/*
// request (except /ctl/auth/*) must carry it via the X-Ctl-Token header. Auth
// endpoints (/ctl/auth/*) are protected by their own session mechanism.
// onShutdown is invoked (with the running server) shortly after the
// /ctl/shutdown handler has flushed its response; the caller decides how to
// terminate: a foreground daemon exits the process, while a Windows service
// stops the HTTP server so the service control manager sees a clean stop.
func ServeCtl(d *Daemon, ctlToken, addr string, onShutdown func(*http.Server)) error {
	var srv *http.Server
	mux := http.NewServeMux()

	// ctlAuthMiddleware gates all /ctl/* requests (except /ctl/auth/*) with
	// a shared-secret token so local unprivileged processes cannot manipulate the
	// VPN without possessing the secret.
	ctlAuthMux := http.NewServeMux()
	ctlAuthMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/ctl/auth/") {
			mux.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("X-Ctl-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(ctlToken)) != 1 {
			writeCtlErr(w, http.StatusUnauthorized, errors.New("missing or invalid ctl token"))
			return
		}
		mux.ServeHTTP(w, r)
	})

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
			nid, code, linkServer, _, err = protocol.ParseLink(req.Link)
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

	mux.HandleFunc("POST /ctl/subnets", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.UpdateSubnets(req.Nid, req.Subnets); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
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

	mux.HandleFunc("GET /ctl/local-subnets", func(w http.ResponseWriter, r *http.Request) {
		subnets := d.DetectLocalSubnets()
		writeCtlJSON(w, 200, map[string]any{"subnets": subnets})
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
			time.Sleep(shutdownGraceDelay)
			onShutdown(srv)
		}()
	})

	// ── Auth endpoints ──────────────────────────────────────────────
	auth := &authManager{d: d}
	mux.HandleFunc("POST /ctl/auth/login", auth.handleLogin)
	mux.HandleFunc("POST /ctl/auth/logout", auth.handleLogout)
	mux.HandleFunc("GET /ctl/auth/check", auth.handleCheck)
	mux.HandleFunc("POST /ctl/auth/password", auth.handlePassword)

	srv = &http.Server{Addr: addr, Handler: ctlAuthMux}
	return srv.ListenAndServe()
}

// authManager handles web console authentication with bcrypt passwords
// and in-memory session tokens.
type authManager struct {
	d        *Daemon
	mu       sync.Mutex
	sessions map[string]time.Time // token -> expiry
}

func (a *authManager) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeCtlErr(w, 400, err)
		return
	}
	a.d.mu.Lock()
	hash := a.d.cfg.WebPasswordHash
	a.d.mu.Unlock()
	if hash == "" {
		writeCtlErr(w, 400, errors.New("未设置密码"))
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		writeCtlErr(w, 401, errors.New("密码错误"))
		return
	}
	token, err := generateToken(32)
	if err != nil {
		writeCtlErr(w, 500, err)
		return
	}
	a.mu.Lock()
	if a.sessions == nil {
		a.sessions = make(map[string]time.Time)
	}
	expiry := time.Now().Add(24 * time.Hour)
	if req.Remember {
		expiry = time.Now().Add(30 * 24 * time.Hour)
	}
	a.sessions[token] = expiry
	a.mu.Unlock()
	writeCtlJSON(w, 200, map[string]string{"token": token})
}

func (a *authManager) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r)
	if token != "" {
		a.mu.Lock()
		delete(a.sessions, token)
		a.mu.Unlock()
	}
	w.WriteHeader(204)
}

func (a *authManager) handleCheck(w http.ResponseWriter, r *http.Request) {
	a.d.mu.Lock()
	hasPassword := a.d.cfg.WebPasswordHash != ""
	a.d.mu.Unlock()
	if !hasPassword {
		writeCtlJSON(w, 200, map[string]any{"hasPassword": false, "authenticated": true})
		return
	}
	token := extractToken(r)
	a.mu.Lock()
	expiry, ok := a.sessions[token]
	a.mu.Unlock()
	authenticated := ok && time.Now().Before(expiry)
	writeCtlJSON(w, 200, map[string]any{"hasPassword": true, "authenticated": authenticated})
}

func (a *authManager) handlePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeCtlErr(w, 400, err)
		return
	}
	if len(req.New) < 8 {
		writeCtlErr(w, 400, errors.New("密码至少需要8个字符"))
		return
	}
	a.d.mu.Lock()
	hasPassword := a.d.cfg.WebPasswordHash != ""
	a.d.mu.Unlock()

	if hasPassword {
		token := extractToken(r)
		a.mu.Lock()
		_, ok := a.sessions[token]
		a.mu.Unlock()
		if !ok {
			writeCtlErr(w, 401, errors.New("未登录"))
			return
		}
		a.d.mu.Lock()
		if err := bcrypt.CompareHashAndPassword([]byte(a.d.cfg.WebPasswordHash), []byte(req.Current)); err != nil {
			a.d.mu.Unlock()
			writeCtlErr(w, 401, errors.New("当前密码错误"))
			return
		}
		a.d.mu.Unlock()
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.New), bcrypt.DefaultCost)
	if err != nil {
		writeCtlErr(w, 500, err)
		return
	}
	a.d.mu.Lock()
	a.d.cfg.WebPasswordHash = string(hash)
	a.d.mu.Unlock()
	if err := a.d.SaveConfig(); err != nil {
		writeCtlErr(w, 500, err)
		return
	}
	if hasPassword {
		token := extractToken(r)
		a.mu.Lock()
		a.sessions = map[string]time.Time{token: a.sessions[token]}
		a.mu.Unlock()
	}
	w.WriteHeader(204)
}

func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func generateToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeCtlJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeCtlErr(w http.ResponseWriter, status int, err error) {
	writeCtlJSON(w, status, map[string]string{"error": err.Error()})
}
