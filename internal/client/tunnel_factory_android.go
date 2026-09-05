//go:build android

package client

import "fmt"

// createTunnelForPlatform creates a WireGuard tunnel. On Android the TUN fd
// must come from VpnService.establish(); there is no way to create a TUN
// device directly without root, so when the fd is not yet available we return
// an explicit error instead of falling through to NewTunnel (which would fail
// every time with "permission denied"). The daemon's retry loop keeps retrying
// until the VPN service provides the fd via SetAndroidTunFD.
func createTunnelForPlatform(fd int, privKeyHex, ip string, port, mtu int) (*Tunnel, error) {
	if fd != 0 {
		return NewTunnelFromFD(fd, privKeyHex, ip, port, mtu)
	}
	return nil, fmt.Errorf("VPN TUN 未就绪，请稍候")
}
