//go:build android

package client

import (
	"fmt"

	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"

	"snet/internal/protocol"
)

// tunDeviceName is unused on Android (TUN fd comes from VpnService).
func tunDeviceName() string { return "" }

// configureInterface is a no-op on Android; VpnService.Builder handles it.
func configureInterface(_, _ string, _ int) error { return nil }

// addHostRoute / removeHostRoute / addSubnetRoute / removeSubnetRoute are
// no-ops on Android; VpnService.Builder.addRoute() handles routing.
func addHostRoute(_, _ string) error      { return nil }
func removeHostRoute(_, _ string) error   { return nil }
func addSubnetRoute(_, _ string) error    { return nil }
func removeSubnetRoute(_, _ string) error { return nil }

// enableIPForwarding is a no-op on Android; the kernel handles it.
func enableIPForwarding() error { return nil }

// NewTunnelFromFD creates a WireGuard tunnel from a TUN file descriptor
// provided by Android's VpnService. The fd is owned by the caller (VpnService,
// via a ParcelFileDescriptor) and must never be touched by Go: wrapping a
// PFD-owned fd in os.NewFile attaches a finalizer that closes it on GC, which
// trips Android's fdsan ownership check and SIGABRTs the process. We therefore
// take an independent duplicate that Go owns for the tunnel's entire lifetime.
func NewTunnelFromFD(fd int, privKeyHex, ip string, port, mtu int) (*Tunnel, error) {
	gofd, err := unix.Dup(fd)
	if err != nil {
		return nil, fmt.Errorf("dup tun fd %d: %w", fd, err)
	}
	t, name, err := tun.CreateUnmonitoredTUNFromFD(gofd)
	if err != nil {
		unix.Close(gofd)
		return nil, fmt.Errorf("create tun from fd: %w", err)
	}
	_ = name // Android doesn't need the interface name for routing

	logger := device.NewLogger(device.LogLevelError, "snetd: ")
	dev := device.NewDevice(t, conn.NewDefaultBind(), logger)
	dev.IpcSet(fmt.Sprintf("private_key=%s\nlisten_port=%d\n", privKeyHex, port))
	if err := dev.Up(); err != nil {
		t.Close()
		return nil, fmt.Errorf("wg up: %w", err)
	}

	return &Tunnel{dev: dev, tun: t, iface: name, ip: ip, peers: make(map[string]protocol.Node)}, nil
}
