package client

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ctlTokenFileName is the basename of the file holding the ctl channel
// shared secret. It is written next to the daemon config so both the daemon
// and the desktop GUI process can read it.
const ctlTokenFileName = "ctl-token"

// CtlTokenPath returns the absolute path of the ctl-token file derived from
// the daemon config path. When configPath is empty the file is named after
// the platform default config dir.
func CtlTokenPath(configPath string) string {
	if configPath == "" {
		return ctlTokenFileName
	}
	return filepath.Join(filepath.Dir(configPath), ctlTokenFileName)
}

// LoadOrCreateCtlToken returns the ctl-channel shared secret, generating and
// persisting a fresh 32-byte (hex-encoded) random token if the file is
// missing. The file is created 0644: on macOS and Windows the daemon runs as
// root / LocalSystem while the desktop GUI runs as the logged-in user, so a
// root-only 0600 file would make the ctl API unusable for the GUI. Any local
// user already had full unauthenticated control of the 127.0.0.1 ctl API, so
// a readable token is still strictly stronger — the file is past the reach of
// the remote/browser-initiated (DNS-rebinding) class of attacks it defends
// against.
func LoadOrCreateCtlToken(tokenPath string) (string, error) {
	if tokenPath == "" {
		return "", fmt.Errorf("ctl token path is empty")
	}
	if b, err := os.ReadFile(tokenPath); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	}
	tok, err := randomCtlToken()
	if err != nil {
		return "", err
	}
	if dir := filepath.Dir(tokenPath); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("create ctl token dir: %w", err)
		}
	}
	if err := os.WriteFile(tokenPath, []byte(tok+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("persist ctl token: %w", err)
	}
	_ = os.Chmod(tokenPath, 0o644)
	return tok, nil
}

func randomCtlToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
