package server

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"virtualnet/internal/protocol"
)

//go:embed admin.html
var adminPage embed.FS

const maxBodyBytes = 64 << 10

// Options configure the HTTP handler. Zero values provide sensible defaults.
type Options struct {
	AdminToken string        // static bearer token; if set alongside creds, both work
	AdminUser  string        // session-login username (requires AdminPass)
	AdminPass  string        // session-login password (requires AdminUser)
	ZombieTTL  time.Duration // networks idle longer than this are considered zombies (admin view)
	TrustProxy bool          // read X-Forwarded-For when behind a reverse proxy

	// CreatePerHour and JoinPerMinute rate-limit the two expensive endpoints
	// per client IP. Zero values use generous self-hosted defaults.
	CreatePerHour int
	JoinPerMinute int

	// RequireDeviceAuth gates create/join/device-registration behind a device
	// authorization code bound via POST /api/v1/devices/bind. Off by default
	// for compatibility with existing deployments; enable it on servers that
	// want to control exactly which devices may join.
	RequireDeviceAuth bool
}

const (
	defaultCreatePerHour = 50
	defaultJoinPerMinute = 60
	defaultBindPerMinute = 20
	sessionTTL           = 24 * time.Hour
)

type sessionEntry struct {
	expires time.Time
}

type handler struct {
	s       *Store
	opts    Options
	creates *rateLimiter
	joins   *rateLimiter
	binds   *rateLimiter
	logins  *rateLimiter

	// adminPassHash is the active bcrypt hash of the admin login password. It
	// is seeded from opts.AdminPass on first start and can be rotated via
	// POST /admin/password.
	adminPassHash string

	sessMu   sync.Mutex
	sessions map[string]sessionEntry // key = SHA-256 hex of session token
}

// NewHandler builds the full HTTP API. The admin surface is enabled when an
// admin token or admin credentials are configured; otherwise admin routes and
// the management page return 404.
func NewHandler(s *Store, opts Options) http.Handler {
	createPerHour := opts.CreatePerHour
	if createPerHour == 0 {
		createPerHour = defaultCreatePerHour
	}
	joinPerMinute := opts.JoinPerMinute
	if joinPerMinute == 0 {
		joinPerMinute = defaultJoinPerMinute
	}
	h := &handler{
		s:        s,
		opts:     opts,
		creates:  newRateLimiter(createPerHour, time.Hour),
		joins:    newRateLimiter(joinPerMinute, time.Minute),
		binds:    newRateLimiter(defaultBindPerMinute, time.Minute),
		logins:   newRateLimiter(5, time.Minute),
		sessions: make(map[string]sessionEntry),
	}
	if opts.RequireDeviceAuth {
		s.SetRequireDeviceAuth(true)
	}
	if opts.AdminUser != "" && opts.AdminPass != "" {
		if hash, err := s.EnsureAdminPassword(opts.AdminUser, opts.AdminPass); err != nil {
			log.Printf("admin: seed password: %v", err)
		} else {
			h.adminPassHash = hash
		}
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /api/v1/networks", func(w http.ResponseWriter, r *http.Request) {
		if h.creates.blocked(h.clientIP(r)) {
			writeErr(w, http.StatusTooManyRequests, errors.New("too many requests"))
			return
		}
		var req protocol.CreateNetworkReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.CreateNetwork(req.PublicKey, req.DeviceID, req.Name, req.Subnet, req.ApprovalRequired)
		if err != nil {
			if writeEnrollmentErr(w, err) {
				return
			}
			handleStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, resp)
	})

	mux.HandleFunc("POST /api/v1/networks/{nid}/join", func(w http.ResponseWriter, r *http.Request) {
		if h.joins.blocked(h.clientIP(r)) {
			writeErr(w, http.StatusTooManyRequests, errors.New("too many requests"))
			return
		}
		nid := r.PathValue("nid")
		var req protocol.JoinReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.Join(nid, req.Code, req.PublicKey, req.DeviceID)
		switch {
		case writeEnrollmentErr(w, err):
		case errors.Is(err, ErrNotFound):
			writeErr(w, http.StatusNotFound, err)
		case errors.Is(err, ErrCodeLocked):
			writeErr(w, http.StatusTooManyRequests, err)
		case errors.Is(err, ErrCodeInvalid):
			writeErr(w, http.StatusUnauthorized, err)
		case errors.Is(err, ErrNetworkFull):
			writeErr(w, http.StatusConflict, err)
		case err != nil:
			writeErr(w, http.StatusInternalServerError, err)
		default:
			writeJSON(w, http.StatusOK, resp)
		}
	})

	mux.HandleFunc("POST /api/v1/devices", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.RegisterDeviceReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.RegisterDevice(req.DeviceID, req.PublicKey); err != nil {
			if writeEnrollmentErr(w, err) {
				return
			}
			handleStoreErr(w, err)
			return
		}
		// Report the device's binding status so bound clients can detect an
		// admin revocation even when the enrollment gate is off.
		bound := s.DeviceBound(req.DeviceID)
		writeJSON(w, http.StatusOK, protocol.RegisterDeviceResp{DeviceID: req.DeviceID, Bound: &bound})
	})

	// Bind consumes a device authorization code, permanently linking the
	// presented device ID (and its WireGuard public key) to this server. No
	// bearer token is required: the code itself is the credential. It is rate
	// limited per IP to deter brute force (codes carry ≈80 bits of entropy).
	mux.HandleFunc("POST /api/v1/devices/bind", func(w http.ResponseWriter, r *http.Request) {
		if h.binds.blocked(h.clientIP(r)) {
			writeErr(w, http.StatusTooManyRequests, errors.New("too many requests"))
			return
		}
		var req protocol.BindDeviceReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if req.Code == "" {
			writeErr(w, http.StatusBadRequest, errors.New("缺少设备授权码"))
			return
		}
		if err := s.BindDevice(req.Code, req.DeviceID, req.PublicKey); err != nil {
			handleStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, protocol.BindDeviceResp{DeviceID: req.DeviceID})
	})

	mux.HandleFunc("PUT /api/v1/networks/{nid}/nodes/{nodeID}/endpoint", requireToken(func(w http.ResponseWriter, r *http.Request) {
		var req protocol.SetEndpointReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.SetEndpoint(tokenOf(r), req.Endpoint); err != nil {
			handleStoreErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	mux.HandleFunc("GET /api/v1/networks/{nid}/peers", requireToken(func(w http.ResponseWriter, r *http.Request) {
		peers, err := s.ListPeers(tokenOf(r))
		if err != nil {
			handleStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, peers)
	}))

	mux.HandleFunc("DELETE /api/v1/networks/{nid}/nodes/{nodeID}", requireToken(func(w http.ResponseWriter, r *http.Request) {
		if err := s.RemoveNode(tokenOf(r)); err != nil {
			handleStoreErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	mux.HandleFunc("POST /api/v1/networks/{nid}/nodes/{nodeID}/device", requireToken(func(w http.ResponseWriter, r *http.Request) {
		var req protocol.SetNodeDeviceReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.SetNodeDevice(tokenOf(r), r.PathValue("nodeID"), req.DeviceID); err != nil {
			handleStoreErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	// ---- owner surface (token = a node of the network; token's network must
	// match the path) ----
	ownerNID := func(w http.ResponseWriter, r *http.Request) bool {
		netID, err := s.NodeNetwork(tokenOf(r))
		if err != nil || netID != r.PathValue("nid") {
			writeErr(w, http.StatusNotFound, ErrNotFound)
			return false
		}
		return true
	}

	mux.HandleFunc("GET /api/v1/networks/{nid}", requireToken(func(w http.ResponseWriter, r *http.Request) {
		if !ownerNID(w, r) {
			return
		}
		info, err := s.NetworkInfo(tokenOf(r))
		if err != nil {
			handleStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, info)
	}))

	mux.HandleFunc("PATCH /api/v1/networks/{nid}", requireToken(func(w http.ResponseWriter, r *http.Request) {
		if !ownerNID(w, r) {
			return
		}
		var req protocol.NetworkSettingsReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.UpdateNetworkSettings(tokenOf(r), req.Name, req.Subnet, req.ApprovalRequired); err != nil {
			handleStoreErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	// Public pending-join status: the joining client polls this until the
	// owner approves/denies its request (or it expires).
	mux.HandleFunc("GET /api/v1/pending/{pendingID}", func(w http.ResponseWriter, r *http.Request) {
		status, err := s.PendingStatus(r.PathValue("pendingID"))
		if err != nil {
			handleStoreErr(w, err)
			return
		}
		if status.Status == "approved" {
			// One-shot credentials: hand them out once, then drop the record.
			s.ConsumePending(r.PathValue("pendingID"))
		}
		writeJSON(w, http.StatusOK, status)
	})

	mux.HandleFunc("POST /api/v1/networks/{nid}/pending/{pendingID}/approve", requireToken(func(w http.ResponseWriter, r *http.Request) {
		if !ownerNID(w, r) {
			return
		}
		status, err := s.OwnerApprove(tokenOf(r), r.PathValue("pendingID"))
		if err != nil {
			handleStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
	}))

	mux.HandleFunc("POST /api/v1/networks/{nid}/pending/{pendingID}/deny", requireToken(func(w http.ResponseWriter, r *http.Request) {
		if !ownerNID(w, r) {
			return
		}
		if err := s.OwnerDeny(tokenOf(r), r.PathValue("pendingID")); err != nil {
			handleStoreErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	mux.HandleFunc("POST /api/v1/networks/{nid}/code", requireToken(func(w http.ResponseWriter, r *http.Request) {
		if !ownerNID(w, r) {
			return
		}
		code, err := s.ResetCode(tokenOf(r))
		if err != nil {
			handleStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, protocol.ResetCodeResp{PairingCode: code})
	}))

	mux.HandleFunc("DELETE /api/v1/networks/{nid}/members/{nodeID}", requireToken(func(w http.ResponseWriter, r *http.Request) {
		if !ownerNID(w, r) {
			return
		}
		if err := s.KickNode(tokenOf(r), r.PathValue("nodeID")); err != nil {
			handleStoreErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	mux.HandleFunc("DELETE /api/v1/networks/{nid}", requireToken(func(w http.ResponseWriter, r *http.Request) {
		if !ownerNID(w, r) {
			return
		}
		if err := s.DeleteNetwork(tokenOf(r)); err != nil {
			handleStoreErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	mux.HandleFunc("POST /api/v1/networks/{nid}/claim", requireToken(func(w http.ResponseWriter, r *http.Request) {
		var req protocol.ClaimReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.ClaimNetwork(r.PathValue("nid"), tokenOf(r), req.DeviceID); err != nil {
			handleStoreErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	// ---- admin surface ----
	if h.adminEnabled() {
		mux.HandleFunc("POST /admin/login", h.adminLogin)
		mux.HandleFunc("POST /admin/password", h.requireAdmin(h.adminPasswordChange))
		mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			data, _ := adminPage.ReadFile("admin.html")
			_, _ = w.Write(data)
		})
		mux.HandleFunc("GET /admin/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin", http.StatusMovedPermanently)
		})
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin", http.StatusMovedPermanently)
		})
		mux.HandleFunc("POST /admin/networks", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			var req protocol.AdminCreateNetworkReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			resp, err := s.AdminCreateNetwork(req.Name, req.Subnet, req.ApprovalRequired)
			if err != nil {
				handleStoreErr(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, resp)
		}))
		mux.HandleFunc("GET /admin/networks", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, s.AdminNetworks(h.opts.ZombieTTL))
		}))
		mux.HandleFunc("GET /admin/networks/{nid}", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			info, err := s.AdminNetworkInfo(r.PathValue("nid"))
			if err != nil {
				handleStoreErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, info)
		}))
		mux.HandleFunc("PATCH /admin/networks/{nid}", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			var req protocol.NetworkSettingsReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			if err := s.AdminUpdateNetwork(r.PathValue("nid"), req.Name, req.Subnet, req.ApprovalRequired); err != nil {
				handleStoreErr(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		mux.HandleFunc("GET /admin/networks/{nid}/pending", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			pending, err := s.AdminPending(r.PathValue("nid"))
			if err != nil {
				handleStoreErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, pending)
		}))
		mux.HandleFunc("POST /admin/networks/{nid}/pending/{pid}/approve", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			status, err := s.AdminApprove(r.PathValue("nid"), r.PathValue("pid"))
			if err != nil {
				handleStoreErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, status)
		}))
		mux.HandleFunc("POST /admin/networks/{nid}/pending/{pid}/deny", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			if err := s.AdminDeny(r.PathValue("nid"), r.PathValue("pid")); err != nil {
				handleStoreErr(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		mux.HandleFunc("GET /admin/networks/{nid}/nodes", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			nodes, err := s.AdminNodes(r.PathValue("nid"))
			if err != nil {
				handleStoreErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, nodes)
		}))
		mux.HandleFunc("DELETE /admin/networks/{nid}/nodes/{nodeID}", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			if err := s.AdminRemoveNode(r.PathValue("nid"), r.PathValue("nodeID")); err != nil {
				handleStoreErr(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		mux.HandleFunc("POST /admin/networks/{nid}/code", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			code, err := s.AdminResetCode(r.PathValue("nid"))
			if err != nil {
				handleStoreErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"code": code})
		}))
		mux.HandleFunc("POST /admin/networks/{nid}/external-node", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				PublicKey string `json:"publicKey"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PublicKey == "" {
				writeErr(w, http.StatusBadRequest, errors.New("bad request"))
				return
			}
			nodeID, ip, err := s.AdminAddExternalNode(r.PathValue("nid"), req.PublicKey)
			if err != nil {
				handleStoreErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"nodeId": nodeID, "ip": ip})
		}))
		mux.HandleFunc("DELETE /admin/networks/{nid}", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			if err := s.AdminDeleteNetwork(r.PathValue("nid")); err != nil {
				handleStoreErr(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		mux.HandleFunc("GET /admin/devices", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, protocol.AdminDevicesResp{Devices: s.AdminDevices()})
		}))
		mux.HandleFunc("GET /admin/devices/{deviceID}/networks", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, protocol.NetworksResp{Networks: s.DeviceNetworks(r.PathValue("deviceID"))})
		}))
		mux.HandleFunc("POST /admin/devices/authcodes/generate", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			var req protocol.AdminGenerateAuthCodesReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			if req.Count < 1 || req.Count > 100 {
				writeErr(w, http.StatusBadRequest, errors.New("count must be between 1 and 100"))
				return
			}
			if req.MaxBindings == 0 {
				req.MaxBindings = 1 // default: one device per code
			}
			if req.MaxBindings < 1 || req.MaxBindings > 100 {
				writeErr(w, http.StatusBadRequest, errors.New("maxBindings must be between 1 and 100"))
				return
			}
			codes, ids, err := s.AdminGenerateAuthCodes(req.Count, req.MaxBindings)
			if err != nil {
				handleStoreErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, protocol.AdminGenerateAuthCodesResp{Codes: codes, IDs: ids})
		}))
		mux.HandleFunc("GET /admin/devices/authcodes", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, protocol.AdminAuthCodesResp{Codes: s.AdminAuthCodes()})
		}))
		mux.HandleFunc("POST /admin/devices/authcodes/revoke", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				ID string `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
				writeErr(w, http.StatusBadRequest, errors.New("bad request"))
				return
			}
			if err := s.AdminRevokeAuthCode(req.ID); err != nil {
				handleStoreErr(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		mux.HandleFunc("POST /admin/devices/authcodes/unbind", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				DeviceID string `json:"deviceId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeviceID == "" {
				writeErr(w, http.StatusBadRequest, errors.New("bad request"))
				return
			}
			if err := s.AdminUnbindDevice(req.DeviceID); err != nil {
				handleStoreErr(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
	}

	return logRequests(limitBody(mux))
}

func (h *handler) adminEnabled() bool {
	return h.opts.AdminToken != "" || (h.opts.AdminUser != "" && h.opts.AdminPass != "")
}

func (h *handler) adminLogin(w http.ResponseWriter, r *http.Request) {
	if h.opts.AdminUser == "" || h.opts.AdminPass == "" {
		writeErr(w, http.StatusNotFound, ErrNotFound)
		return
	}
	if h.logins.blocked(h.clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, errors.New("too many attempts"))
		return
	}
	var req protocol.AdminLoginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(h.opts.AdminUser)) == 1
	passOK := h.adminPassHash != "" && bcrypt.CompareHashAndPassword([]byte(h.adminPassHash), []byte(req.Password)) == nil
	if !userOK || !passOK {
		writeErr(w, http.StatusUnauthorized, ErrUnauthorized)
		return
	}
	tok, err := randomToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	expires := time.Now().Add(sessionTTL)
	h.sessMu.Lock()
	h.sessions[hashToken(tok)] = sessionEntry{expires: expires}
	h.gcSessionsLocked()
	h.sessMu.Unlock()
	writeJSON(w, http.StatusOK, protocol.AdminLoginResp{Token: tok, Expires: expires.Unix()})
}

// adminPasswordChange rotates the admin login password. It verifies the old
// password, persists the new bcrypt hash and invalidates every existing admin
// session so all clients must log in again.
func (h *handler) adminPasswordChange(w http.ResponseWriter, r *http.Request) {
	if h.opts.AdminUser == "" || h.adminPassHash == "" {
		writeErr(w, http.StatusNotFound, ErrNotFound)
		return
	}
	var req protocol.AdminPasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.OldPassword == "" || req.NewPassword == "" {
		writeErr(w, http.StatusBadRequest, errors.New("缺少旧密码或新密码"))
		return
	}
	if len(req.NewPassword) < 8 {
		writeErr(w, http.StatusBadRequest, errors.New("新密码至少 8 位"))
		return
	}
	if req.NewPassword == req.OldPassword {
		writeErr(w, http.StatusBadRequest, errors.New("新密码不能与旧密码相同"))
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(h.adminPassHash), []byte(req.OldPassword)) != nil {
		writeErr(w, http.StatusUnauthorized, errors.New("旧密码不正确"))
		return
	}
	newHash, err := h.s.SetAdminPassword(h.opts.AdminUser, req.NewPassword)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	h.adminPassHash = newHash
	h.sessMu.Lock()
	h.sessions = make(map[string]sessionEntry)
	h.sessMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) sessionValid(tok string) bool {
	if tok == "" {
		return false
	}
	h.sessMu.Lock()
	defer h.sessMu.Unlock()
	e, ok := h.sessions[hashToken(tok)]
	if !ok {
		return false
	}
	if time.Now().After(e.expires) {
		delete(h.sessions, hashToken(tok))
		return false
	}
	return true
}

func (h *handler) gcSessionsLocked() {
	now := time.Now()
	for tok, e := range h.sessions {
		if now.After(e.expires) {
			delete(h.sessions, tok)
		}
	}
}

func (h *handler) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := tokenOf(r)
		static := h.opts.AdminToken != "" && subtle.ConstantTimeCompare([]byte(got), []byte(h.opts.AdminToken)) == 1
		if static || h.sessionValid(got) {
			next(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, ErrUnauthorized)
	}
}

func requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if tokenOf(r) == "" {
			writeErr(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		next(w, r)
	}
}

func tokenOf(r *http.Request) string {
	h := r.Header.Get("Authorization")
	return strings.TrimPrefix(h, "Bearer ")
}

// limitBody caps request bodies to maxBodyBytes.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// rateLimiter is a per-IP fixed-window limiter.
type rateLimiter struct {
	mu    sync.Mutex
	limit int
	win   time.Duration
	seen  map[string]*windowCount
}

type windowCount struct {
	count int
	reset time.Time
}

func newRateLimiter(limit int, win time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, win: win, seen: make(map[string]*windowCount)}
}

// blocked reports whether the IP has exceeded the window limit.
func (rl *rateLimiter) blocked(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	wc, ok := rl.seen[ip]
	if !ok || now.After(wc.reset) {
		rl.seen[ip] = &windowCount{count: 1, reset: now.Add(rl.win)}
		return false
	}
	wc.count++
	return wc.count > rl.limit
}

func (h *handler) clientIP(r *http.Request) string {
	if h.opts.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.Index(xff, ","); i > 0 {
				xff = xff[:i]
			}
			if ip := strings.TrimSpace(xff); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func handleStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnauthorized):
		writeErr(w, http.StatusUnauthorized, err)
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, err)
	case errors.Is(err, ErrManagedNetwork):
		writeErr(w, http.StatusForbidden, err)
	case errors.Is(err, ErrAuthCodeInvalid):
		writeErr(w, http.StatusNotFound, errors.New("无效的设备授权码"))
	case errors.Is(err, ErrAuthCodeUsed):
		writeErr(w, http.StatusConflict, errors.New("设备授权码已被其他设备使用"))
	case errors.Is(err, ErrAuthCodeFull):
		writeErr(w, http.StatusConflict, errors.New("设备授权码可绑定的设备数已满"))
	default:
		writeErr(w, http.StatusInternalServerError, err)
	}
}

// writeEnrollmentErr renders the "device not authorized" response for the
// create/join/register gate when RequireDeviceAuth is on. It reports whether
// the error was the enrollment gate so callers can fall through to
// handleStoreErr otherwise.
func writeEnrollmentErr(w http.ResponseWriter, err error) bool {
	if errors.Is(err, ErrUnauthorized) {
		writeErr(w, http.StatusForbidden, errors.New("设备未授权：请先在设置中绑定本服务器（需设备授权码）"))
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, protocol.ErrResp{Error: err.Error()})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s from %s", r.Method, r.URL.Path, clientIPOf(r))
		next.ServeHTTP(w, r)
	})
}

func clientIPOf(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
