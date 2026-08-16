//go:build darwin

package client

import (
	"os/exec"
	"regexp"
	"strings"
)

var ioPlatformUUIDRe = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`)

// hardwareID returns the machine's stable hardware identifier (the macOS
// IOPlatformUUID), which survives both app and OS reinstalls.
func hardwareID() (string, error) {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", err
	}
	m := ioPlatformUUIDRe.FindSubmatch(out)
	if len(m) < 2 {
		return "", nil
	}
	return strings.TrimSpace(string(m[1])), nil
}
