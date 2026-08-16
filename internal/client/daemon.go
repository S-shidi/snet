package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"virtualnet/internal/protocol"
)

// retryInterval is how often the daemon re-attempts tunnel creation for a
// network whose initial bring-up failed (e.g. insufficient privileges to
// create a TUN device) until it comes up or is left/removed.
const retryInterval = 30 * time.Second

// netRuntime holds the live tunnel + control loops for one joined network.
type netRuntime struct {
	tun  *Tunnel
	stop chan struct{}
	err  string
}

// Daemon coordinates the local tunnels and the coordination server for any
// number of networks against a single server.
type Daemon struct {
	mu   sync.Mutex
	cfg  *Config
	nets map[string]*netRuntime
	// pendingRuns tracks the poll goroutines of pending join requests.
	pendingRuns map[string]chan struct{}
	// api caches the HTTP client so TCP/TLS connections are reused across
	// poll ticks instead of paying a full handshake every PollIntervalSeconds.
	// Rebuilt whenever the server address or pinned CA path changes.
	api       *apiClient
	apiServer string
	apiCA     string
	// configPath is where Save persists state; empty falls back to the user
	// config dir. Explicit path keeps daemons running without $HOME
	// (e.g. launchd) functional.
	configPath string
	// deviceIDFile is the on-disk stable device identity; empty disables it
	// (used in tests / read-only environments).
	deviceIDFile string
	// bindCheckStop stops the periodic binding-verification goroutine that
	// detects an admin revocation of this device on its bound server.
	bindCheckStop chan struct{}
	// netErrs records the last tunnel bring-up failure per network so the
	// status API can explain why an active-looking network has no tunnel.
	netErrs map[string]string
	// retryPending tracks networks whose tunnel creation failed; a background
	// loop retries them so transient failures (or a late privilege fix, e.g.
	// installing the root LaunchDaemon) self-heal without a daemon restart.
	retryPending map[string]struct{}
	// retryStop signals the background retry loop to exit.
	retryStop chan struct{}
}

func NewDaemon(cfg *Config) *Daemon {
	return NewDaemonAt(cfg, "")
}

func NewDaemonAt(cfg *Config, configPath string) *Daemon {
	return &Daemon{
		cfg:          cfg,
		configPath:   configPath,
		nets:         make(map[string]*netRuntime),
		pendingRuns:  make(map[string]chan struct{}),
		netErrs:      make(map[string]string),
		retryPending: make(map[string]struct{}),
		deviceIDFile: DefaultDeviceIDFile,
	}
}

// SetDeviceIDFile overrides the on-disk device identity path.
func (d *Daemon) SetDeviceIDFile(path string) {
	d.deviceIDFile = path
}

func (d *Daemon) save() error {
	if d.configPath != "" {
		return d.cfg.SaveAt(d.configPath)
	}
	return d.cfg.Save()
}

func (d *Daemon) apiLocked() *apiClient {
	if d.api != nil && d.apiServer == d.cfg.ServerAddr && d.apiCA == d.cfg.ServerCAPath {
		return d.api
	}
	d.api = newAPIClient(d.cfg.ServerAddr, d.cfg.ServerCAPath)
	d.apiServer = d.cfg.ServerAddr
	d.apiCA = d.cfg.ServerCAPath
	return d.api
}

func (d *Daemon) publicKeyLocked() string {
	return pubKeyB64FromPrivHex(d.cfg.PrivateKey)
}

// ensureKeys points the daemon at a server and guarantees a private key and
// device ID exist. It no longer pins the device to one server: switching is
// always allowed, and any networks held on the previous server are torn down
// by commitSwitchLocked once the operation against the new server succeeds.
func (d *Daemon) ensureKeys(serverAddr string, port int) error {
	d.cfg.ServerAddr = normalizeServer(serverAddr)
	if port != 0 {
		d.cfg.WireguardPort = port
	}
	if d.cfg.PrivateKey == "" {
		priv, _, err := GenerateKeyPair()
		if err != nil {
			return err
		}
		d.cfg.PrivateKey = b64ToHex(priv)
		if err := d.save(); err != nil {
			return err
		}
	}
	if d.cfg.DeviceID == "" {
		id, err := LoadOrCreateDeviceID(d.deviceIDFile, "")
		if err != nil {
			return err
		}
		d.cfg.DeviceID = id
		if err := d.save(); err != nil {
			return err
		}
	}
	return nil
}

func normalizeServer(s string) string {
	if s == "" {
		return s
	}
	if u, err := url.Parse(s); err == nil && u.Scheme == "" {
		s = "https://" + s
	}
	return strings.TrimRight(s, "/")
}

// serverSwitch tracks a pending effective-server change. The device belongs to
// exactly one server at a time; switching servers clears every network and
// pending join created on the previous server. The clear is committed only
// after the operation against the new server succeeds so a failed switch never
// destroys the previous server's networks.
type serverSwitch struct {
	changed  bool
	prevAddr string
	prevCA   string
}

func (d *Daemon) beginSwitchLocked(target string) serverSwitch {
	return serverSwitch{
		prevAddr: d.cfg.ServerAddr,
		prevCA:   d.cfg.ServerCAPath,
		changed:  normalizeServer(target) != normalizeServer(d.cfg.ServerAddr),
	}
}

// commitSwitchLocked finalizes a server switch: tunnels, poll loops, networks
// and pending joins tied to the previous server are torn down and forgotten.
// The old server's networks stay alive for the other members.
func (d *Daemon) commitSwitchLocked(sw serverSwitch) {
	if !sw.changed {
		return
	}
	for nid, rt := range d.nets {
		if rt.tun != nil {
			rt.tun.Close()
		}
		close(rt.stop)
		delete(d.nets, nid)
	}
	for pid, stop := range d.pendingRuns {
		close(stop)
		delete(d.pendingRuns, pid)
	}
	d.netErrs = make(map[string]string)
	d.retryPending = make(map[string]struct{})
	if d.retryStop != nil {
		close(d.retryStop)
		d.retryStop = nil
	}
	d.cfg.Networks = nil
	d.cfg.PendingJoins = nil
}

// rollbackSwitchLocked restores the previous server after a failed operation.
// Nothing was torn down, so the previous server's networks stay fully intact.
func (d *Daemon) rollbackSwitchLocked(sw serverSwitch) {
	d.cfg.ServerAddr = sw.prevAddr
	d.cfg.ServerCAPath = sw.prevCA
	_ = d.save()
}

// isUnboundErr reports whether err is the server's "device not authorized"
// enrollment-gate response, i.e. the device is not bound on that server.
func isUnboundErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "设备未授权")
}

// clearBindingLocked drops the local bound state. Callers must hold d.mu.
func (d *Daemon) clearBindingLocked() {
	if d.cfg.BoundServer == "" {
		return
	}
	d.cfg.BoundServer = ""
	_ = d.save()
}

// handleUnboundOpErrLocked clears the local binding when an operation against
// this device's own bound server reports it as no longer authorized. A 403
// against a different server is expected (the device simply is not bound
// there) and must not erase the current binding.
func (d *Daemon) handleUnboundOpErrLocked(target string, err error) {
	if !isUnboundErr(err) {
		return
	}
	if d.cfg.BoundServer != "" && normalizeServer(target) == d.cfg.BoundServer {
		d.clearBindingLocked()
	}
}

// bindCheckInterval is how often a bound device verifies its binding with the
// server, so an admin revocation is reflected in the status within seconds.
var bindCheckInterval = 10 * time.Second

// startBindCheckLocked launches the periodic binding-verification goroutine.
// Callers must hold d.mu.
func (d *Daemon) startBindCheckLocked() {
	if d.bindCheckStop != nil {
		return
	}
	stop := make(chan struct{})
	d.bindCheckStop = stop
	go d.bindCheckLoop(stop)
}

// bindCheckLoop polls the server's view of this device's binding. It exits as
// soon as the device is no longer bound (revocation detected) or no longer in
// a bindable custom mode, or when the daemon closes.
func (d *Daemon) bindCheckLoop(stop chan struct{}) {
	ticker := time.NewTicker(bindCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		if !d.verifyBinding() {
			d.mu.Lock()
			d.bindCheckStop = nil
			d.mu.Unlock()
			return
		}
	}
}

// verifyBinding checks the current server's view of this device's binding and
// clears the local bound state when the server no longer considers the device
// bound. It returns false when the check loop should stop.
func (d *Daemon) verifyBinding() bool {
	d.mu.Lock()
	if !d.cfg.Bound() || d.cfg.ServerAddr == "" {
		d.mu.Unlock()
		return false
	}
	api := d.apiLocked()
	deviceID := d.cfg.DeviceID
	pub := d.publicKeyLocked()
	d.mu.Unlock()

	bound, err := api.RegisterDevice(deviceID, pub)
	if err != nil {
		// A definite "not authorized" answer means the server revoked the
		// binding. Transient errors (timeouts, DNS) are not a revocation.
		if isUnboundErr(err) {
			d.mu.Lock()
			d.clearBindingLocked()
			d.mu.Unlock()
			return false
		}
		return true
	}
	// Only an explicit non-nil response is authoritative; old servers that do
	// not report binding status leave the local state untouched.
	if bound != nil && !*bound {
		d.mu.Lock()
		d.clearBindingLocked()
		d.mu.Unlock()
		return false
	}
	return true
}

// Create makes a new network on the server and joins this node as owner.
func (d *Daemon) Create(serverAddr string, port int, name, subnet string, approvalRequired bool) (protocol.CreateNetworkResp, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if serverAddr == "" {
		serverAddr = d.cfg.ServerAddr
	}
	if serverAddr == "" {
		return protocol.CreateNetworkResp{}, errors.New("未连接服务器：请先在设置中链接服务器")
	}
	sw := d.beginSwitchLocked(serverAddr)
	// The per-device owner limit applies to the networks that remain after the
	// switch: changing servers clears the previous server's owned network, so
	// creating a new one there is allowed.
	remaining := d.cfg.Networks
	if sw.changed {
		remaining = nil
	}
	for _, nc := range remaining {
		if nc.Owner {
			return protocol.CreateNetworkResp{}, errors.New("本设备已创建网络（每客户端仅能创建一个）")
		}
	}
	if err := d.ensureKeys(serverAddr, port); err != nil {
		d.rollbackSwitchLocked(sw)
		return protocol.CreateNetworkResp{}, err
	}
	api := d.apiLocked()
	resp, err := api.CreateNetwork(d.publicKeyLocked(), d.cfg.DeviceID, name, subnet, approvalRequired)
	if err != nil {
		d.handleUnboundOpErrLocked(serverAddr, err)
		d.rollbackSwitchLocked(sw)
		return resp, err
	}
	d.commitSwitchLocked(sw)
	if err := d.attach(resp.NetworkID, name, resp.NodeID, resp.IP, resp.Token, resp.PairingCode, resp.Subnet, true); err != nil {
		return resp, err
	}
	d.reconcileOwnership(resp.NetworkID)
	return resp, nil
}

// Join joins an existing network by nid + pairing code.
func (d *Daemon) Join(serverAddr string, port int, nid, code string) (protocol.JoinResp, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if serverAddr == "" {
		serverAddr = d.cfg.ServerAddr
	}
	if serverAddr == "" {
		return protocol.JoinResp{}, errors.New("未连接服务器：请先在设置中链接服务器")
	}
	sw := d.beginSwitchLocked(serverAddr)
	if err := d.ensureKeys(serverAddr, port); err != nil {
		d.rollbackSwitchLocked(sw)
		return protocol.JoinResp{}, err
	}
	api := d.apiLocked()
	resp, err := api.Join(nid, code, d.publicKeyLocked(), d.cfg.DeviceID)
	if err != nil {
		d.handleUnboundOpErrLocked(serverAddr, err)
		d.rollbackSwitchLocked(sw)
		return resp, err
	}
	d.commitSwitchLocked(sw)
	if resp.Status == "pending" {
		if _, ok := d.cfg.Networks[resp.NetworkID]; ok {
			return resp, fmt.Errorf("本机已加入网络 %s", resp.NetworkID)
		}
		return resp, d.registerPendingLocked(resp)
	}
	if _, ok := d.cfg.Networks[resp.NetworkID]; ok {
		return resp, fmt.Errorf("本机已加入网络 %s", resp.NetworkID)
	}
	if err := d.attach(resp.NetworkID, resp.Name, resp.NodeID, resp.IP, resp.Token, "", resp.Subnet, false); err != nil {
		return resp, err
	}
	d.reconcileOwnership(resp.NetworkID)
	return resp, nil
}

// Bind links this device to the coordination server using an admin-generated
// device authorization code. The code travels once over the wire and is never
// persisted. Binding puts the daemon in "custom" server mode. Binding to a
// different server clears the previous server's networks on success; a failed
// bind leaves them untouched.
func (d *Daemon) Bind(serverAddr, caPath, code string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	sw := d.beginSwitchLocked(serverAddr)
	if err := d.ensureKeys(serverAddr, 0); err != nil {
		d.rollbackSwitchLocked(sw)
		return err
	}
	d.cfg.ServerCAPath = caPath
	api := d.apiLocked()
	if _, err := api.BindDevice(d.cfg.DeviceID, d.publicKeyLocked(), code); err != nil {
		d.rollbackSwitchLocked(sw)
		return err
	}
	d.commitSwitchLocked(sw)
	d.cfg.BoundServer = normalizeServer(d.cfg.ServerAddr)
	if err := d.save(); err != nil {
		return err
	}
	d.startBindCheckLocked()
	return nil
}

func (d *Daemon) registerPendingLocked(resp protocol.JoinResp) error {
	if d.cfg.PendingJoins == nil {
		d.cfg.PendingJoins = map[string]*PendingJoin{}
	}
	pj := &PendingJoin{
		PendingID: resp.PendingID,
		NetworkID: resp.NetworkID,
		Name:      resp.Name,
		Code:      "",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Status:    "pending",
	}
	d.cfg.PendingJoins[resp.PendingID] = pj
	if err := d.save(); err != nil {
		return err
	}
	d.startPendingLoopLocked(resp.PendingID)
	return nil
}

func (d *Daemon) startPendingLoopLocked(pendingID string) {
	if _, ok := d.pendingRuns[pendingID]; ok {
		return
	}
	stop := make(chan struct{})
	d.pendingRuns[pendingID] = stop
	go d.pendingLoop(pendingID, stop)
}

// pendingPollInterval controls how often a pending join is re-checked.
var pendingPollInterval = 3 * time.Second

// pendingLoop polls an awaiting-approval join until it is approved (then
// joins), denied, or expires. It also resumes after daemon restarts.
func (d *Daemon) pendingLoop(pendingID string, stop chan struct{}) {
	ticker := time.NewTicker(pendingPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		d.mu.Lock()
		pj := d.cfg.PendingJoins[pendingID]
		if pj == nil {
			delete(d.pendingRuns, pendingID)
			d.mu.Unlock()
			return
		}
		api := d.apiLocked()
		d.mu.Unlock()

		// Poll without holding d.mu so a slow server never stalls the UI.
		status, err := api.PendingJoinStatus(pendingID)
		if err != nil {
			log.Printf("pending %s: %v", pendingID, err)
			continue
		}

		d.mu.Lock()
		if d.cfg.PendingJoins[pendingID] == nil {
			d.mu.Unlock()
			return
		}
		switch status.Status {
		case "approved":
			delete(d.cfg.PendingJoins, pendingID)
			delete(d.pendingRuns, pendingID)
			_ = d.save()
			err := d.attach(status.NetworkID, pj.Name, status.NodeID, status.IP, status.Token, pj.Code, status.Subnet, false)
			if err != nil {
				log.Printf("join approved %s: %v", pendingID, err)
				d.mu.Unlock()
				return
			}
			d.reconcileOwnership(status.NetworkID)
			d.mu.Unlock()
			log.Printf("pending %s approved, joined %s", pendingID, status.NetworkID)
			return
		case "denied":
			pj.Status = "denied"
			pj.Err = "创建者已拒绝加入请求"
			_ = d.save()
			d.mu.Unlock()
			log.Printf("pending %s denied", pendingID)
			return
		case "gone":
			delete(d.cfg.PendingJoins, pendingID)
			delete(d.pendingRuns, pendingID)
			pj.Status = "denied"
			pj.Err = "加入请求已过期"
			_ = d.save()
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()
	}
}

// CancelPending stops waiting for approval and drops the pending request.
func (d *Daemon) CancelPending(pendingID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	pj := d.cfg.PendingJoins[pendingID]
	if pj == nil {
		return fmt.Errorf("待批准请求 %s 未找到", pendingID)
	}
	if stop := d.pendingRuns[pendingID]; stop != nil {
		close(stop)
		delete(d.pendingRuns, pendingID)
	}
	delete(d.cfg.PendingJoins, pendingID)
	return d.save()
}

// attach stores a joined network and brings it up.
func (d *Daemon) attach(nid, name, nodeID, ip, token, pairingCode, subnet string, owner bool) error {
	if subnet == "" {
		subnet = defaultSubnet
	}
	for other, oc := range d.cfg.Networks {
		if other != nid && oc.Active && subnetsOverlap(subnet, oc.Subnet) {
			return fmt.Errorf("网段 %s 与已激活网络 %s（%s）冲突，请先停用", subnet, other, oc.Subnet)
		}
	}
	if d.cfg.Networks == nil {
		d.cfg.Networks = map[string]*NetworkCfg{}
	}
	nc := &NetworkCfg{
		Name:        name,
		NodeID:      nodeID,
		IP:          ip,
		Token:       token,
		PairingCode: pairingCode,
		Subnet:      subnet,
		Port:        0,
		Active:      true,
		Owner:       owner,
	}
	if existing, ok := d.cfg.Networks[nid]; ok && existing.Name != "" && name == "" {
		nc.Name = existing.Name
	}
	d.cfg.Networks[nid] = nc
	if err := d.save(); err != nil {
		return err
	}
	if err := d.bringUp(nid); err != nil {
		log.Printf("bring up %s: %v", nid, err)
	}
	return nil
}

// reconcileOwnership migrates legacy networks: registers this device, binds
// its own node, and claims ownerless networks. Errors are non-fatal.
func (d *Daemon) reconcileOwnership(nid string) {
	nc := d.cfg.Networks[nid]
	if nc == nil {
		return
	}
	api := d.apiLocked()
	if _, err := api.RegisterDevice(d.cfg.DeviceID, d.publicKeyLocked()); err != nil {
		log.Printf("register device: %v", err)
	}
	if err := api.SetNodeDevice(nid, nc.NodeID, nc.Token, d.cfg.DeviceID); err != nil {
		log.Printf("bind node device: %v", err)
	}
	if err := api.ClaimNetwork(nid, nc.Token, d.cfg.DeviceID); err == nil {
		nc.Owner = true
		if err := d.save(); err != nil {
			log.Printf("save owner flag: %v", err)
		}
	}
}

// Start resumes from persisted state (tunnels + poll loops) for every active
// network, then reconciles device ownership for legacy networks.
func (d *Daemon) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cfg.DeviceID == "" {
		id, err := LoadOrCreateDeviceID(d.deviceIDFile, "")
		if err != nil {
			return err
		}
		d.cfg.DeviceID = id
		if err := d.save(); err != nil {
			return err
		}
	}
	for nid := range d.cfg.Networks {
		nc := d.cfg.Networks[nid]
		if !nc.Active {
			continue
		}
		if err := d.bringUp(nid); err != nil {
			log.Printf("start network %s: %v", nid, err)
		}
		d.reconcileOwnership(nid)
	}
	for pendingID := range d.cfg.PendingJoins {
		d.startPendingLoopLocked(pendingID)
	}
	// A bound device verifies its binding with the server periodically so an
	// admin revocation clears the local "bound" state within seconds.
	if d.cfg.Bound() {
		d.startBindCheckLocked()
	}
	return nil
}

func (d *Daemon) bringUp(nid string) error {
	nc := d.cfg.Networks[nid]
	if nc == nil {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	if rt := d.nets[nid]; rt != nil {
		if rt.tun != nil {
			rt.tun.Close()
		}
		close(rt.stop)
		delete(d.nets, nid)
	}
	if nc.Port == 0 {
		nc.Port = d.pickPortLocked()
	}
	t, err := NewTunnel(d.cfg.PrivateKey, nc.IP, nc.Port, protocol.DefaultMTU)
	if err != nil {
		d.netErrs[nid] = err.Error()
		d.scheduleRetryLocked(nid)
		return fmt.Errorf("tunnel: %w", err)
	}
	delete(d.netErrs, nid)
	rt := &netRuntime{tun: t, stop: make(chan struct{})}
	d.nets[nid] = rt

	endpoint, err := d.localEndpointLocked(nc.Port)
	if err != nil {
		log.Printf("endpoint detect failed: %v", err)
	} else {
		if err := d.apiLocked().SetEndpointFor(nid, nc.NodeID, nc.Token, endpoint); err != nil {
			log.Printf("set endpoint: %v", err)
		}
	}
	go d.pollLoop(nid)
	go d.probeLoop(nid)
	return nil
}

// scheduleRetryLocked marks a network for background re-bring-up after its
// tunnel failed to come up, starting the retry loop if it is not running.
func (d *Daemon) scheduleRetryLocked(nid string) {
	if d.retryPending == nil {
		d.retryPending = make(map[string]struct{})
	}
	d.retryPending[nid] = struct{}{}
	if d.retryStop == nil {
		stop := make(chan struct{})
		d.retryStop = stop
		go d.retryLoop(stop)
	}
}

// retryLoop re-attempts tunnel creation for failed networks until they come up
// or are left/removed; it stops itself once nothing remains to retry.
func (d *Daemon) retryLoop(stop chan struct{}) {
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-stop:
			return
		}
		d.mu.Lock()
		for nid := range d.retryPending {
			nc := d.cfg.Networks[nid]
			if nc == nil || !nc.Active || d.nets[nid] != nil {
				delete(d.retryPending, nid)
				continue
			}
			if err := d.bringUp(nid); err != nil {
				log.Printf("retry bring up %s: %v", nid, err)
			} else {
				delete(d.retryPending, nid)
			}
		}
		if len(d.retryPending) == 0 {
			close(d.retryStop)
			d.retryStop = nil
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()
	}
}

// pickPortLocked finds a free UDP port, preferring the configured base.
func (d *Daemon) pickPortLocked() int {
	base := d.cfg.WireguardPort
	if base == 0 {
		base = protocol.DefaultWGPort
	}
	for p := base; p < base+64; p++ {
		if portFree(p) {
			return p
		}
	}
	return base
}

func (d *Daemon) localEndpointLocked(port int) (string, error) {
	ip, err := LocalIPForServer(d.cfg.ServerAddr)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(ip, fmt.Sprint(port)), nil
}

func (d *Daemon) failNetwork(nid, msg string) {
	rt := d.nets[nid]
	if rt == nil {
		return
	}
	if rt.tun != nil {
		rt.tun.Close()
	}
	close(rt.stop)
	delete(d.nets, nid)
	if nc := d.cfg.Networks[nid]; nc != nil {
		nc.Active = false
		_ = d.save()
	}
	log.Printf("network %s failed: %s", nid, msg)
}

func (d *Daemon) pollLoop(nid string) {
	ticker := time.NewTicker(protocol.PollIntervalSeconds * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		}
		d.mu.Lock()
		rt := d.nets[nid]
		if rt == nil || rt.tun == nil {
			d.mu.Unlock()
			return
		}
		nc := d.cfg.Networks[nid]
		if nc == nil {
			d.mu.Unlock()
			return
		}
		api := d.apiLocked()
		token := nc.Token
		d.mu.Unlock()

		// The coordination call runs without holding d.mu so slow/jittery
		// links do not stall the control API (status/info/members).
		st, err := api.PeersState(nid, token)
		if err != nil {
			log.Printf("poll peers %s: %v", nid, err)
			continue
		}

		d.mu.Lock()
		rt = d.nets[nid]
		if rt == nil || rt.tun == nil {
			d.mu.Unlock()
			return
		}
		nc = d.cfg.Networks[nid]
		if nc == nil {
			d.mu.Unlock()
			return
		}
		// The owner may have changed the network subnet; the server re-allocated
		// our IP. Rebuild the tunnel on the new address (port unchanged).
		if st.Self != nil && st.Self.IP != "" && st.Self.IP != nc.IP {
			old := nc.IP
			nc.IP = st.Self.IP
			if st.Subnet != "" {
				nc.Subnet = st.Subnet
			}
			if err := d.save(); err != nil {
				log.Printf("save after IP change %s: %v", nid, err)
			}
			log.Printf("network %s IP changed %s -> %s (subnet %s)", nid, old, nc.IP, nc.Subnet)
			if err := d.bringUp(nid); err != nil {
				log.Printf("rebuild after IP change %s: %v", nid, err)
			}
			rt = d.nets[nid]
			if rt == nil || rt.tun == nil {
				d.mu.Unlock()
				return
			}
		} else if st.Subnet != "" && st.Subnet != nc.Subnet {
			nc.Subnet = st.Subnet
			if err := d.save(); err != nil {
				log.Printf("save subnet %s: %v", nid, err)
			}
		}
		// 同步服务器上的网络名称，保证客户端卡片与服务端显示一致。
		if st.Name != "" && st.Name != nc.Name {
			nc.Name = st.Name
			if err := d.save(); err != nil {
				log.Printf("save name %s: %v", nid, err)
			}
		}
		if err := rt.tun.ApplyPeers(st.Peers); err != nil {
			log.Printf("apply peers %s: %v", nid, err)
		}
		d.mu.Unlock()
	}
}

// probeLoop periodically asks the coordination server's UDP echo for this
// node's public (NAT-observed) IP and advertises publicIP:wgPort as the
// endpoint, so remote peers can attempt a direct hole-punched connection.
func (d *Daemon) probeLoop(nid string) {
	ticker := time.NewTicker(protocol.ProbeIntervalSeconds * time.Second)
	defer ticker.Stop()
	lastIP := ""
	for {
		select {
		case <-ticker.C:
		}
		d.mu.Lock()
		rt := d.nets[nid]
		if rt == nil {
			d.mu.Unlock()
			return
		}
		nc := d.cfg.Networks[nid]
		if nc == nil {
			d.mu.Unlock()
			return
		}
		serverAddr := d.cfg.ServerAddr
		serverCA := d.cfg.ServerCAPath
		port := nc.Port
		d.mu.Unlock()

		ip, err := probePublicIP(serverProbeAddr(serverAddr), nid, nc.NodeID, nc.Token)
		if err != nil {
			log.Printf("probe public IP %s: %v", nid, err)
			continue
		}
		if ip == lastIP {
			continue
		}
		lastIP = ip
		ep := net.JoinHostPort(ip, fmt.Sprint(port))
		if err := newAPIClient(serverAddr, serverCA).SetEndpointFor(nid, nc.NodeID, nc.Token, ep); err != nil {
			log.Printf("set public endpoint %s: %v", nid, err)
			continue
		}
		log.Printf("public endpoint %s: %s", nid, ep)
	}
}

// serverProbeAddr derives the UDP probe address (host:ProbePort) from the
// coordination server's base URL.
func serverProbeAddr(server string) string {
	if u, err := url.Parse(server); err == nil && u.Hostname() != "" {
		return net.JoinHostPort(u.Hostname(), fmt.Sprint(protocol.ProbePort))
	}
	return net.JoinHostPort(server, fmt.Sprint(protocol.ProbePort))
}

// probePublicIP sends one UDP probe to the server and returns the public IP
// the server observed for us.
func probePublicIP(probeAddr, nid, nodeID, token string) (string, error) {
	raddr, err := net.ResolveUDPAddr("udp", probeAddr)
	if err != nil {
		return "", err
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	payload, _ := json.Marshal(map[string]string{"networkId": nid, "nodeId": nodeID, "token": token})
	if _, err := conn.Write(payload); err != nil {
		return "", err
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		return "", err
	}
	var resp struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return "", err
	}
	ip, _, err := net.SplitHostPort(resp.Endpoint)
	if err != nil {
		return "", err
	}
	return ip, nil
}

// Leave stops and forgets a network's tunnel but keeps the server-side node
// so the owner can still see this device. Empty nid leaves all networks.
func (d *Daemon) Leave(nid string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if nid == "" {
		for id := range d.cfg.Networks {
			d.leaveLocked(id)
		}
		return d.save()
	}
	if _, ok := d.cfg.Networks[nid]; !ok {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	d.leaveLocked(nid)
	return d.save()
}

func (d *Daemon) leaveLocked(nid string) {
	if rt := d.nets[nid]; rt != nil {
		if rt.tun != nil {
			rt.tun.Close()
		}
		close(rt.stop)
		delete(d.nets, nid)
	}
	if nc := d.cfg.Networks[nid]; nc != nil {
		nc.Active = false
	}
	delete(d.netErrs, nid)
	delete(d.retryPending, nid)
}

// Rejoin brings a left network's tunnel back up.
func (d *Daemon) Rejoin(nid string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	nc := d.cfg.Networks[nid]
	if nc == nil {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	if nc.Active {
		return fmt.Errorf("网络 %s 已激活", nid)
	}
	nc.Active = true
	if err := d.save(); err != nil {
		return err
	}
	if err := d.bringUp(nid); err != nil {
		log.Printf("bring up %s: %v", nid, err)
	}
	return nil
}

// Remove tears down the tunnel, deletes the node on the server, and forgets
// the network entirely.
func (d *Daemon) Remove(nid string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	nc := d.cfg.Networks[nid]
	if nc == nil {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	d.leaveLocked(nid)
	_ = d.apiLocked().RemoveNode(nid, nc.NodeID, nc.Token)
	delete(d.cfg.Networks, nid)
	return d.save()
}

// UpdateSettings updates the network name, subnet and/or join-approval
// requirement for a network this node owns. Changing the subnet re-allocates
// every member's IP on the server; this node itself picks the new IP up via
// the poll loop.
func (d *Daemon) UpdateSettings(nid, name, subnet string, approvalRequired *bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	if err := d.apiLocked().UpdateNetworkSettings(nid, nc.Token, name, subnet, approvalRequired); err != nil {
		return err
	}
	if name != "" {
		nc.Name = name
	}
	if subnet != "" {
		nc.Subnet = subnet
	}
	return d.save()
}

// ApprovePending approves a member's join request on a network this node owns.
func (d *Daemon) ApprovePending(nid, pendingID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	return d.apiLocked().ApprovePending(nid, nc.Token, pendingID)
}

// DenyPending rejects a member's join request on a network this node owns.
func (d *Daemon) DenyPending(nid, pendingID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	return d.apiLocked().DenyPending(nid, nc.Token, pendingID)
}

// DeleteNetwork deletes a network on the server and forgets it locally.
func (d *Daemon) DeleteNetwork(nid string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	if err := d.apiLocked().DeleteNetwork(nid, nc.Token); err != nil {
		return err
	}
	d.leaveLocked(nid)
	delete(d.cfg.Networks, nid)
	return d.save()
}

// Kick removes another member from a network this node owns.
func (d *Daemon) Kick(nid, nodeID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	return d.apiLocked().KickNode(nid, nc.Token, nodeID)
}

// ResetCode issues a fresh pairing code for a network this node owns.
func (d *Daemon) ResetCode(nid string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		return "", fmt.Errorf("网络 %s 未找到", nid)
	}
	return d.apiLocked().ResetCode(nid, nc.Token)
}

// Info fetches full network info from the server (owner details included).
func (d *Daemon) Info(nid string) (protocol.NetworkInfoResp, error) {
	d.mu.Lock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		d.mu.Unlock()
		return protocol.NetworkInfoResp{}, fmt.Errorf("网络 %s 未找到", nid)
	}
	api := d.apiLocked()
	token := nc.Token
	d.mu.Unlock()
	return api.NetworkInfo(nid, token)
}

// Peers returns the live peer list for a network. Unlike Info it works for
// any member of the network (the server's /peers endpoint is member-scoped),
// so non-owner clients can show a read-only member list.
func (d *Daemon) Peers(nid string) (protocol.PeersResp, error) {
	d.mu.Lock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		d.mu.Unlock()
		return protocol.PeersResp{}, fmt.Errorf("网络 %s 未找到", nid)
	}
	api := d.apiLocked()
	token := nc.Token
	d.mu.Unlock()
	return api.PeersState(nid, token)
}

// SetDeviceID replaces this machine's device identity (advanced; normally
// auto-generated and persisted at /usr/local/vnet/device.id).
func (d *Daemon) SetDeviceID(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg.DeviceID = id
	if d.deviceIDFile != "" {
		if err := writeDeviceIDFile(d.deviceIDFile, id); err != nil {
			return err
		}
	}
	return d.save()
}

// Claim adopts an ownerless (legacy) network as this device's owner.
func (d *Daemon) Claim(nid string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	if err := d.apiLocked().ClaimNetwork(nid, nc.Token, d.cfg.DeviceID); err != nil {
		return err
	}
	nc.Owner = true
	return d.save()
}

func writeDeviceIDFile(path, id string) error {
	return writeFile0600(path, []byte(id+"\n"))
}

// Close tears down every running tunnel without persisting config changes, so
// a later start restores the previously active networks. Used by graceful
// daemon shutdown.
func (d *Daemon) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.bindCheckStop != nil {
		close(d.bindCheckStop)
		d.bindCheckStop = nil
	}
	if d.retryStop != nil {
		close(d.retryStop)
		d.retryStop = nil
	}
	d.netErrs = make(map[string]string)
	d.retryPending = make(map[string]struct{})
	for nid := range d.nets {
		d.leaveLocked(nid)
	}
}

// Status snapshot for the control API.
func (d *Daemon) Status() (map[string]any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	nets := make([]map[string]any, 0, len(d.cfg.Networks))
	for nid, nc := range d.cfg.Networks {
		entry := map[string]any{
			"networkId": nid,
			"name":      nc.Name,
			"ip":        nc.IP,
			"subnet":    nc.Subnet,
			"port":      nc.Port,
			"active":    nc.Active,
			"owner":     nc.Owner,
			"error":     "",
			"interface": "",
			"peerStats": map[string]PeerStats{},
		}
		if rt := d.nets[nid]; rt != nil {
			entry["error"] = rt.err
			if rt.tun != nil {
				entry["interface"] = rt.tun.InterfaceName()
				if stats, err := rt.tun.Stats(); err == nil {
					entry["peerStats"] = stats
				}
			}
		}
		if entry["error"] == "" {
			if e, ok := d.netErrs[nid]; ok {
				entry["error"] = e
			}
		}
		nets = append(nets, entry)
	}
	pending := make([]map[string]any, 0, len(d.cfg.PendingJoins))
	for pid, pj := range d.cfg.PendingJoins {
		pending = append(pending, map[string]any{
			"pendingId": pid,
			"networkId": pj.NetworkID,
			"createdAt": pj.CreatedAt,
			"status":    pj.Status,
			"error":     pj.Err,
		})
	}
	return map[string]any{
		"deviceId":     d.cfg.DeviceID,
		"serverAddr":   d.cfg.ServerAddr,
		"bound":        d.cfg.Bound(),
		"wgPort":       d.cfg.WireguardPort,
		"networks":     nets,
		"pendingJoins": pending,
	}, nil
}
