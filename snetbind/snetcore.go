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
	if c.deviceIDFile != "" {
		client.SetDeviceIDFile(c.deviceIDFile)
	}
	cfg, err := client.LoadConfigAt(c.configPath)
	if err != nil {
		cfg = &client.Config{WireguardPort: protocol.DefaultWGPort}
	}
	d := client.NewDaemonAt(cfg, c.configPath)
	if c.deviceIDFile != "" {
		d.SetDeviceIDFile(c.deviceIDFile)
	}
	c.daemon = d
	return d, nil
}

// Start loads config and brings up all active networks. tunFD is the file
// descriptor from VpnService.establish(). deviceIDPath is where the device
// identity file is stored.
func (c *SnetCore) Start(tunFD int, deviceIDPath string, serverAddr string, serverCA string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.deviceIDFile = deviceIDPath
	client.SetDeviceIDFile(deviceIDPath)

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

	d := client.NewDaemonAt(cfg, c.configPath)

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

// Stop gracefully shuts down the daemon and tears down all tunnels.
func (c *SnetCore) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.daemon != nil {
		c.daemon.Close()
		c.daemon = nil
	}
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

// CreateNetwork creates a new network.
func (c *SnetCore) CreateNetwork(name string, subnet string, approvalRequired bool, serverAddr string, port int) string {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	resp, err := d.Create(serverAddr, port, name, subnet, approvalRequired)
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

// UpdateSettings updates network settings (name, subnet, approval).
func (c *SnetCore) UpdateSettings(nid string, name string, subnet string, approvalRequired bool) error {
	c.mu.Lock()
	d, err := c.ensureDaemon()
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return d.UpdateSettings(nid, name, subnet, &approvalRequired)
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
