package server

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"snet/internal/protocol"
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
	defaultCreatePerHour  = 50
	defaultJoinPerMinute  = 60
	defaultBindPerMinute  = 20
	defaultRegisterPerMin = 30
	sessionTTL            = 24 * time.Hour
	limiterPruneInterval  = time.Minute
)

type sessionEntry struct {
	expires time.Time
}

type handler struct {
	s         *Store
	opts      Options
	creates   *rateLimiter
	joins     *rateLimiter
	binds     *rateLimiter
	logins    *rateLimiter
	registers *rateLimiter

	// adminMu guards adminUser and adminPassHash, which are rotated while
	// the server is serving (bootstrap wizard, password change).
	adminMu       sync.RWMutex
	adminUser     string // active session-login username (see field docs below)
	adminPassHash string // active bcrypt hash of the admin login password

	// bootstrapMu serializes first-run account creation so two simultaneous
	// wizard submissions cannot both pass the availability check.
	bootstrapMu sync.Mutex

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
		s:         s,
		opts:      opts,
		creates:   newRateLimiter(createPerHour, time.Hour),
		joins:     newRateLimiter(joinPerMinute, time.Minute),
		binds:     newRateLimiter(defaultBindPerMinute, time.Minute),
		logins:    newRateLimiter(5, time.Minute),
		registers: newRateLimiter(defaultRegisterPerMin, time.Minute),
		sessions:  make(map[string]sessionEntry),
	}
	if opts.RequireDeviceAuth {
		s.SetRequireDeviceAuth(true)
	}
	if opts.AdminUser != "" && opts.AdminPass != "" {
		if hash, err := s.EnsureAdminPassword(opts.AdminUser, opts.AdminPass); err != nil {
			log.Printf("admin: seed password: %v", err)
		} else {
			h.adminMu.Lock()
			h.adminUser = opts.AdminUser
			h.adminPassHash = hash
			h.adminMu.Unlock()
		}
	} else if users := s.AdminUsernames(); len(users) > 0 {
		// Restart without flags: reload the previously bootstrapped/configured
		// account so login keeps working.
		h.adminMu.Lock()
		h.adminUser = users[0]
		h.adminPassHash = s.adminPasswordHash(users[0])
		h.adminMu.Unlock()
	}
	// Periodically drop expired limiter windows so per-IP maps cannot grow
	// without bound from scanning sources.
	go func() {
		t := time.NewTicker(limiterPruneInterval)
		defer t.Stop()
		for range t.C {
			for _, rl := range []*rateLimiter{h.creates, h.joins, h.binds, h.logins, h.registers} {
				rl.prune()
			}
		}
	}()
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
			writeErr(w, http.StatusBadRequest, ErrBadJSON)
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
			writeErr(w, http.StatusBadRequest, ErrBadJSON)
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
			writeInternalErr(w, err)
		default:
			writeJSON(w, http.StatusOK, resp)
		}
	})

	mux.HandleFunc("POST /api/v1/devices", func(w http.ResponseWriter, r *http.Request) {
		// Unauthenticated endpoint that persists a record per unique deviceId:
		// rate limit per IP to deter store-growth abuse.
		if h.registers.blocked(h.clientIP(r)) {
			writeErr(w, http.StatusTooManyRequests, errors.New("too many requests"))
			return
		}
		var req protocol.RegisterDeviceReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, ErrBadJSON)
			return
		}
		if err := s.RegisterDevice(req.DeviceID, req.PublicKey, req.Name); err != nil {
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
			writeErr(w, http.StatusBadRequest, ErrBadJSON)
			return
		}
		if req.Code == "" {
			writeErr(w, http.StatusBadRequest, errors.New("缺少设备授权码"))
			return
		}
		deviceToken, err := s.BindDevice(req.Code, req.DeviceID, req.PublicKey, req.Name)
		if err != nil {
			handleStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, protocol.BindDeviceResp{DeviceID: req.DeviceID, DeviceToken: deviceToken})
	})

	// Device-scoped network listing: returns all networks the device holds a
	// node in, including per-node credentials for config reconstruction after
	// a reinstall. Authenticated by deviceToken (not a node token).
	mux.HandleFunc("GET /api/v1/devices/{deviceId}/networks", func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("deviceId")
		deviceToken := deviceTokenOf(r)
		if deviceID == "" || deviceToken == "" {
			writeErr(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		if !s.ValidateDeviceToken(deviceID, deviceToken) {
			writeErr(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		details, err := s.DeviceNetworkDetails(deviceID)
		if err != nil {
			handleStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, protocol.DeviceNetworksResp{Networks: details})
	})

	// Update a node's WireGuard public key. Used after a client reinstall
	// when the device generates a new keypair. Authenticated by deviceToken.
	mux.HandleFunc("POST /api/v1/networks/{nid}/nodes/{nodeID}/publickey", func(w http.ResponseWriter, r *http.Request) {
		nid := r.PathValue("nid")
		nodeID := r.PathValue("nodeID")
		deviceToken := deviceTokenOf(r)
		if deviceToken == "" {
			writeErr(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		var req protocol.UpdateNodePublicKeyReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, ErrBadJSON)
			return
		}
		if req.DeviceID == "" || req.PublicKey == "" {
			writeErr(w, http.StatusBadRequest, errors.New("缺少deviceId或publicKey"))
			return
		}
		if !s.ValidateDeviceToken(req.DeviceID, deviceToken) {
			writeErr(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		if err := s.UpdateNodePublicKey(req.DeviceID, nid, nodeID, req.PublicKey); err != nil {
			handleStoreErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("PUT /api/v1/networks/{nid}/nodes/{nodeID}/endpoint", requireToken(func(w http.ResponseWriter, r *http.Request) {
		var req protocol.SetEndpointReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, ErrBadJSON)
			return
		}
		if err := s.SetEndpoint(tokenOf(r), req.Endpoint, req.LocalEndpoint); err != nil {
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

	mux.HandleFunc("PATCH /api/v1/networks/{nid}/subnets", requireToken(func(w http.ResponseWriter, r *http.Request) {
		var req protocol.UpdateSubnetsReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, ErrBadJSON)
			return
		}
		token := tokenOf(r)
		if err := s.UpdateAllowedSubnets(token, req.Subnets); err != nil {
			handleStoreErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
			writeErr(w, http.StatusBadRequest, ErrBadJSON)
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

	mux.HandleFunc("GET /api/v1/networks/{nid}/exists", requireToken(func(w http.ResponseWriter, r *http.Request) {
		nid := r.PathValue("nid")
		netID, err := s.NodeNetwork(tokenOf(r))
		if err != nil || netID != nid {
			writeErr(w, http.StatusNotFound, ErrNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"exists": true})
	}))

	mux.HandleFunc("PATCH /api/v1/networks/{nid}", requireToken(func(w http.ResponseWriter, r *http.Request) {
		if !ownerNID(w, r) {
			return
		}
		var req protocol.NetworkSettingsReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, ErrBadJSON)
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
			writeErr(w, http.StatusBadRequest, ErrBadJSON)
			return
		}
		if err := s.ClaimNetwork(r.PathValue("nid"), tokenOf(r), req.DeviceID); err != nil {
			handleStoreErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	// ---- admin surface ----
	// The page, login and bootstrap routes are always served so a fresh
	// deployment can initialize the admin account from the web UI; every
	// other admin route is gated by requireAdmin (404 while locked).
	mux.HandleFunc("POST /admin/login", h.adminLogin)
	mux.HandleFunc("POST /admin/logout", h.requireAdmin(h.adminLogout))
	mux.HandleFunc("GET /admin/bootstrap", h.adminBootstrapStatus)
	mux.HandleFunc("POST /admin/bootstrap", h.adminBootstrapCreate)
	mux.HandleFunc("POST /admin/password", h.requireAdmin(h.adminPasswordChange))
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// The page is a single self-contained HTML file: inline script/style,
		// QR codes as data/blob images, same-origin API calls, nothing else.
		w.Header().Set("Content-Security-Policy",
			"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; "+
				"img-src data: blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
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
			writeErr(w, http.StatusBadRequest, ErrBadJSON)
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
		reqPage, pageSize := adminPageParams(r)
		items, total, page := s.AdminNetworksPage(h.opts.ZombieTTL, r.URL.Query().Get("q"), r.URL.Query().Get("status"), reqPage, pageSize)
		writeJSON(w, http.StatusOK, adminPageResp[networkSummary]{Items: items, Total: total, Page: page, PageSize: pageSize})
	}))
	mux.HandleFunc("GET /admin/stats", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.AdminOverview(h.opts.ZombieTTL))
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
			writeErr(w, http.StatusBadRequest, ErrBadJSON)
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
		reqPage, pageSize := adminPageParams(r)
		items, total, page := s.AdminDevicesPage(r.URL.Query().Get("q"), reqPage, pageSize)
		writeJSON(w, http.StatusOK, adminPageResp[protocol.Device]{Items: items, Total: total, Page: page, PageSize: pageSize})
	}))
	mux.HandleFunc("GET /admin/devices/{deviceID}/networks", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, protocol.NetworksResp{Networks: s.DeviceNetworks(r.PathValue("deviceID"))})
	}))
	mux.HandleFunc("POST /admin/devices/authcodes/generate", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		var req protocol.AdminGenerateAuthCodesReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, ErrBadJSON)
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
		reqPage, pageSize := adminPageParams(r)
		items, total, page := s.AdminAuthCodesPage(reqPage, pageSize)
		writeJSON(w, http.StatusOK, adminPageResp[protocol.AuthCodeInfo]{Items: items, Total: total, Page: page, PageSize: pageSize})
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

	mux.HandleFunc("DELETE /admin/devices/{deviceID}", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("deviceID")
		if err := s.AdminDeleteDevice(deviceID); err != nil {
			handleStoreErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	mux.HandleFunc("PATCH /admin/devices/{deviceID}", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("deviceID")
		var req protocol.AdminRenameDeviceReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, errors.New("bad request"))
			return
		}
		if err := s.AdminRenameDevice(deviceID, req.Name); err != nil {
			handleStoreErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	return secureHeaders(logRequests(limitBody(mux)))
}

// secureHeaders sets baseline protective response headers. API and admin
// responses are marked no-store so bearer tokens, pairing codes and plaintext
// authorization codes never land in shared caches or browser disk cache.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/admin") || strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// adminUnlocked reports whether any admin authentication path is active:
// a static token or a username/password account (configured via flags/env or
// initialized through the web setup wizard).
// adminPageResp is the uniform paged-listing envelope for the admin console.
type adminPageResp[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
}

// adminPageParams reads the page/page_size query params with sane defaults
// (1 and 20) and clamping handled by the store layer.
func adminPageParams(r *http.Request) (page, pageSize int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ = strconv.Atoi(r.URL.Query().Get("page_size"))
	return clampPage(page, pageSize)
}

func (h *handler) adminUnlocked() bool {
	user, hash := h.creds()
	return h.opts.AdminToken != "" || (user != "" && hash != "")
}

// creds returns the active session-login username and password hash under a
// read lock.
func (h *handler) creds() (user, hash string) {
	h.adminMu.RLock()
	defer h.adminMu.RUnlock()
	return h.adminUser, h.adminPassHash
}

// setCreds rotates the active credentials under the write lock.
func (h *handler) setCreds(user, hash string) {
	h.adminMu.Lock()
	defer h.adminMu.Unlock()
	h.adminUser = user
	h.adminPassHash = hash
}

// bootstrapAvailable reports whether the first-run web setup wizard may run:
// no static token, no configured credentials and no stored admin account.
func (h *handler) bootstrapAvailable() bool {
	_, hash := h.creds()
	return h.opts.AdminToken == "" && h.opts.AdminUser == "" && hash == ""
}

func (h *handler) adminLogin(w http.ResponseWriter, r *http.Request) {
	user, hash := h.creds()
	if user == "" || hash == "" {
		writeErr(w, http.StatusNotFound, ErrNotFound)
		return
	}
	if h.logins.blocked(h.clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, errors.New("too many attempts"))
		return
	}
	var req protocol.AdminLoginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, ErrBadJSON)
		return
	}
	userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(user)) == 1
	passOK := hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) == nil
	if !userOK || !passOK {
		writeErr(w, http.StatusUnauthorized, ErrUnauthorized)
		return
	}
	tok, err := randomToken()
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	expires := time.Now().Add(sessionTTL)
	h.sessMu.Lock()
	h.sessions[hashToken(tok)] = sessionEntry{expires: expires}
	h.gcSessionsLocked()
	h.sessMu.Unlock()
	writeJSON(w, http.StatusOK, protocol.AdminLoginResp{Token: tok, Expires: expires.Unix()})
}

// adminBootstrapStatus tells the web setup wizard whether first-run
// initialization is still possible.
func (h *handler) adminBootstrapStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"available": h.bootstrapAvailable()})
}

// adminBootstrapCreate initializes the very first admin account from the web
// setup wizard. It is only accepted while no account exists and no admin
// token/credentials are configured; the created account is logged in
// immediately.
func (h *handler) adminBootstrapCreate(w http.ResponseWriter, r *http.Request) {
	// Serialize the whole flow so two simultaneous first-run submissions are
	// decided by the store-level ErrAdminExists guard, not a racy availability
	// check.
	h.bootstrapMu.Lock()
	defer h.bootstrapMu.Unlock()
	if !h.bootstrapAvailable() {
		if h.adminUnlocked() {
			writeErr(w, http.StatusConflict, ErrAdminExists)
		} else {
			writeErr(w, http.StatusNotFound, ErrNotFound)
		}
		return
	}
	if h.logins.blocked(h.clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, errors.New("too many attempts"))
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, ErrBadJSON)
		return
	}
	user := strings.TrimSpace(req.Username)
	switch {
	case user == "" || len(user) > 64:
		writeErr(w, http.StatusBadRequest, errors.New("用户名需为 1-64 个字符"))
		return
	case strings.ContainsAny(user, " \t\r\n"):
		writeErr(w, http.StatusBadRequest, errors.New("用户名不能包含空白字符"))
		return
	case len(req.Password) < 8:
		writeErr(w, http.StatusBadRequest, errors.New("密码至少 8 位"))
		return
	}
	hash, err := h.s.BootstrapAdmin(user, req.Password)
	if err != nil {
		if errors.Is(err, ErrAdminExists) {
			writeErr(w, http.StatusConflict, err)
		} else {
			writeInternalErr(w, err)
		}
		return
	}
	h.setCreds(user, hash)
	log.Printf("admin: account %q initialized via web bootstrap", user)
	tok, err := randomToken()
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	expires := time.Now().Add(sessionTTL)
	h.sessMu.Lock()
	h.sessions[hashToken(tok)] = sessionEntry{expires: expires}
	h.sessMu.Unlock()
	writeJSON(w, http.StatusOK, protocol.AdminLoginResp{Token: tok, Expires: expires.Unix()})
}

// adminPasswordChange rotates the admin login password. It verifies the old
// password, persists the new bcrypt hash and invalidates every existing admin
// session so all clients must log in again.
func (h *handler) adminPasswordChange(w http.ResponseWriter, r *http.Request) {
	user, hash := h.creds()
	if user == "" || hash == "" {
		writeErr(w, http.StatusNotFound, ErrNotFound)
		return
	}
	var req protocol.AdminPasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, ErrBadJSON)
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
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.OldPassword)) != nil {
		writeErr(w, http.StatusUnauthorized, errors.New("旧密码不正确"))
		return
	}
	newHash, err := h.s.SetAdminPassword(user, req.NewPassword)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	h.setCreds(user, newHash)
	h.sessMu.Lock()
	h.sessions = make(map[string]sessionEntry)
	h.sessMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// adminLogout invalidates the caller's session. Static-token callers get a
// success no-op: a static token cannot be revoked per-session.
func (h *handler) adminLogout(w http.ResponseWriter, r *http.Request) {
	tok := tokenOf(r)
	if tok != "" && !h.sessionValid(tok) {
		// Static token or already-invalid session: still report success so
		// the client can always clear its local state.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.sessMu.Lock()
	delete(h.sessions, hashToken(tok))
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
		if !h.adminUnlocked() {
			// No admin configured at all: keep the surface invisible.
			writeErr(w, http.StatusNotFound, ErrNotFound)
			return
		}
		writeErr(w, http.StatusUnauthorized, ErrUnauthorized)
	}
}

func requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := tokenOf(r)
		if t == "" {
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

// deviceTokenOf extracts the device token from the X-Device-Token header.
func deviceTokenOf(r *http.Request) string {
	return r.Header.Get("X-Device-Token")
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

// prune deletes entries whose window has expired so the per-IP map does not
// grow without bound over the process lifetime.
func (rl *rateLimiter) prune() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	for ip, wc := range rl.seen {
		if now.After(wc.reset) {
			delete(rl.seen, ip)
		}
	}
}

func (h *handler) clientIP(r *http.Request) string {
	if h.opts.TrustProxy {
		// Use the RIGHTMOST XFF entry: the leftmost is fully client-controlled,
		// while the rightmost was appended by the trusted proxy itself. The
		// proxy must still be configured to strip inbound spoofed XFF chains.
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.LastIndex(xff, ","); i >= 0 {
				xff = xff[i+1:]
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
		log.Printf("internal error: %v", err)
		writeErr(w, http.StatusInternalServerError, ErrInternal)
	}
}

// writeInternalErr logs the real error and returns a generic message so
// internal details never leak to clients.
func writeInternalErr(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeErr(w, http.StatusInternalServerError, ErrInternal)
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
		log.Printf("%s %s from %s", r.Method, sanitizeLog(r.URL.Path), sanitizeLog(clientIPOf(r)))
		next.ServeHTTP(w, r)
	})
}

// sanitizeLog escapes control characters so request paths cannot inject
// forged lines into the log stream.
func sanitizeLog(s string) string {
	if strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0 {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r != 0x7f {
			b.WriteRune(r)
			continue
		}
		fmt.Fprintf(&b, "\\x%02x", r)
	}
	return b.String()
}

func clientIPOf(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
