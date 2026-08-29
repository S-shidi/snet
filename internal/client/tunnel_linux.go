//go:build linux && !android

package client

import (
	"fmt"
	"os"
	"strings"
)

// tunDeviceName returns the TUN adapter name used by tun.CreateTUN on Linux.
// An empty name lets the kernel auto-assign a unique tun%d interface so
// multiple networks can coexist (a fixed "utun" would collide).
func tunDeviceName() string { return "" }

// configureInterface assigns the private /32 address, MTU and up state on a
// freshly created TUN interface using iproute2 commands.
func configureInterface(iface, ip string, mtu int) error {
	if err := runCmd("ip", "addr", "add", ip+"/32", "dev", iface); err != nil {
		return fmt.Errorf("ip addr add: %w", err)
	}
	if err := runCmd("ip", "link", "set", iface, "mtu", fmt.Sprint(mtu)); err != nil {
		return fmt.Errorf("ip link mtu: %w", err)
	}
	if err := runCmd("ip", "link", "set", iface, "up"); err != nil {
		return fmt.Errorf("ip link up: %w", err)
	}
	return nil
}

// addHostRoute installs a /32 host route to a peer through the TUN interface.
func addHostRoute(iface, ip string) error {
	return runCmd("ip", "route", "add", ip+"/32", "dev", iface)
}

// removeHostRoute removes a /32 host route.
func removeHostRoute(iface, ip string) error {
	return runCmd("ip", "route", "del", ip+"/32", "dev", iface)
}

// addSubnetRoute installs a subnet route through the TUN interface.
func addSubnetRoute(iface, cidr string) error {
	return runCmd("ip", "route", "add", cidr, "dev", iface)
}

// removeSubnetRoute removes a subnet route.
func removeSubnetRoute(iface, cidr string) error {
	return runCmd("ip", "route", "del", cidr, "dev", iface)
}

// enableIPForwarding enables IP forwarding on Linux (requires root)
// and persists the setting so it survives reboots.
func enableIPForwarding() error {
	if err := runCmd("sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return err
	}
	persistSysctl("net.ipv4.ip_forward", "1")
	return nil
}

// persistSysctl writes a sysctl key=value to a drop-in file.
func persistSysctl(key, value string) {
	dir := "/etc/sysctl.d"
	_ = os.MkdirAll(dir, 0755)
	path := dir + "/99-snet.conf"
	line := key + " = " + value
	data, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(data), line) {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", line)
}
