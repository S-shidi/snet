//go:build !windows

package client

import "fmt"

// tunDeviceName returns the TUN adapter name used by tun.CreateTUN. macOS
// treats "utun" as a template prefix and allocates a fresh interface; linux
// creates the literal name.
func tunDeviceName() string { return "utun" }

// configureInterface assigns the private /32 address, MTU and up state on a
// freshly created TUN interface (BSD ifconfig / iproute2 syntax).
func configureInterface(iface, ip string, mtu int) error {
	if err := runCmd("ifconfig", iface, "inet", ip, "255.255.255.255", "up"); err != nil {
		return fmt.Errorf("ifconfig addr: %w", err)
	}
	if err := runCmd("ifconfig", iface, "mtu", fmt.Sprint(mtu)); err != nil {
		return fmt.Errorf("ifconfig mtu: %w", err)
	}
	return nil
}

// addHostRoute installs a /32 host route to a peer through the TUN interface.
func addHostRoute(iface, ip string) error {
	return runCmd("route", "add", "-host", ip, "-interface", iface)
}
