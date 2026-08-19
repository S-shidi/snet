//go:build windows

package client

import "fmt"

// tunDeviceName returns the stable wintun adapter name. On Windows
// tun.CreateTUN reuses an adapter with the same name instead of allocating a
// fresh one, so a fixed name keeps daemon restarts on the same interface
// (avoiding a pile-up of orphaned adapters).
func tunDeviceName() string { return "snet" }

// configureInterface assigns the private /32 address and brings the adapter up.
// MTU is not configured here: wireguard-go forces 1420 on wintun adapters, so
// the configured mtu is intentionally ignored on Windows.
func configureInterface(iface, ip string, mtu int) error {
	// Delete-then-set keeps this idempotent across daemon restarts, since the
	// adapter (and its addresses) survives between runs.
	_ = runCmd("netsh", "interface", "ipv4", "delete", "address", "name="+iface, "addr="+ip)
	if err := runCmd("netsh", "interface", "ipv4", "set", "address", "name="+iface, "static", ip, "255.255.255.255"); err != nil {
		return fmt.Errorf("netsh set address: %w", err)
	}
	return nil
}

// addHostRoute installs a /32 on-link route to a peer through the wintun
// adapter. Delete-then-add keeps it idempotent when the adapter already
// carries routes from a previous daemon run.
func addHostRoute(iface, ip string) error {
	_ = runCmd("netsh", "interface", "ipv4", "delete", "route", ip+"/32", "interface="+iface)
	if err := runCmd("netsh", "interface", "ipv4", "add", "route", ip+"/32", "interface="+iface); err != nil {
		return fmt.Errorf("netsh add route: %w", err)
	}
	return nil
}

// removeHostRoute removes a /32 host route.
func removeHostRoute(iface, ip string) error {
	return runCmd("netsh", "interface", "ipv4", "delete", "route", ip+"/32", "interface="+iface)
}

// addSubnetRoute installs a subnet route through the wintun adapter.
func addSubnetRoute(iface, cidr string) error {
	_ = runCmd("netsh", "interface", "ipv4", "delete", "route", cidr, "interface="+iface)
	if err := runCmd("netsh", "interface", "ipv4", "add", "route", cidr, "interface="+iface); err != nil {
		return fmt.Errorf("netsh add route: %w", err)
	}
	return nil
}

// removeSubnetRoute removes a subnet route.
func removeSubnetRoute(iface, cidr string) error {
	return runCmd("netsh", "interface", "ipv4", "delete", "route", cidr, "interface="+iface)
}

// enableIPForwarding enables IP forwarding on Windows (requires admin).
func enableIPForwarding() error {
	return runCmd("reg", "add",
		`HKLM\SYSTEM\CurrentControlSet\Services\Tcpip\Parameters`,
		"/v", "IPEnableRouter", "/t", "REG_DWORD", "/d", "1", "/f")
}
