//go:build linux && !android

package client

import (
	"os"
	"strings"
)

// hardwareID returns the machine's stable hardware identifier. It prefers the
// DMI product UUID (stable across OS reinstalls) and falls back to
// /etc/machine-id when the DMI data is not exposed (e.g. containers/VMs).
func hardwareID() (string, error) {
	if b, err := os.ReadFile("/sys/class/dmi/id/product_uuid"); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	}
	if b, err := os.ReadFile("/etc/machine-id"); err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	return "", nil
}
