package client

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// DefaultDeviceIDFile is where the daemon keeps the machine's stable device
// identity, outside the app config dir so it survives an app reinstall. The
// path is OS-dependent; platform files may override it (see device_windows.go).
var DefaultDeviceIDFile = "/usr/local/vnet/device.id"

// newDeviceID returns a deterministic 16-hex device identity derived from the
// machine's hardware identifier when available, so it survives both an app
// and an OS reinstall. It falls back to a random 8-byte ID (still 16 hex)
// when no hardware identifier can be read (e.g. virtual machines).
func newDeviceID() (string, error) {
	if hw, err := hardwareID(); err == nil && hw != "" {
		sum := sha256.Sum256([]byte(hw))
		return hex.EncodeToString(sum[:8]), nil
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// deviceIDGen is the identity generator used when no stored device ID exists.
// Tests override it so multiple test daemons in one process don't share the
// single hardware-derived identity.
var deviceIDGen = newDeviceID

// LoadOrCreateDeviceID returns this machine's stable device identity. It
// prefers the on-disk identity file, then an already persisted config value,
// and finally generates a fresh ID (persisting it to the file when possible).
func LoadOrCreateDeviceID(deviceIDFile, existing string) (string, error) {
	if deviceIDFile != "" {
		if b, err := os.ReadFile(deviceIDFile); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s, nil
			}
		}
	}
	if existing != "" {
		return existing, nil
	}
	id, err := deviceIDGen()
	if err != nil {
		return "", err
	}
	if deviceIDFile != "" {
		if err := os.WriteFile(deviceIDFile, []byte(id+"\n"), 0o600); err != nil {
			return "", fmt.Errorf("persist device id: %w", err)
		}
	}
	return id, nil
}
