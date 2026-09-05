package client

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"

	"snet/internal/protocol"
)

// Tunnel owns the utun interface and the WireGuard device.
type Tunnel struct {
	dev   *device.Device
	tun   tun.Device
	iface string
	ip    string

	mu    sync.Mutex
	peers map[string]protocol.Node // nodeID -> node
}

// NewTunnel creates the utun interface, assigns the private /32 address and
// brings up the WireGuard device. Requires root on macOS.
func NewTunnel(privKeyHex, ip string, port int, mtu int) (*Tunnel, error) {
	t, err := tun.CreateTUN(tunDeviceName(), mtu)
	if err != nil {
		return nil, fmt.Errorf("create tun: %w", err)
	}
	name, err := t.Name()
	if err != nil {
		t.Close()
		return nil, fmt.Errorf("tun name: %w", err)
	}

	if err := configureInterface(name, ip, mtu); err != nil {
		t.Close()
		return nil, err
	}

	logger := device.NewLogger(device.LogLevelError, "snetd: ")
	dev := device.NewDevice(t, conn.NewDefaultBind(), logger)
	dev.IpcSet(fmt.Sprintf("private_key=%s\nlisten_port=%d\n", privKeyHex, port))
	if err := dev.Up(); err != nil {
		t.Close()
		return nil, fmt.Errorf("wg up: %w", err)
	}

	return &Tunnel{dev: dev, tun: t, iface: name, ip: ip, peers: make(map[string]protocol.Node)}, nil
}

// ApplyPeers rebuilds the full WireGuard peer set and OS routes, diffing
// against the previous set to add/remove routes as peers join, leave, or
// change their advertised subnets. subnet is the tunnel subnet (e.g.
// "10.88.1.0/24") used to route peer-to-peer traffic through the interface.
// relayEP, when non-empty, is used as the fallback endpoint for peers that
// have no direct endpoint or whose direct endpoint has not been established.
func (t *Tunnel) ApplyPeers(peers []protocol.Node, subnet, relayEP string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	next := make(map[string]protocol.Node, len(peers))
	for _, p := range peers {
		next[p.ID] = p
	}

	// Build WireGuard IPC configuration.
	var sb strings.Builder
	for _, p := range peers {
		// Skip peers without a usable public key: a coordination server may
		// withhold peer keys (member-scoped views), and writing an empty key
		// would make wireguard-go reject the whole config. Such peers simply
		// cannot be reached directly and stay relay-only.
		pub, err := pubToHex(p.PublicKey)
		if err != nil || pub == "" {
			continue
		}
		sb.WriteString("public_key=" + pub + "\n")
		// Always accept packets from the peer's tunnel IP (needed for replies).
		sb.WriteString("allowed_ip=" + p.IP + "/32\n")
		// Also accept packets from any advertised subnets.
		for _, sub := range p.AllowedSubnets {
			sb.WriteString("allowed_ip=" + sub + "\n")
		}
		ep := ""
		if p.Endpoint != "" {
			ep = resolveEndpoint(p.Endpoint)
		} else if relayEP != "" {
			ep = resolveEndpoint(relayEP)
		}
		if ep != "" {
			sb.WriteString("endpoint=" + ep + "\n")
		}
		sb.WriteString(fmt.Sprintf("persistent_keepalive_interval=%d\n", protocol.KeepaliveInterval))
	}
	if err := t.dev.IpcSet(sb.String()); err != nil {
		return fmt.Errorf("wg config: %w", err)
	}

	// OS route diff: remove routes for departed peers.
	for id, old := range t.peers {
		if _, ok := next[id]; !ok {
			removePeerRoutes(t.iface, old)
		}
	}
	// OS route diff: add/change routes for current peers.
	for id, p := range next {
		old, existed := t.peers[id]
		if !existed {
			addPeerRoutes(t.iface, p)
		} else if subnetsChanged(old.AllowedSubnets, p.AllowedSubnets) || old.IP != p.IP {
			removePeerRoutes(t.iface, old)
			addPeerRoutes(t.iface, p)
		}
	}
	// Ensure the tunnel subnet is routed through the interface so that
	// packets to any peer's tunnel IP reach the WireGuard device.
	if subnet != "" {
		_ = addSubnetRoute(t.iface, subnet)
	}
	t.peers = next
	return nil
}

// Peers returns a snapshot of the current peer set. Callers must not modify
// the returned map.
func (t *Tunnel) Peers() map[string]protocol.Node {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.peers
}

// Iface returns the TUN interface name.
func (t *Tunnel) Iface() string { return t.iface }

// RemoveAllPeers removes all OS routes for every peer in the tunnel.
// Used during tunnel teardown before closing the interface.
func (t *Tunnel) RemoveAllPeers() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range t.peers {
		removePeerRoutes(t.iface, p)
	}
	t.peers = make(map[string]protocol.Node)
}

// addPeerRoutes installs OS routes for a peer's IP and any advertised subnets.
func addPeerRoutes(iface string, p protocol.Node) {
	if len(p.AllowedSubnets) > 0 {
		for _, sub := range p.AllowedSubnets {
			addSubnetRoute(iface, sub)
		}
	} else {
		addHostRoute(iface, p.IP)
	}
}

// removePeerRoutes removes OS routes for a peer's IP and any advertised subnets.
func removePeerRoutes(iface string, p protocol.Node) {
	if len(p.AllowedSubnets) > 0 {
		for _, sub := range p.AllowedSubnets {
			removeSubnetRoute(iface, sub)
		}
	}
	// Always remove the host route (might exist from before subnets were added).
	removeHostRoute(iface, p.IP)
}

// subnetsChanged reports whether two subnet slices differ.
func subnetsChanged(a, b []string) bool {
	if len(a) != len(b) {
		return true
	}
	m := make(map[string]struct{}, len(a))
	for _, s := range a {
		m[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := m[s]; !ok {
			return true
		}
	}
	return false
}

// Stats returns per-peer transfer and handshake info for status display.
func (t *Tunnel) Stats() (map[string]PeerStats, error) {
	out, err := t.dev.IpcGet()
	if err != nil {
		return nil, err
	}
	stats := make(map[string]PeerStats)
	var cur *PeerStats
	var curPub string
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "public_key":
			if cur != nil {
				stats[curPub] = *cur
			}
			curPub = hexToB64(v)
			cur = &PeerStats{}
		case "rx_bytes", "tx_bytes", "last_handshake_time_sec":
			if cur == nil {
				continue
			}
			switch k {
			case "rx_bytes":
				fmt.Sscanf(v, "%d", &cur.RxBytes)
			case "tx_bytes":
				fmt.Sscanf(v, "%d", &cur.TxBytes)
			case "last_handshake_time_sec":
				var sec int64
				fmt.Sscanf(v, "%d", &sec)
				cur.LastHandshakeSec = sec
			}
		}
	}
	if cur != nil {
		stats[curPub] = *cur
	}
	return stats, nil
}

type PeerStats struct {
	RxBytes          int64
	TxBytes          int64
	LastHandshakeSec int64
}

func (t *Tunnel) InterfaceName() string { return t.iface }
func (t *Tunnel) IP() string            { return t.ip }

func (t *Tunnel) Close() {
	if t.dev != nil {
		t.dev.Close()
	}
	if t.tun != nil {
		t.tun.Close()
	}
}

func pubToHex(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("decode pubkey: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// resolveEndpoint turns "host:port" into "ip:port" so it can be passed to the
// WireGuard device, whose IPC parser only accepts numeric addresses.
func resolveEndpoint(ep string) string {
	addr, err := net.ResolveUDPAddr("udp", ep)
	if err != nil {
		return ep
	}
	return addr.String()
}

func hexToB64(h string) string {
	raw, err := hex.DecodeString(h)
	if err != nil {
		return h
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// LocalIPForServer returns the source IP that routes to the given server,
// used as the local endpoint candidate.
func LocalIPForServer(serverAddr string) (string, error) {
	host := serverAddr
	if strings.HasPrefix(host, "http://") {
		host = strings.TrimPrefix(host, "http://")
	}
	if strings.HasPrefix(host, "https://") {
		host = strings.TrimPrefix(host, "https://")
	}
	if i := strings.Index(host, "/"); i > 0 {
		host = host[:i]
	}
	if i := strings.Index(host, ":"); i > 0 {
		host = host[:i]
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	conn, err := net.Dial("udp", net.JoinHostPort(host, "9"))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "", fmt.Errorf("unexpected local addr type")
	}
	return local.IP.String(), nil
}
