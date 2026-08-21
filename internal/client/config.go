package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"snet/internal/protocol"
)

// defaultSubnet is assumed when a legacy coordination server does not report
// a subnet.
const defaultSubnet = "10.88.0.0/24"

// PendingJoin is a persisted join request awaiting the network owner's
// approval. It is removed once approved (network added) or denied.
type PendingJoin struct {
	PendingID string `json:"pendingId"`
	NetworkID string `json:"networkId"`
	Code      string `json:"code,omitempty"`
	Name      string `json:"name,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	Status    string `json:"status,omitempty"` // pending | denied
	Err       string `json:"err,omitempty"`
}

// NetworkCfg is the persisted per-network state of this daemon. A daemon can
// hold any number of networks; only Active ones are brought up on start.
type NetworkCfg struct {
	Name           string   `json:"name,omitempty"`
	NodeID         string   `json:"nodeId"`
	IP             string   `json:"ip"`
	Token          string   `json:"token"`
	PairingCode    string   `json:"pairingCode,omitempty"`
	Subnet         string   `json:"subnet,omitempty"`
	Port           int      `json:"port,omitempty"`
	Active         bool     `json:"active"`
	Owner          bool     `json:"owner,omitempty"`
	AllowedSubnets []string `json:"allowedSubnets,omitempty"`
}

// Config is the daemon configuration (v2, multi-network). PrivateKey is the
// single WireGuard identity of this device and DeviceID is its stable
// identity that survives reinstalls.
type Config struct {
	ServerAddr    string                  `json:"serverAddr"`
	WireguardPort int                     `json:"wireguardPort"`
	PrivateKey    string                  `json:"privateKey"`
	DeviceID      string                  `json:"deviceId,omitempty"`
	ServerCAPath  string                  `json:"serverCaPath,omitempty"`
	Networks      map[string]*NetworkCfg  `json:"networks,omitempty"`     // key = network ID
	PendingJoins  map[string]*PendingJoin `json:"pendingJoins,omitempty"` // key = pending ID

	// BoundServer is the normalized address this device bound to. The device
	// is considered bound only when BoundServer is non-empty and matches the
	// configured ServerAddr, so changing the server address does not leave a
	// stale "bound" state behind.
	BoundServer string `json:"boundServer,omitempty"`
}

func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "virtual-net")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// LoadConfigAt loads config from an explicit path (used for multi-instance
// testing); empty path falls back to the user config dir. v1 single-network
// configs are migrated to the v2 multi-network schema in place.
func LoadConfigAt(path string) (*Config, error) {
	if path == "" {
		return LoadConfig()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{WireguardPort: protocol.DefaultWGPort}, nil
		}
		return nil, err
	}
	return parseConfig(data)
}

func parseConfig(data []byte) (*Config, error) {
	// The legacy schema kept networkId/nodeId/ip/token/pairingCode at the top
	// level; unmarshal those too so v1 configs migrate cleanly.
	var raw struct {
		Config
		NetworkID   string `json:"networkId"`
		NodeID      string `json:"nodeId"`
		IP          string `json:"ip"`
		Token       string `json:"token"`
		PairingCode string `json:"pairingCode"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	c := raw.Config
	if c.WireguardPort == 0 {
		c.WireguardPort = protocol.DefaultWGPort
	}
	if c.Networks == nil && raw.NetworkID != "" && raw.Token != "" {
		c.Networks = map[string]*NetworkCfg{
			raw.NetworkID: {
				NodeID:      raw.NodeID,
				IP:          raw.IP,
				Token:       raw.Token,
				PairingCode: raw.PairingCode,
				Subnet:      defaultSubnet,
				Port:        c.WireguardPort,
				Active:      true,
			},
		}
	}
	// Stale-server cleanup: a legacy device that never bound to a server and
	// holds no networks or pending joins has no meaningful server address.
	// Drop it so the UI shows "未连接服务器" and the device is free to choose
	// any server. Configs with networks, pending joins or an explicit binding
	// keep their address (their networks poll that server).
	if c.BoundServer == "" && len(c.Networks) == 0 && len(c.PendingJoins) == 0 {
		c.ServerAddr = ""
	}
	return &c, nil
}

func LoadConfig() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	return LoadConfigAt(path)
}

func (c *Config) Save() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	return c.SaveAt(path)
}

// SaveAt persists the config to an explicit path, creating parent dirs.
func (c *Config) SaveAt(path string) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// NetworkIDs returns the network IDs this daemon has ever joined.
func (c *Config) NetworkIDs() []string {
	ids := make([]string, 0, len(c.Networks))
	for id := range c.Networks {
		ids = append(ids, id)
	}
	return ids
}

// Bound reports whether this device has bound to its currently configured
// server. The binding is tied to the exact normalized server address, so
// changing the server address clears the effective bound state.
func (c *Config) Bound() bool {
	return c.BoundServer != "" && strings.EqualFold(c.BoundServer, c.ServerAddr)
}

// FindUnusedPort picks a free UDP port in the range starting at base.
func FindUnusedPort(base int) int {
	for p := base; p < base+64; p++ {
		if portFree(p) {
			return p
		}
	}
	return 0
}
