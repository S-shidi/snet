//go:build android

package client

import (
	"os"
	"strings"
)

// SetDeviceIDFile overrides the default device ID file path for Android,
// where /usr/local/snet/ does not exist. Must be called before
// LoadOrCreateDeviceID, typically from the Java/Kotlin layer at startup.
func SetDeviceIDFile(path string) {
	DefaultDeviceIDFile = path
}

// hardwareID on Android returns empty (no DMI/machine-id), falling back to
// a random device ID in newDeviceID().
func hardwareID() (string, error) {
	if b, err := os.ReadFile("/proc/version"); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	}
	return "", nil
}
