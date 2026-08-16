package client

import (
	"net"
	"os"
)

// portFree reports whether a UDP port is available on all local addresses.
func portFree(port int) bool {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// subnetsOverlap reports whether two CIDR networks overlap.
func subnetsOverlap(a, b string) bool {
	_, an, err := net.ParseCIDR(a)
	if err != nil {
		return true // unknown subnet: assume conflict to stay safe
	}
	_, bn, err := net.ParseCIDR(b)
	if err != nil {
		return true
	}
	return an.Contains(bn.IP) || bn.Contains(an.IP)
}

func writeFile0600(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
