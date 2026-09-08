package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"snet/internal/protocol"
)

// netGoneErr is returned when the server reports 404 for a network operation,
// indicating the network no longer exists on the server.
var netGoneErr = errors.New("该网络在服务端已不存在")

// shareOpt carries optional community-sharing metadata (description, tags,
// visibility) for network create/settings operations. description and
// visibility are pointers so "nil = leave unchanged" works for updates.
type shareOpt struct {
	description *string
	tags        []string
	visibility  *string
}

// ShareOpt builds a shareOpt for Create/UpdateSettings. Nil description or
// visibility means "leave unchanged"; an empty-string pointer clears the value.
func ShareOpt(description *string, tags []string, visibility *string) shareOpt {
	return shareOpt{description: description, tags: tags, visibility: visibility}
}

// wrapNetGone converts a server 404 error into netGoneErr so callers can
// present a clear "network gone" message to the user.
func wrapNetGone(err error) error {
	var se *httpStatusErr
	if errors.As(err, &se) && se.code == 404 {
		return netGoneErr
	}
	return err
}

// retryInterval is the base interval between tunnel re-creation attempts for
// a network whose initial bring-up failed (e.g. insufficient privileges to
// create a TUN device). The actual delay grows exponentially with each
// consecutive failure up to retryIntervalMax, so a server outage or
// permission fix pending a user action does not flood the log every 30s.
const (
	retryInterval    = 30 * time.Second
	retryIntervalMax = 30 * time.Minute
)

// nextRetryDelay returns the wait before the next attempt, doubling the
// previous wait up to retryIntervalMax. Consecutive successes reset the
// counter.
func nextRetryDelay(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	d := retryInterval
	for i := 0; i < attempts; i++ {
		d *= 2
		if d >= retryIntervalMax {
			return retryIntervalMax
		}
	}
	return d
}

// directGraceSec is how long the daemon tries a peer's direct endpoint
// (LAN/LocalEndpoint) before falling back to the network relay endpoint.
const directGraceSec = 20

// directRetrySec is how long the daemon stays on the relay endpoint for a
// peer before re-attempting a direct path.
const directRetrySec = 300

// candProbeSec is how long the daemon stays on one direct-endpoint candidate
// before rotating to the next. Behind a symmetric NAT the observed public port
// is usually off by a small delta per destination, so probing a small window
// of candidate ports (buildCandidates) raises the direct-hit rate.
const candProbeSec = 4

// netRuntime holds the live tunnel + control loops for one joined network.
type netRuntime struct {
	tun *Tunnel
	// peerDirect tracks the endpoint strategy per peer ("direct" vs
	// "relay") so the daemon can attempt direct paths and fall back to the
	// relay when a direct handshake does not complete in time.
	peerDirect map[string]*peerDirect
}

// peerDirect records the endpoint strategy for a single peer.
type peerDirect struct {
	mode     string // "direct" or "relay"
	dirSince int64  // unix seconds when the current direct attempt started
	lastDir  string // direct endpoint last attempted
	relay    string // network relay endpoint, for telemetry

	// Candidates for the current direct attempt: when the observed public
	// endpoint does not complete a handshake, the daemon rotates through a
	// small window of nearby ports. Can be empty (LAN peers etc.) meaning
	// "do not probe".
	cands     []string
	candIdx   int
	candStart int64 // unix second the current candidate attempt began
	baseHS    int64 // peer handshake second observed when probing started
	locked    bool  // a handshake succeeded on the current candidate; stop probing
}

// Daemon coordinates the local tunnels and the coordination server for any
// number of networks against a single server.
type Daemon struct {
	mu   sync.RWMutex
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
	// ctx/cancel scope the Daemon's lifetime; cancelling ctx aborts all
	// in-flight HTTP requests made through api.
	ctx    context.Context
	cancel context.CancelFunc
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
	// serverState records the server-side existence of each joined network.
	// Values: pointer to "ok"   = network exists on server
	//         pointer to "gone" = network no longer exists (deleted by owner)
	//         nil               = unknown (not yet probed)
	serverState map[string]*string
	// retryPending tracks networks whose tunnel creation failed; a background
	// loop retries them so transient failures (or a late privilege fix, e.g.
	// installing the root LaunchDaemon) self-heal without a daemon restart.
	retryPending map[string]struct{}
	// retryAttempts tracks how many consecutive failed retry attempts per
	// network, used to compute an exponential backoff. Cleared when a retry
	// succeeds or the network is left/removed.
	retryAttempts map[string]int
	// retryStop signals the background retry loop to exit.
	retryStop chan struct{}
	// androidTunFD holds the TUN file descriptor from VpnService on Android.
	// When non-zero, bringUp uses NewTunnelFromFD instead of NewTunnel.
	androidTunFD int
	// hostnameCache caches the machine hostname for device registration.
	hostnameCache string
	// keyRotationStop signals the periodic key-rotation goroutine to exit.
	keyRotationStop chan struct{}
	// keyRotating guards against concurrent rotations (loop tick vs manual
	// trigger). Guarded by d.mu.
	keyRotating bool
}

// defaultKeyRotationDays is assumed when KeyRotationDays is unset (<=0) but a
// caller explicitly enables rotation: 30 days is a sane forward-secrecy
// interval for home/lab meshes.
const defaultKeyRotationDays = 30

func NewDaemon(cfg *Config) *Daemon {
	return NewDaemonAt(cfg, "")
}

func NewDaemonAt(cfg *Config, configPath string) *Daemon {
	ctx, cancel := context.WithCancel(context.Background())
	return &Daemon{
		cfg:          cfg,
		configPath:   configPath,
		nets:         make(map[string]*netRuntime),
		pendingRuns:  make(map[string]chan struct{}),
		netErrs:      make(map[string]string),
		retryPending: make(map[string]struct{}),
		retryAttempts: make(map[string]int),
		deviceIDFile: DefaultDeviceIDFile,
		ctx:          ctx,
		cancel:       cancel,
	}
}

// SetDeviceIDFile overrides the on-disk device identity path.
func (d *Daemon) SetDeviceIDFile(path string) {
	d.deviceIDFile = path
}

// SetAndroidTunFD stores the TUN file descriptor from VpnService so bringUp
// can use NewTunnelFromFD instead of trying to create a TUN device directly.
// If running under Android with no fd, tunnel creation returns a clean
// "VPN TUN 未就绪" error and the retry loop waits for the fd. When the fd is
// (re)provided, any pending networks are brought up immediately rather than
// waiting for the next retry tick.
func (d *Daemon) SetAndroidTunFD(fd int) {
	d.mu.Lock()
	hadFD := d.androidTunFD != 0
	gotFD := fd != 0
	d.androidTunFD = fd
	// When the fd transitions from unavailable to available, bring up any
	// active networks that were waiting (their previous bringUp failed with a
	// clean "VPN TUN 未就绪" error and the fd is now ready).
	if gotFD && !hadFD {
		d.bringUpAllLocked()
	}
	d.mu.Unlock()
}

// bringUpAllLocked brings up tunnels for all active networks that do not yet
// have a live tunnel. Caller must hold d.mu.
func (d *Daemon) bringUpAllLocked() {
	for nid, nc := range d.cfg.Networks {
		if nc.Active && d.nets[nid] == nil {
			if err := d.bringUp(nid); err != nil {
				log.Printf("bring up %s on TUN ready: %v", nid, err)
			}
		}
	}
}

func (d *Daemon) save() error {
	if d.configPath != "" {
		return d.cfg.SaveAt(d.configPath)
	}
	return d.cfg.Save()
}

// SaveConfig persists the current daemon configuration to disk.
func (d *Daemon) SaveConfig() error {
	return d.save()
}

// Config returns the daemon's current configuration. The caller must not
// modify the returned pointer; it is a snapshot taken under the lock.
func (d *Daemon) Config() *Config {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cfg
}

func (d *Daemon) apiLocked() *apiClient {
	if d.api != nil && d.apiServer == d.cfg.ServerAddr && d.apiCA == d.cfg.ServerCAPath {
		return d.api
	}
	d.api = newAPIClient(d.cfg.ServerAddr, d.cfg.ServerCAPath, d.ctx)
	d.apiServer = d.cfg.ServerAddr
	d.apiCA = d.cfg.ServerCAPath
	return d.api
}

func (d *Daemon) publicKeyLocked() string {
	return pubKeyB64FromPrivHex(d.cfg.PrivateKey)
}

func (d *Daemon) hostname() string {
	d.mu.RLock()
	if d.hostnameCache != "" {
		v := d.hostnameCache
		d.mu.RUnlock()
		return v
	}
	d.mu.RUnlock()
	h, _ := os.Hostname()
	d.mu.Lock()
	d.hostnameCache = h
	d.mu.Unlock()
	return h
}

// hostnameLocked returns the hostname assuming the caller already holds d.mu.
func (d *Daemon) hostnameLocked() string {
	if d.hostnameCache != "" {
		return d.hostnameCache
	}
	h, _ := os.Hostname()
	d.hostnameCache = h
	return h
}

// SetHostname overrides the display name reported during device registration
// and binding. Intended for embedders (e.g. Android passes Build.MODEL since
// os.Hostname is generic there). Call before Start or any network activity.
func (d *Daemon) SetHostname(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hostnameCache = strings.TrimSpace(name)
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
		// Try to load the private key from the persistent device ID file
		// first (survives app reinstalls). Falls back to generating a new one.
		if _, persisted, err := LoadDeviceKeypair(d.deviceIDFile); err == nil && persisted != "" {
			d.cfg.PrivateKey = persisted
		} else {
			priv, _, err := GenerateKeyPair()
			if err != nil {
				return err
			}
			d.cfg.PrivateKey = b64ToHex(priv)
		}
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
	// Persist the private key alongside the device ID so it survives reinstalls.
	if d.deviceIDFile != "" {
		_ = SaveDeviceKeypair(d.deviceIDFile, d.cfg.DeviceID, d.cfg.PrivateKey)
	}
	return nil
}

// SetKeyRotationDays configures the automatic WireGuard key rotation interval
// in days (0 disables it) and (re)starts the background scheduler when enabled.
func (d *Daemon) SetKeyRotationDays(days int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if days <= 0 {
		d.cfg.KeyRotationDays = 0
		if d.keyRotationStop != nil {
			close(d.keyRotationStop)
			d.keyRotationStop = nil
		}
		return
	}
	d.cfg.KeyRotationDays = days
	d.startKeyRotationLocked()
}

// startKeyRotationLocked starts the daily key-rotation scheduler when it is
// not already running. Caller must hold d.mu.
func (d *Daemon) startKeyRotationLocked() {
	if d.keyRotationStop != nil {
		return
	}
	stop := make(chan struct{})
	d.keyRotationStop = stop
	go d.keyRotationLoop(stop)
}

// keyRotationLoop checks once a day whether the WireGuard keypair is due for
// rotation and rotates it when so. The anchor (LastKeyRotatedAt) persists, so
// restarting the daemon does not reset the schedule.
func (d *Daemon) keyRotationLoop(stop chan struct{}) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		d.mu.Lock()
		days := d.cfg.KeyRotationDays
		last := d.cfg.LastKeyRotatedAt
		hasKey := d.cfg.PrivateKey != ""
		d.mu.Unlock()
		if !hasKey || days <= 0 {
			continue
		}
		if last == 0 {
			// First daemon run with rotation enabled: anchor the schedule now
			// so an existing (possibly long-lived) key is not rotated
			// immediately on upgrade.
			d.mu.Lock()
			if d.cfg.LastKeyRotatedAt == 0 {
				d.cfg.LastKeyRotatedAt = time.Now().Unix()
				_ = d.save()
			}
			d.mu.Unlock()
			continue
		}
		due := time.Now().Unix()-last >= int64(days)*24*3600
		if !due {
			continue
		}
		if err := d.RotateKeys(); err != nil {
			log.Printf("key rotation: %v", err)
		}
	}
}

// RotateKeys rotates the daemon's WireGuard keypair and pushes the new public
// key to every active network's coordination server before switching the local
// key, so tunnels are rebuilt on the new identity. Rotation is atomic: if any
// network rejects the new key (stale token, unreachable server, incompatible
// version), the local key is left unchanged and the error is returned, so no
// network is ever broken by a half-applied rotation.
func (d *Daemon) RotateKeys() error {
	d.mu.Lock()
	if d.keyRotating {
		d.mu.Unlock()
		return fmt.Errorf("key rotation already in progress")
	}
	d.keyRotating = true
	serverAddr := d.cfg.ServerAddr
	serverCA := d.cfg.ServerCAPath
	type target struct {
		nid, nodeID, token, name string
	}
	var targets []target
	for nid, nc := range d.cfg.Networks {
		if nc.Active && nc.Token != "" {
			targets = append(targets, target{nid, nc.NodeID, nc.Token, nc.Name})
		}
	}
	deviceID := d.cfg.DeviceID
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		d.keyRotating = false
		d.mu.Unlock()
	}()

	priv, pub, err := GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("generate keypair: %w", err)
	}
	newPrivHex := b64ToHex(priv)

	api := newAPIClient(serverAddr, serverCA, d.ctx)
	for _, t := range targets {
		if err := api.UpdateNodePublicKeyByToken(t.nid, t.nodeID, t.token, pub); err != nil {
			return fmt.Errorf("rotate %s (%s): %w", t.nid, t.name, err)
		}
		log.Printf("key rotation: network %s (%s) now advertises the new public key", t.nid, t.name)
	}

	now := time.Now().Unix()
	d.mu.Lock()
	d.cfg.PrivateKey = newPrivHex
	d.cfg.LastKeyRotatedAt = now
	if d.deviceIDFile != "" {
		_ = SaveDeviceKeypair(d.deviceIDFile, deviceID, newPrivHex)
	}
	if err := d.save(); err != nil {
		d.mu.Unlock()
		return fmt.Errorf("save rotated key: %w", err)
	}
	// Rebuild tunnels so wireguard-go starts using the new keypair. Networking
	// glitches during this window self-heal via keepalives and the poll loop.
	for nid, nc := range d.cfg.Networks {
		if nc.Active {
			if err := d.bringUp(nid); err != nil {
				log.Printf("key rotation: rebuild %s: %v", nid, err)
			}
		}
	}
	d.mu.Unlock()
	log.Printf("key rotation: new WireGuard keypair active (%d network(s) updated)", len(targets))
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
		delete(d.nets, nid)
	}
	for pid, stop := range d.pendingRuns {
		close(stop)
		delete(d.pendingRuns, pid)
	}
	d.netErrs = make(map[string]string)
	d.retryPending = make(map[string]struct{})
	d.retryAttempts = make(map[string]int)
	d.serverState = make(map[string]*string)
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
	var se *httpStatusErr
	return errors.As(err, &se) && se.code == 403
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

	bound, err := api.RegisterDevice(deviceID, pub, d.hostname())
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
// Optional share metadata (description, tags, visibility) may be passed via
// the variadic shareOpt, keeping existing callers source-compatible.
func (d *Daemon) Create(serverAddr string, port int, name, subnet string, approvalRequired bool, opts ...shareOpt) (protocol.CreateNetworkResp, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var so shareOpt
	if len(opts) > 0 {
		so = opts[0]
	}

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
	desc, vis := "", ""
	if so.description != nil {
		desc = *so.description
	}
	if so.visibility != nil {
		vis = *so.visibility
	}
	resp, err := api.CreateNetwork(d.publicKeyLocked(), d.cfg.DeviceID, name, subnet, approvalRequired, desc, so.tags, vis)
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
// bind leaves them untouched. After a successful bind, the device's historical
// networks are synced from the server (reinstall recovery).
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
	resp, err := api.BindDevice(d.cfg.DeviceID, d.publicKeyLocked(), code, d.hostnameLocked())
	if err != nil {
		d.rollbackSwitchLocked(sw)
		return err
	}
	d.commitSwitchLocked(sw)
	d.cfg.BoundServer = normalizeServer(d.cfg.ServerAddr)
	if err := d.save(); err != nil {
		return err
	}
	d.startBindCheckLocked()
	// Sync historical networks from the server (reinstall recovery).
	if resp.DeviceToken != "" {
		// Unlock before SyncNetworks since it acquires d.mu internally.
		d.mu.Unlock()
		_ = d.SyncNetworks(resp.DeviceToken)
		d.mu.Lock()
	}
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
			err := d.attach(status.NetworkID, pj.Name, status.NodeID, status.IP, status.Token, pj.Code, status.Subnet, false)
			if err != nil {
				log.Printf("join approved %s: %v", pendingID, err)
				d.mu.Unlock()
				return
			}
			delete(d.cfg.PendingJoins, pendingID)
			delete(d.pendingRuns, pendingID)
			_ = d.save()
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
		if other != nid && subnetsOverlap(subnet, oc.Subnet) {
			return fmt.Errorf("网段 %s 与已加入网络 %s（%s）冲突，无法加入", subnet, other, oc.Subnet)
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
		JoinedAt:    time.Now().UTC().Format(time.RFC3339),
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
	if _, err := api.RegisterDevice(d.cfg.DeviceID, d.publicKeyLocked(), d.hostnameLocked()); err != nil {
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

// SyncNetworks fetches the device's historical networks from the server and
// reconstructs the local config. Used after a reinstall when the DeviceID
// persists but the local config (networks, tokens, private key) is lost.
// The deviceToken authenticates the request. Errors are logged but non-fatal.
func (d *Daemon) SyncNetworks(deviceToken string) error {
	d.mu.Lock()
	api := d.apiLocked()
	deviceID := d.cfg.DeviceID
	d.mu.Unlock()

	if deviceID == "" || deviceToken == "" {
		return nil
	}

	details, err := api.DeviceNetworks(deviceID, deviceToken)
	if err != nil {
		log.Printf("sync networks: fetch device networks: %v", err)
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	api = d.apiLocked()
	pub := d.publicKeyLocked()

	// Register this device with its current (possibly new) public key.
	if _, err := api.RegisterDevice(deviceID, pub, d.hostnameLocked()); err != nil {
		log.Printf("sync networks: register device: %v", err)
	}

	synced := 0
	for _, detail := range details {
		nid := detail.Network.ID

		// Skip networks already present locally.
		if _, ok := d.cfg.Networks[nid]; ok {
			continue
		}

		// Update the node's WireGuard public key on the server to match
		// the current (new) keypair.
		if detail.PublicKey != pub {
			if err := api.UpdateNodePublicKey(nid, detail.NodeID, deviceID, deviceToken, pub); err != nil {
				log.Printf("sync networks: update public key %s: %v", nid, err)
			}
		}

		// Build local network config from server data.
		if d.cfg.Networks == nil {
			d.cfg.Networks = map[string]*NetworkCfg{}
		}
		// Check for subnet overlap with already-restored networks.
		active := true
		for other, oc := range d.cfg.Networks {
			if other != nid && subnetsOverlap(detail.Subnet, oc.Subnet) {
				log.Printf("sync networks: subnet %s overlaps with %s (%s), activating %s as inactive", detail.Subnet, other, oc.Subnet, nid)
				active = false
				break
			}
		}
		nc := &NetworkCfg{
			Name:     detail.Name,
			NodeID:   detail.NodeID,
			IP:       detail.IP,
			Token:    detail.Token,
			Subnet:   detail.Subnet,
			Port:     0,
			Active:   active,
			Owner:    detail.Owner,
			JoinedAt: time.Now().UTC().Format(time.RFC3339),
		}
		d.cfg.Networks[nid] = nc

		// Bind the node to this device on the server.
		if err := api.SetNodeDevice(nid, detail.NodeID, detail.Token, deviceID); err != nil {
			log.Printf("sync networks: bind node device %s: %v", nid, err)
		}

		// Try to claim ownership if the server says we're the owner.
		if detail.Owner {
			if err := api.ClaimNetwork(nid, detail.Token, deviceID); err != nil {
				log.Printf("sync networks: claim %s: %v", nid, err)
			}
		}

		synced++
	}

	if synced > 0 {
		if err := d.save(); err != nil {
			log.Printf("sync networks: save: %v", err)
		}
		// Bring up tunnels for newly added networks.
		for nid, nc := range d.cfg.Networks {
			if nc.Active && d.nets[nid] == nil {
				if err := d.bringUp(nid); err != nil {
					log.Printf("sync networks: bring up %s: %v", nid, err)
				}
			}
		}
		log.Printf("sync networks: restored %d network(s)", synced)
	}

	return nil
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
	// Try to restore private key from the persistent device file if missing.
	if d.cfg.PrivateKey == "" {
		if _, key, err := LoadDeviceKeypair(d.deviceIDFile); err == nil && key != "" {
			d.cfg.PrivateKey = key
			if err := d.save(); err != nil {
				log.Printf("start: save restored key: %v", err)
			}
		}
	}
	// Enable IP forwarding at startup if any active network has subnets
	// configured, so the setting is restored after a reboot.
	for nid := range d.cfg.Networks {
		nc := d.cfg.Networks[nid]
		if nc.Active && len(nc.AllowedSubnets) > 0 {
			if err := enableIPForwarding(); err != nil {
				log.Printf("enable IP forwarding: %v (needs admin/root)", err)
			}
			break
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
	// Periodic WireGuard key rotation (KeyRotationDays > 0) for forward secrecy.
	if d.cfg.KeyRotationDays > 0 {
		d.startKeyRotationLocked()
	}
	// One-shot startup probe of every joined network so the UI can render a
	// "已删除" badge for networks that the owner has removed from the server.
	// Run asynchronously so daemon startup is not blocked on the server.
	go d.pingAllServerStates()
	return nil
}

// BringUpActive brings up tunnels for all active networks. Used on Android
// when the TUN fd becomes available after the daemon was already created.
func (d *Daemon) BringUpActive() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for nid, nc := range d.cfg.Networks {
		if nc.Active && d.nets[nid] == nil {
			if err := d.bringUp(nid); err != nil {
				log.Printf("bring up active %s: %v", nid, err)
			}
		}
	}
}

func (d *Daemon) bringUp(nid string) error {
	nc := d.cfg.Networks[nid]
	if nc == nil {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	// Clean up routes from the old tunnel before closing it.
	if rt := d.nets[nid]; rt != nil {
		if rt.tun != nil {
			rt.tun.RemoveAllPeers()
			rt.tun.Close()
		}
		delete(d.nets, nid)
	}
	if nc.Port == 0 {
		p, err := d.pickPortLocked()
		if err != nil {
			d.netErrs[nid] = err.Error()
			d.scheduleRetryLocked(nid)
			return fmt.Errorf("pick port: %w", err)
		}
		nc.Port = p
	}
	t, err := createTunnelForPlatform(d.androidTunFD, d.cfg.PrivateKey, nc.IP, nc.Port, protocol.DefaultMTU)
	if err != nil {
		d.netErrs[nid] = err.Error()
		d.scheduleRetryLocked(nid)
		return fmt.Errorf("tunnel: %w", err)
	}
	delete(d.netErrs, nid)
	rt := &netRuntime{tun: t, peerDirect: make(map[string]*peerDirect)}
	d.nets[nid] = rt

	// Enable IP forwarding if this device advertises subnets.
	if len(nc.AllowedSubnets) > 0 {
		if err := enableIPForwarding(); err != nil {
			log.Printf("enable IP forwarding: %v (needs admin/root)", err)
		}
	}

	endpoint, err := d.localEndpointLocked(nc.Port)
	if err != nil {
		log.Printf("endpoint detect failed: %v", err)
	} else {
		// Advertise the LAN/NAT-internal endpoint as the direct candidate so
		// same-subnet peers can reach this device without the relay.
		if err := d.apiLocked().SetEndpointFor(nid, nc.NodeID, nc.Token, endpoint, endpoint); err != nil {
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
	if d.retryAttempts == nil {
		d.retryAttempts = make(map[string]int)
	}
	d.retryPending[nid] = struct{}{}
	if d.retryStop == nil {
		stop := make(chan struct{})
		d.retryStop = stop
		go d.retryLoop(stop)
	}
}

// retryLoop re-attempts tunnel creation for failed networks until they come up
// or are left/removed. The wait between attempts grows exponentially per
// network (capped at retryIntervalMax) so a long server outage does not
// flood logs. The loop stops itself once nothing remains to retry.
func (d *Daemon) retryLoop(stop chan struct{}) {
	for {
		d.mu.Lock()
		// Find the soonest next attempt time across pending networks.
		var nextDelay time.Duration
		hasPending := false
		for nid := range d.retryPending {
			delay := nextRetryDelay(d.retryAttempts[nid])
			if !hasPending || delay < nextDelay {
				nextDelay = delay
				hasPending = true
			}
		}
		if !hasPending {
			// No pending networks: exit. Only close d.retryStop if we are
			// still the registered loop (the stop channel is still set).
			if d.retryStop != nil {
				close(d.retryStop)
				d.retryStop = nil
			}
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()

		timer := time.NewTimer(nextDelay)
		select {
		case <-timer.C:
		case <-stop:
			timer.Stop()
			return
		}

		d.mu.Lock()
		for nid := range d.retryPending {
			nc := d.cfg.Networks[nid]
			if nc == nil || !nc.Active || d.nets[nid] != nil {
				delete(d.retryPending, nid)
				delete(d.retryAttempts, nid)
				continue
			}
			if err := d.bringUp(nid); err != nil {
				d.retryAttempts[nid]++
				log.Printf("retry bring up %s (attempt %d): %v", nid, d.retryAttempts[nid], err)
			} else {
				delete(d.retryPending, nid)
				delete(d.retryAttempts, nid)
			}
		}
		d.mu.Unlock()
	}
}

// pickPortLocked finds a free UDP port, preferring the configured base.
//
// The scan covers several contiguous 64-port bands so a client that is
// co-located with the coordination server can still find a port even when the
// server's relay pool (usually DefaultWGPort..DefaultWGPort+64, bound by the
// relay) fully occupies the first band. Falling through to later bands keeps
// the client's WireGuard port disjoint from the server's relay ports.
func (d *Daemon) pickPortLocked() (int, error) {
	base := d.cfg.WireguardPort
	if base == 0 {
		base = protocol.DefaultWGPort
	}
	const band = 64
	const bands = 4
	for b := 0; b < bands; b++ {
		start := base + b*band
		for p := start; p < start+band; p++ {
			if portFree(p) {
				return p, nil
			}
		}
	}
	return 0, fmt.Errorf("no free UDP port in range %d-%d", base, base+bands*band-1)
}

func (d *Daemon) localEndpointLocked(port int) (string, error) {
	ip, err := LocalIPForServer(d.cfg.ServerAddr)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(ip, fmt.Sprint(port)), nil
}

// resolvePeerEndpoints picks, per peer, the endpoint to use. With relay
// enabled, a peer whose LocalEndpoint shares our LAN subnet is tried directly
// first and falls back to the network relay endpoint when no WireGuard
// handshake completes within directGraceSec; peers stay on the relay until
// directRetrySec elapses, then retry the direct path. Without relay the
// peer's self-advertised public endpoint is used as before.
//
// Behind a symmetric NAT the public port observed by the probe often differs
// from the port the peer's WireGuard socket actually uses toward us; while in
// direct mode the daemon therefore rotates through a small window of nearby
// candidate ports (buildCandidates) until a handshake completes, improving the
// direct-hit rate before falling back to the relay.
// Caller must hold no lock; the reply is a fresh slice.
func (d *Daemon) resolvePeerEndpoints(nid string, rt *netRuntime, st protocol.PeersResp, localSubs []string) []protocol.Node {
	out := make([]protocol.Node, len(st.Peers))
	copy(out, st.Peers)
	stats, _ := rt.tun.Stats()
	now := time.Now().Unix()
	for i := range out {
		p := &out[i]
		// Choose the best direct candidate for this peer.
		direct := ""
		if st.RelayEndpoint == "" {
			direct = p.Endpoint
		} else {
			direct = endpointIfLocal(p, localSubs)
		}
		st2 := rt.peerDirect[p.ID]
		if st2 == nil {
			// First contact: prefer direct when a candidate exists.
			st2 = &peerDirect{mode: "relay", lastDir: direct, dirSince: now}
			if direct != "" {
				st2.mode = "direct"
			}
			rt.peerDirect[p.ID] = st2
		}
		st2.relay = st.RelayEndpoint
		if st2.mode == "relay" {
			// Periodically retry the direct path.
			if st.RelayEndpoint != "" && direct != "" && now-st2.dirSince >= directRetrySec {
				st2.mode = "direct"
				st2.dirSince = now
				st2.lastDir = direct
				st2.cands = nil
				log.Printf("path %s: peer %s %s -> direct (%s)", nid, p.ID, "relay", direct)
			}
		}
		if st2.mode == "direct" {
			switch {
			case direct == "":
				// Candidate disappeared; fall back to the relay.
				if st.RelayEndpoint != "" {
					log.Printf("path %s: peer %s direct -> relay (no direct candidate)", nid, p.ID)
				}
				st2.mode = "relay"
				st2.dirSince = now
			case st2.lastDir != direct:
				// Direct candidate changed; restart the direct timer and the
				// candidate probe window.
				st2.lastDir = direct
				st2.dirSince = now
				st2.cands = nil
			}
		}
		if st2.mode == "direct" && direct != "" {
			hs := stats[p.PublicKey].LastHandshakeSec
			// (Re)build the candidate window when it is missing or the direct
			// candidate changed. baseHS anchors "was there a new handshake
			// since probing started" so a relay-era handshake cannot falsely
			// lock a direct candidate.
			if len(st2.cands) == 0 {
				st2.cands = buildCandidates(direct)
				st2.candIdx = 0
				st2.candStart = now
				st2.baseHS = hs
				st2.locked = false
			}
			if !st2.locked && hs > st2.baseHS {
				st2.locked = true
				log.Printf("path %s: peer %s direct handshake via %s (candidate %d/%d)", nid, p.ID, direct, st2.candIdx+1, len(st2.cands))
			}
			if !st2.locked && now-st2.candStart >= candProbeSec {
				if st2.candIdx+1 < len(st2.cands) {
					st2.candIdx++
					st2.candStart = now
					st2.baseHS = hs
					log.Printf("path %s: peer %s probing direct candidate %d/%d (%s)", nid, p.ID, st2.candIdx+1, len(st2.cands), st2.cands[st2.candIdx])
				}
			}
			if st2.candIdx < len(st2.cands) {
				p.Endpoint = st2.cands[st2.candIdx]
			}
			// Fall back to the relay when the direct budget is exhausted or
			// every candidate has been tried without a handshake.
			candsDone := !st2.locked && st2.candIdx+1 >= len(st2.cands) && now-st2.candStart >= candProbeSec
			if st.RelayEndpoint != "" && !st2.locked && (now-st2.dirSince >= directGraceSec || candsDone) {
				log.Printf("path %s: peer %s direct -> relay (no direct handshake in %ds)", nid, p.ID, now-st2.dirSince)
				st2.mode = "relay"
				st2.dirSince = now
			}
		}
		if st2.mode == "relay" && st.RelayEndpoint != "" {
			p.Endpoint = st.RelayEndpoint
		}
	}
	return out
}

// buildCandidates expands a direct endpoint into the ordered set of endpoints
// to probe. LAN/private endpoints are tried as-is (no probing window: they are
// either reachable or not, and a wrong port on the local segment buys nothing).
// Public endpoints get the observed port first, then a deterministic window of
// nearby ports (NATs tend to allocate consecutive ports per destination), so
// a symmetric-NAT peer whose real port is offset from the probe-observed one
// is still reached directly.
func buildCandidates(direct string) []string {
	host, port, err := net.SplitHostPort(direct)
	if err != nil {
		return []string{direct}
	}
	base, err := strconv.Atoi(port)
	if err != nil || base <= 0 || base > 65535 {
		return []string{direct}
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		return []string{direct}
	}
	out := make([]string, 0, 1+2*maxPortDelta)
	out = append(out, direct)
	for delta := 1; delta <= maxPortDelta; delta++ {
		for _, d := range []int{-delta, delta} {
			cand := base + d
			if cand < 1 || cand > 65535 {
				continue
			}
			out = append(out, net.JoinHostPort(host, fmt.Sprint(cand)))
		}
	}
	return out
}

// maxPortDelta bounds how far from the observed port the candidate window
// reaches. Eight in each direction (17 candidates total, ~4s each ≈ 68s worst
// case) is generous for consecutive-allocating NATs while keeping the probe
// burst tame; directGraceSec caps the overall direct budget anyway.
const maxPortDelta = 8

// endpointIfLocal returns the peer's local endpoint when its IP falls inside
// one of our own private LAN subnets (same-segment direct path), and empty
// otherwise.
func endpointIfLocal(p *protocol.Node, localSubs []string) string {
	if p.LocalEndpoint == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(p.LocalEndpoint)
	if err != nil {
		return ""
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	for _, sub := range localSubs {
		_, n, err := net.ParseCIDR(sub)
		if err != nil {
			continue
		}
		if n.Contains(ip) {
			return p.LocalEndpoint
		}
	}
	return ""
}

// pollIntervalFor returns the peer poll interval scaled by network count.
// Base is 2 s; each extra network adds 0.5 s, capped at 10 s.
func pollIntervalFor(n int) time.Duration {
	if n <= 1 {
		return protocol.PollIntervalSeconds * time.Second
	}
	dur := time.Duration(int(protocol.PollIntervalSeconds*10)+5*(n-1)) * 100 * time.Millisecond
	if dur > 10*time.Second {
		dur = 10 * time.Second
	}
	return dur
}

func (d *Daemon) pollLoop(nid string) {
	n := len(d.cfg.Networks)
	if n < 1 {
		n = 1
	}
	ticker := time.NewTicker(pollIntervalFor(n))
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
			// Warn if the new subnet overlaps with another network on this device.
			for other, oc := range d.cfg.Networks {
				if other != nid && subnetsOverlap(nc.Subnet, oc.Subnet) {
					log.Printf("WARNING: network %s subnet %s overlaps with %s (%s)", nid, nc.Subnet, other, oc.Subnet)
					break
				}
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
			// Warn if the new subnet overlaps with another network on this device.
			for other, oc := range d.cfg.Networks {
				if other != nid && subnetsOverlap(st.Subnet, oc.Subnet) {
					log.Printf("WARNING: network %s subnet %s overlaps with %s (%s)", nid, st.Subnet, other, oc.Subnet)
					break
				}
			}
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
		peers := d.resolvePeerEndpoints(nid, rt, st, d.DetectLocalSubnets())
		for i := range peers {
			log.Printf("poll peers %s: peer %d id=%s pub=%s endpoint=%s local=%s online=%v", nid, i, peers[i].ID, peers[i].PublicKey, peers[i].Endpoint, peers[i].LocalEndpoint, peers[i].Online)
		}
		if len(peers) == 0 {
			log.Printf("poll peers %s: got 0 peers", nid)
		}
		if err := rt.tun.ApplyPeers(peers, nc.Subnet, st.RelayEndpoint); err != nil {
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
	lastLocal := ""
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

		localEP, _ := d.localEndpointLocked(port)
		ip, err := probePublicIP(serverProbeAddr(serverAddr), nid, nc.NodeID, nc.Token)
		if err != nil {
			log.Printf("probe public IP %s: %v", nid, err)
			continue
		}
		if ip == lastIP && localEP == lastLocal {
			continue
		}
		lastIP = ip
		lastLocal = localEP
		ep := net.JoinHostPort(ip, fmt.Sprint(port))
		if err := newAPIClient(serverAddr, serverCA, d.ctx).SetEndpointFor(nid, nc.NodeID, nc.Token, ep, localEP); err != nil {
			log.Printf("set public endpoint %s: %v", nid, err)
			continue
		}
		log.Printf("public endpoint %s: %s (local %s)", nid, ep, localEP)
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
		delete(d.nets, nid)
	}
	if nc := d.cfg.Networks[nid]; nc != nil {
		nc.Active = false
	}
	delete(d.netErrs, nid)
	delete(d.retryPending, nid)
	delete(d.retryAttempts, nid)
}

// Rejoin brings a network's tunnel back up.
func (d *Daemon) Rejoin(nid string) error {
	d.mu.Lock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		d.mu.Unlock()
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	// Read current state before we release the lock so we can decide whether
	// to probe. nil = unknown, &stateGone = gone, &stateOK = ok.
	currentState := d.serverState[nid]
	d.mu.Unlock()
	// Lazy probe: if the network was previously unknown or ok, check the server
	// now so the user cannot accidentally re-join a deleted network. Run it in
	// the background: over a slow/jittery physical link this HTTP round trip is
	// the dominant latency of Rejoin, and blocking the caller (a UI thread on
	// mobile) made the toggle feel unresponsive. The result lands in
	// d.serverState and is re-checked below; if the probe is still in flight
	// when we reach that check we optimistically proceed and re-check on the
	// next status render.
	if currentState == nil || *currentState == "ok" {
		done := make(chan struct{})
		go func() {
			defer close(done)
			d.pingServerState(nid)
		}()
		// Give the probe a bounded window so a slow network constrains the
		// wait instead of the full HTTP client timeout. This keeps the toggle
		// snappy while still catching a definitively-deleted network quickly.
		select {
		case <-done:
		case <-time.After(300 * time.Millisecond):
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	nc = d.cfg.Networks[nid]
	if nc == nil {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	if d.serverState[nid] != nil && *d.serverState[nid] == "gone" {
		return netGoneErr
	}
	// If already active but tunnel is missing (e.g. VPN service restarted),
	// re-bring-up instead of refusing.
	if nc.Active {
		if rt := d.nets[nid]; rt != nil && rt.tun != nil {
			return fmt.Errorf("网络 %s 已激活", nid)
		}
		// Tunnel lost — bring it up again.
		if err := d.bringUp(nid); err != nil {
			log.Printf("rejoin bring up %s: %v", nid, err)
		}
		return nil
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
	if err := d.apiLocked().RemoveNode(nid, nc.NodeID, nc.Token); err != nil {
		// 404: the node was already gone on the server (network deleted by owner).
		// 401: the node was kicked and its token invalidated — server-side cleanup
		// already happened. In either case proceed with local cleanup. Any other
		// error (network unreachable, 403, 500, ...) means the server could not
		// process the removal; keep the local config and surface the error so
		// the caller can retry.
		if se, ok := err.(*httpStatusErr); !ok || (se.code != 404 && se.code != 401) {
			return fmt.Errorf("remove node %s from server: %w", nid, err)
		}
	}
	delete(d.cfg.Networks, nid)
	delete(d.serverState, nid)
	return d.save()
}

// UpdateSettings updates the network name, subnet and/or join-approval
// requirement for a network this node owns. Changing the subnet re-allocates
// every member's IP on the server; this node itself picks the new IP up via
// the poll loop.
func (d *Daemon) UpdateSettings(nid, name, subnet string, approvalRequired *bool, opts ...shareOpt) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	var so shareOpt
	if len(opts) > 0 {
		so = opts[0]
	}
	if subnet != "" && subnet != nc.Subnet {
		for other, oc := range d.cfg.Networks {
			if other != nid && subnetsOverlap(subnet, oc.Subnet) {
				return fmt.Errorf("网段 %s 与已加入网络 %s（%s）冲突", subnet, other, oc.Subnet)
			}
		}
	}
	if err := d.apiLocked().UpdateNetworkSettings(nid, nc.Token, name, subnet, approvalRequired, so.description, so.tags, so.visibility); err != nil {
		return wrapNetGone(err)
	}
	if name != "" {
		nc.Name = name
	}
	if subnet != "" {
		nc.Subnet = subnet
	}
	return d.save()
}

// SetNodeRole updates a peer node's role within a network this node owns.
func (d *Daemon) SetNodeRole(nid, nodeID, role string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	if err := d.apiLocked().SetNodeRole(nid, nc.Token, nodeID, role); err != nil {
		return wrapNetGone(err)
	}
	return nil
}

// UpdateSubnets replaces the CIDR subnets this device advertises for routing
// on the given network. Other peers will route traffic for these subnets
// through this device's tunnel.
func (d *Daemon) UpdateSubnets(nid string, subnets []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		return fmt.Errorf("网络 %s 未找到", nid)
	}
	if err := d.apiLocked().UpdateSubnets(nid, nc.Token, subnets); err != nil {
		return wrapNetGone(err)
	}
	nc.AllowedSubnets = subnets
	if err := d.save(); err != nil {
		return err
	}
	// If subnets were added, try to enable IP forwarding (best-effort).
	if len(subnets) > 0 {
		if err := enableIPForwarding(); err != nil {
			log.Printf("enable IP forwarding: %v (needs admin/root)", err)
		}
	}
	return nil
}

// DetectLocalSubnets enumerates network interfaces and returns the private
// IPv4 CIDR subnets (e.g. "192.168.1.0/24") this device is directly on.
// Virtual / tunnel interfaces are excluded.
func (d *Daemon) DetectLocalSubnets() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var result []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagPointToPoint != 0 {
			continue
		}
		if isVirtualIface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}
			ip := ipNet.IP.To4()
			if ip[0] == 127 {
				continue
			}
			if !privateIPv4(ip) {
				continue
			}
			cidr := ipNetCIDR(ipNet)
			if cidr == "" {
				continue
			}
			if _, dup := seen[cidr]; dup {
				continue
			}
			seen[cidr] = struct{}{}
			result = append(result, cidr)
		}
	}
	return result
}

// isVirtualIface returns true for names known to be VPN tunnels or virtual
// bridges that should not be advertised as routable LAN subnets.
func isVirtualIface(name string) bool {
	n := strings.ToLower(name)
	prefixes := []string{"utun", "wg", "tun", "tap", "docker", "br-", "veth", "virbr", "lo"}
	for _, p := range prefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return n == "docker0" || n == "lo0" || n == "lo"
}

// privateIPv4 reports whether ip is in a private IPv4 range (10/8, 172.16/12,
// 192.168/16).
func privateIPv4(ip net.IP) bool {
	if len(ip) != 4 {
		return false
	}
	if ip[0] == 10 {
		return true
	}
	if ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31 {
		return true
	}
	if ip[0] == 192 && ip[1] == 168 {
		return true
	}
	return false
}

// ipNetCIDR returns the CIDR string for an IPNet, computing the network base
// address from the mask.  Example: 192.168.1.100/255.255.255.0 -> "192.168.1.0/24".
func ipNetCIDR(n *net.IPNet) string {
	ip := n.IP.To4()
	if ip == nil {
		return ""
	}
	mask := n.Mask
	ones, bits := mask.Size()
	if bits != 32 {
		return ""
	}
	base := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		base[i] = ip[i] & mask[i]
	}
	return fmt.Sprintf("%s/%d", base.String(), ones)
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
		return wrapNetGone(err)
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
	return wrapNetGone(d.apiLocked().KickNode(nid, nc.Token, nodeID))
}

// ResetCode issues a fresh pairing code for a network this node owns.
func (d *Daemon) ResetCode(nid string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		return "", fmt.Errorf("网络 %s 未找到", nid)
	}
	code, err := d.apiLocked().ResetCode(nid, nc.Token)
	return code, wrapNetGone(err)
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
	resp, err := api.NetworkInfo(nid, token)
	return resp, wrapNetGone(err)
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

// Pre-allocated string values so serverState can use *string to distinguish
// nil (unknown) from a real status, without allocating fresh strings on every
// probe.
var (
	stateOK   = "ok"
	stateGone = "gone"
)

// pingServerState probes whether the given network still exists on the
// server. The result is cached in d.serverState so the UI can render a
// "已删除" badge without having to re-probe on every status request. A 404
// from the server marks the network as gone; any other error (including
// transient network errors) leaves the previous state unchanged so we don't
// flap between ok/gone.
func (d *Daemon) pingServerState(nid string) {
	d.mu.Lock()
	nc := d.cfg.Networks[nid]
	if nc == nil {
		d.mu.Unlock()
		return
	}
	api := d.apiLocked()
	token := nc.Token
	d.mu.Unlock()

	_, err := api.NetworkExists(nid, token)
	var se *httpStatusErr
	gone := errors.As(err, &se) && se.code == 404
	if !gone && err != nil {
		return
	}
	d.mu.Lock()
	if d.serverState == nil {
		d.serverState = make(map[string]*string)
	}
	if gone {
		d.serverState[nid] = &stateGone
	} else {
		d.serverState[nid] = &stateOK
	}
	d.mu.Unlock()
}

// pingAllServerStates probes every joined network once. Intended to run at
// daemon startup and after the user explicitly asks for a refresh; it does
// not run on a timer.
func (d *Daemon) pingAllServerStates() {
	d.mu.Lock()
	nids := make([]string, 0, len(d.cfg.Networks))
	for nid := range d.cfg.Networks {
		nids = append(nids, nid)
	}
	d.mu.Unlock()
	for _, nid := range nids {
		d.pingServerState(nid)
	}
}

// SetDeviceID replaces this machine's device identity (advanced; normally
// auto-generated and persisted at /usr/local/snet/device.id).
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
		return wrapNetGone(err)
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
	d.cancel()
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
	if d.keyRotationStop != nil {
		close(d.keyRotationStop)
		d.keyRotationStop = nil
	}
	d.netErrs = make(map[string]string)
	d.retryPending = make(map[string]struct{})
	d.retryAttempts = make(map[string]int)
	d.serverState = make(map[string]*string)
	for nid := range d.nets {
		d.leaveLocked(nid)
	}
}

// HaltTunnels tears down every tunnel but keeps the daemon and the configured
// networks (including their Active flag) alive. Used on Android when the VPN
// service is stopped: the state-bar indicator must disappear and the TUN must
// be released, but the daemon stays resident so a subsequent toggle re-uses the
// already-loaded config and bound server connection instead of a full cold
// start. Unlike leaveLocked this leaves nc.Active untouched so BringUpActive
// can restore exactly the tunnels that were up before the halt.
func (d *Daemon) HaltTunnels() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for nid := range d.nets {
		rt := d.nets[nid]
		if rt != nil {
			if rt.tun != nil {
				rt.tun.RemoveAllPeers()
				rt.tun.Close()
			}
			delete(d.nets, nid)
		}
	}
}

// peerPathInfo exposes, per peer, the current routing path (direct vs relay),
// the mode duration, the direct candidate being tried, the relay fallback, and
// candidate-probe progress. It renders the daemon's path decisions observable
// so misrouting through a remote relay is diagnosable. Caller must hold d.mu.
func peerPathInfo(rt *netRuntime) map[string]any {
	out := make(map[string]any, len(rt.peerDirect))
	for id, pd := range rt.peerDirect {
		info := map[string]any{
			"mode":  pd.mode,
			"since": pd.dirSince,
			"direct": pd.lastDir,
			"relay": pd.relay,
		}
		if len(pd.cands) > 0 {
			info["candidates"] = len(pd.cands)
			info["candidateIdx"] = pd.candIdx + 1
		}
		info["locked"] = pd.locked
		out[id] = info
	}
	return out
}

// Status snapshot for the control API.
func (d *Daemon) Status() (map[string]any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	nets := make([]map[string]any, 0, len(d.cfg.Networks))
	for nid, nc := range d.cfg.Networks {
		statePtr := d.serverState[nid]
		active := nc.Active
		// A "gone" network cannot be linked; reflect that in the active flag
		// so the UI toggle is disabled without having to teach the renderer
		// about serverState first.
		if statePtr != nil && *statePtr == "gone" {
			active = false
		}
		entry := map[string]any{
			"networkId":      nid,
			"name":           nc.Name,
			"ip":             nc.IP,
			"subnet":         nc.Subnet,
			"port":           nc.Port,
			"active":         active,
			"owner":          nc.Owner,
			"serverState":    statePtr,
			"error":          "",
			"interface":      "",
			"peerStats":      map[string]PeerStats{},
			"allowedSubnets": nc.AllowedSubnets,
			"joinedAt":       nc.JoinedAt,
		}
		if rt := d.nets[nid]; rt != nil {
			if rt.tun != nil {
				entry["interface"] = rt.tun.InterfaceName()
				if stats, err := rt.tun.Stats(); err == nil {
					entry["peerStats"] = stats
				}
			}
			entry["peerPaths"] = peerPathInfo(rt)
		}
		if statePtr != nil && *statePtr == "gone" {
			entry["error"] = netGoneErr.Error()
		} else if e, ok := d.netErrs[nid]; ok {
			entry["error"] = e
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
