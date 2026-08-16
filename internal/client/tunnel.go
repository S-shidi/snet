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

	"virtualnet/internal/protocol"
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

	logger := device.NewLogger(device.LogLevelError, "vnetd: ")
	dev := device.NewDevice(t, conn.NewDefaultBind(), logger)
	dev.IpcSet(fmt.Sprintf("private_key=%s\nlisten_port=%d\n", privKeyHex, port))
	if err := dev.Up(); err != nil {
		t.Close()
		return nil, fmt.Errorf("wg up: %w", err)
	}

	return &Tunnel{dev: dev, tun: t, iface: name, ip: ip, peers: make(map[string]protocol.Node)}, nil
}

// ApplyPeers rebuilds the full WireGuard peer set and host routes.
func (t *Tunnel) ApplyPeers(peers []protocol.Node) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	next := make(map[string]protocol.Node, len(peers))
	for _, p := range peers {
		next[p.ID] = p
	}

	var sb strings.Builder
	for _, p := range peers {
		pub, err := pubToHex(p.PublicKey)
		if err != nil {
			return err
		}
		sb.WriteString("public_key=" + pub + "\n")
		sb.WriteString("allowed_ip=" + p.IP + "/32\n")
		if p.Endpoint != "" {
			sb.WriteString("endpoint=" + resolveEndpoint(p.Endpoint) + "\n")
		}
		sb.WriteString(fmt.Sprintf("persistent_keepalive_interval=%d\n", protocol.KeepaliveInterval))
	}
	if err := t.dev.IpcSet(sb.String()); err != nil {
		return fmt.Errorf("wg config: %w", err)
	}

	// add host routes for new peers
	for id, p := range next {
		if _, ok := t.peers[id]; !ok {
			if err := addHostRoute(t.iface, p.IP); err != nil {
				return fmt.Errorf("route %s: %w", p.IP, err)
			}
		}
	}
	t.peers = next
	return nil
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
