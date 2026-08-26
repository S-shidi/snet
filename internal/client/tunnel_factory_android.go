//go:build android

package client

// createTunnelForPlatform creates a WireGuard tunnel. On Android, if an fd
// from VpnService is available it wraps that fd; otherwise falls back to
// NewTunnel (which will fail on Android without root — the retry loop will
// re-attempt once the VPN service provides the fd).
func createTunnelForPlatform(fd int, privKeyHex, ip string, port, mtu int) (*Tunnel, error) {
	if fd != 0 {
		return NewTunnelFromFD(fd, privKeyHex, ip, port, mtu)
	}
	return NewTunnel(privKeyHex, ip, port, mtu)
}
