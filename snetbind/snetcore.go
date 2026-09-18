// Package snetbind provides a gomobile-friendly binding layer for the SNET
// daemon. All complex data is exchanged as JSON strings to work around
// gomobile's type restrictions.
//
// Build: gomobile bind -target=android -o snet.aar ./snetbind/
package snetbind

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"snet/internal/client"
	"snet/internal/protocol"
)

// SnetCore is the main entry point for Android. All public methods are
// designed to be callable from gomobile/JNI.
type SnetCore struct {
	mu           sync.Mutex
	daemon       *client.Daemon
	configPath   string
	deviceIDFile string
	hardwareID   string // Kotlin-provided hardware-bound device ID (takes priority)
	deviceName   string // Kotlin-provided display name (e.g. Build.MODEL)
}

// NewSnetCore creates a new core instance. configDir is the app-private
// directory where config files are stored (e.g. /data/data/org.snet.app/files).
func NewSnetCore(configDir string) *SnetCore {
	return &SnetCore{configPath: configDir + "/daemon.json"}
}

// ensureDaemon creates the daemon from saved config if it doesn't exist yet.
// Caller must hold c.mu.
func (c *SnetCore) ensureDaemon() (*client.Daemon, error) {
	if c.daemon != nil {
		return c.daemon, nil
	}
	cfg, err := client.LoadConfigAt(c.configPath)
	if err != nil {
		cfg = &client.Config{WireguardPort: protocol.DefaultWGPort}
	}
	d := client.NewDaemonAt(cfg, c.configPath)
	if c.deviceIDFile != "" {
		d.SetDeviceIDFile(c.deviceIDFile)
	}
	if c.deviceName != "" {
		d.SetHostname(c.deviceName)
	}
	// Generate device ID eagerly so it's available before any bind/join.
	// Priority: Kotlin hardware ID > file-based > Go-generated.
	if c.hardwareID != "" {
		if cfg.DeviceID == "" || cfg.DeviceID != c.hardwareID {
			cfg.DeviceID = c.hardwareID
			d.SaveConfig()
			// Also persist to device.key file for caching
			if c.deviceIDFile != "" {
				_ = client.SaveDeviceKeypair(c.deviceIDFile, c.hardwareID, cfg.PrivateKey)
			}
		}
	} else if cfg.DeviceID == "" {
		if id, err := client.LoadOrCreateDeviceID(c.deviceIDFile, ""); err == nil && id != "" {
			cfg.DeviceID = id
			d.SaveConfig()
		}
	}
	c.daemon = d
	return d, nil
}

// Start loads config and brings up all active networks. tunFD is the file
// descriptor from VpnService.establish(). deviceIDPath is where the device
// identity file is stored. If a daemon already exists, just sets the fd and
// brings up active networks instead of creating a new daemon.
func (c *SnetCore) Start(tunFD int, deviceIDPath string, serverAddr string, serverCA string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.deviceIDFile = deviceIDPath

	// If daemon already exists (from ensureDaemon), just set fd and bring up.
	if c.daemon != nil {
		c.daemon.SetAndroidTunFD(int(tunFD))
		c.daemon.BringUpActive()
		return nil
	}

	cfg, err := client.LoadConfigAt(c.configPath)
	if err != nil {
		cfg = &client.Config{WireguardPort: protocol.DefaultWGPort}
	}
	if serverAddr != "" {
		cfg.ServerAddr = serverAddr
	}
	if serverCA != "" {
		cfg.ServerCAPath = serverCA
	}

	// Inject Kotlin hardware ID if available and config doesn't have one yet.
	if c.hardwareID != "" && cfg.DeviceID == "" {
		cfg.DeviceID = c.hardwareID
	}

	d := client.NewDaemonAt(cfg, c.configPath)
	d.SetAndroidTunFD(int(tunFD))
	if c.deviceName != "" {
		d.SetHostname(c.deviceName)
	}

	if err := d.Start(); err != nil {
		return fmt.Errorf("daemon start: %w", err)
	}
	c.daemon = d
	return nil
}

// SetDeviceIDFile sets the path for the persistent device identity file.
// Must be called before Bind if Start has not been called yet.
func (c *SnetCore) SetDeviceIDFile(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deviceIDFile = path
}

// SetHardwareID sets the Kotlin-provided hardware-bound device ID.
// This takes priority over file-based and Go-generated device IDs.
func (c *SnetCore) SetHardwareID(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hardwareID = id
}

// SetDeviceName sets the Kotlin-provided display name (e.g. Build.MODEL)
// reported to the server during device registration and binding. Call during
// init, before Start or Bind.
func (c *SnetCore) SetDeviceName(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deviceName = name
	if c.daemon != nil {
		c.daemon.SetHostname(name)
	}
}

// SetTunFD sets the TUN file descriptor on the existing daemon and brings up
// all active networks. Called from VpnService when the TUN fd becomes
// available. Does NOT create a new daemon — uses the one from ensureDaemon.
func (c *SnetCore) SetTunFD(tunFD int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, err := c.ensureDaemon()
	if err != nil {
		return
	}
	d.SetAndroidTunFD(int(tunFD))
	d.BringUpActive()
}

// Stop gracefully shuts down the daemon and tears down all tunnels.
func (c *SnetCore) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.daemon != nil {
		c.daemon.Close()
		c.daemon = nil
	}
}

// HaltTunnels tears down all tunnels but keeps the daemon and its config (and
// each network's Active flag) alive. Used on Android when the VPN service is
// stopped so the status-bar indicator disappears while the daemon stays warm;
// the next Start(fd) with the new TUN descriptor restores the tunnels quickly
// instead of a full cold start.
func (c *SnetCore) HaltTunnels() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.daemon != nil {
		c.daemon.HaltTunnels()
	}
}

// ServerAddr returns the configured coordination server address (e.g.
// "snet.uizhi.eu.org:8090"). Used by SnetVpnService to exclude the server
// IP from VPN routes so daemon HTTP API calls can reach the server via the
// physical network rather than being routed through the tun0 tunnel (which
// has no peers yet at that point).
func (c *SnetCore) ServerAddr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.daemon == nil {
		return ""
	}
	return c.daemon.Config().ServerAddr
}

// Status returns the daemon status as a JSON string.
func (c *SnetCore) Status() string {
	c.mu.Lock()
	d, _ := c.ensureDaemon()
	c.mu.Unlock()
	st, err := d.Status()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	b, _ := json.Marshal(st)
	return string(b)
}

// JoinNetwork joins a network. nid is the network ID, code is the pairing code.
// serverAddr and port can be empty/0 to use the configured server.
func (c *SnetCore) JoinNetwork(nid string, code string, serverAddr string, port int) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	resp, err := d.Join(serverAddr, port, nid, code)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// CreateNetwork creates a new network. description, tagsJSON and visibility
// are optional community-sharing metadata; pass empty values to skip them.
// tagsJSON, when non-empty, is a JSON array of tag strings.
func (c *SnetCore) CreateNetwork(name string, subnet string, approvalRequired bool, serverAddr string, port int, description string, tagsJSON string, visibility string) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	var tags []string
	if tagsJSON != "" {
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			return fmt.Sprintf(`{"error":%q}`, fmt.Errorf("parse tags: %w", err).Error())
		}
	}
	resp, err := d.Create(serverAddr, port, name, subnet, approvalRequired, client.ShareOpt(&description, tags, &visibility))
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// LeaveNetwork leaves a network.
func (c *SnetCore) LeaveNetwork(nid string) error {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return d.Leave(nid)
}

// RemoveNetwork removes a network from config.
func (c *SnetCore) RemoveNetwork(nid string) error {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return d.Remove(nid)
}

// UpdateSubnets updates the advertised subnets for this device on a network.
func (c *SnetCore) UpdateSubnets(nid string, subnets string) error {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return err
	}
	var subs []string
	if err := json.Unmarshal([]byte(subnets), &subs); err != nil {
		return fmt.Errorf("parse subnets: %w", err)
	}
	return d.UpdateSubnets(nid, subs)
}

// DetectLocalSubnets returns detected local subnets as a JSON array string.
func (c *SnetCore) DetectLocalSubnets() string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return "[]"
	}
	subnets := d.DetectLocalSubnets()
	b, _ := json.Marshal(subnets)
	return string(b)
}

// Peers returns the peer list for a network as a JSON string.
func (c *SnetCore) Peers(nid string) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	info, err := d.Peers(nid)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	b, _ := json.Marshal(info)
	return string(b)
}

// Info returns network info as a JSON string.
func (c *SnetCore) Info(nid string) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	info, err := d.Info(nid)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	b, _ := json.Marshal(info)
	return string(b)
}

// GetAllowedSubnets returns the allowed subnets for a network as a JSON array.
func (c *SnetCore) GetAllowedSubnets(nid string) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return "[]"
	}
	nc := d.Config().Networks[nid]
	if nc == nil {
		return "[]"
	}
	b, _ := json.Marshal(nc.AllowedSubnets)
	return string(b)
}

// Bind binds this device to the server with an authorization code.
func (c *SnetCore) Bind(serverAddr string, serverCA string, code string) error {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	if err != nil {
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()
	return d.Bind(serverAddr, serverCA, code)
}

// BindDebug is like Bind but returns errors as a JSON string instead of
// throwing a Go exception. Useful for debugging from Kotlin.
func (c *SnetCore) BindDebug(serverAddr string, serverCA string, code string) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	if err != nil {
		c.mu.Unlock()
		return fmt.Sprintf(`{"error":"ensureDaemon: %s"}`, err.Error())
	}
	c.mu.Unlock()
	if err := d.Bind(serverAddr, serverCA, code); err != nil {
		return fmt.Sprintf(`{"error":"%s"}`, err.Error())
	}
	return `{"ok":true}`
}

// Rejoin rejoins a network after the server lost the node record.
func (c *SnetCore) Rejoin(nid string) error {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return d.Rejoin(nid)
}

// UpdateSettings updates network settings (name, subnet, approval and
// optional community-sharing metadata). description, tagsJSON and visibility
// are optional; pass empty values to leave them unchanged (nil semantics are
// preserved internally for updates).
func (c *SnetCore) UpdateSettings(nid string, name string, subnet string, approvalRequired bool, description string, tagsJSON string, visibility string) error {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return err
	}
	var tags []string
	if tagsJSON != "" {
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			return fmt.Errorf("parse tags: %w", err)
		}
	}
	var desc, vis *string
	if description != "" {
		desc = &description
	}
	if visibility != "" {
		vis = &visibility
	}
	return d.UpdateSettings(nid, name, subnet, &approvalRequired, client.ShareOpt(desc, tags, vis))
}

// SetNodeRole sets a peer node's role ("admin" or "member") within a network
// this device owns.
func (c *SnetCore) SetNodeRole(nid string, nodeID string, role string) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	if err := d.SetNodeRole(nid, nodeID, role); err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return `{"ok":true}`
}

// DeleteNetwork removes a network from the server and local config (owner only).
func (c *SnetCore) DeleteNetwork(nid string) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	if err := d.DeleteNetwork(nid); err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return `{"ok":true}`
}

// ApprovePending approves a pending join request (owner only).
func (c *SnetCore) ApprovePending(nid string, pendingID string) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	if err := d.ApprovePending(nid, pendingID); err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return `{"ok":true}`
}

// DenyPending denies a pending join request (owner only).
func (c *SnetCore) DenyPending(nid string, pendingID string) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	if err := d.DenyPending(nid, pendingID); err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return `{"ok":true}`
}

// CancelPending cancels a pending join request (requester side).
func (c *SnetCore) CancelPending(pendingID string) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	if err := d.CancelPending(pendingID); err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return `{"ok":true}`
}

// Kick removes a member from a network (owner only).
func (c *SnetCore) Kick(nid string, nodeID string) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	if err := d.Kick(nid, nodeID); err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return `{"ok":true}`
}

// ResetCode resets the pairing code for a network (owner only).
func (c *SnetCore) ResetCode(nid string) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	code, err := d.ResetCode(nid)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	b, _ := json.Marshal(map[string]string{"pairingCode": code})
	return string(b)
}

// GetAuthStatus queries the server for this device's authorization status.
// Returns a JSON object with "bound" (bool), "expired" (bool), and "authCodeId" (string).
// serverAddr and serverCA specify the server to query; pass empty to use the
// daemon's configured server. This is a one-shot check; the daemon's background
// bindCheckLoop continues to verify binding every 10 seconds.
func (c *SnetCore) GetAuthStatus(serverAddr, serverCA string) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}

	// Use the daemon's existing API client (it handles server selection)
	status, err := d.GetAuthStatus()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}

	b, _ := json.Marshal(status)
	return string(b)
}

// Version returns the library version.
func Version() string {
	return "0.1.0"
}

// KeepaliveInterval is the WireGuard persistent keepalive in seconds.
const KeepaliveInterval = protocol.KeepaliveInterval

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	_ = time.Now()
}
