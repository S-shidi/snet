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
var DefaultDeviceIDFile = "/usr/local/snet/device.id"

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
				// Support both legacy (ID only) and new (ID + key) formats.
				if lines := strings.SplitN(s, "\n", 2); len(lines) > 0 {
					return strings.TrimSpace(lines[0]), nil
				}
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

// LoadDeviceKeypair reads the persisted device identity and private key from
// the device ID file. Returns ("", "") when no key is stored (legacy client
// or first start). The file format is:
//
//	line 1: device ID
//	line 2: hex-encoded private key (optional, added by v2+ clients)
func LoadDeviceKeypair(deviceIDFile string) (deviceID, privateKey string, err error) {
	if deviceIDFile == "" {
		return "", "", nil
	}
	b, err := os.ReadFile(deviceIDFile)
	if err != nil {
		return "", "", nil // file missing is not an error
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", "", nil
	}
	lines := strings.Split(s, "\n")
	deviceID = strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		privateKey = strings.TrimSpace(lines[1])
	}
	return deviceID, privateKey, nil
}

// SaveDeviceKeypair persists the device identity and private key to the device
// ID file, replacing any previous content. Both values are written only when
// non-empty; a missing private key line is allowed (legacy format).
func SaveDeviceKeypair(deviceIDFile, deviceID, privateKey string) error {
	if deviceIDFile == "" {
		return nil
	}
	var content string
	if privateKey != "" {
		content = deviceID + "\n" + privateKey + "\n"
	} else {
		content = deviceID + "\n"
	}
	return writeFile0600(deviceIDFile, []byte(content))
}
