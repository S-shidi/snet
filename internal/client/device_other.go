//go:build !darwin && !linux && !windows

package client

// hardwareID is unsupported on this platform; newDeviceID falls back to a
// random ID.
func hardwareID() (string, error) {
	return "", nil
}
