package client

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var deviceIDFormatRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{7,63}$`)

func TestMain(m *testing.M) {
	// Many tests spawn several daemons in one process against the same server.
	// A shared hardware-derived device ID would make them all appear as one
	// device (violating "one network per device"), so tests use a fresh random
	// identity per daemon. The hardware-derived path is still covered by
	// TestNewDeviceIDDeterministic, which calls newDeviceID directly.
	deviceIDGen = func() (string, error) {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		return hex.EncodeToString(b), nil
	}
	os.Exit(m.Run())
}

func TestNewDeviceIDFormat(t *testing.T) {
	id, err := newDeviceID()
	if err != nil {
		t.Fatalf("newDeviceID: %v", err)
	}
	if len(id) != 16 {
		t.Fatalf("newDeviceID length = %d, want 16", len(id))
	}
	if !deviceIDFormatRe.MatchString(id) {
		t.Fatalf("newDeviceID %q fails server deviceID regex", id)
	}
}

func TestNewDeviceIDDeterministic(t *testing.T) {
	a, err := newDeviceID()
	if err != nil {
		t.Fatalf("newDeviceID: %v", err)
	}
	b, err := newDeviceID()
	if err != nil {
		t.Fatalf("newDeviceID: %v", err)
	}
	if a != b {
		t.Fatalf("hardware-derived device ID not stable: %q vs %q", a, b)
	}
}

func TestLoadOrCreateDeviceIDPrecedence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "device.id")
	if err := os.WriteFile(file, []byte("stored-id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := LoadOrCreateDeviceID(file, "cfg-id")
	if err != nil {
		t.Fatal(err)
	}
	if id != "stored-id" {
		t.Fatalf("file should take precedence, got %q", id)
	}
}

func TestLoadOrCreateDeviceIDGeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "device.id")
	id, err := LoadOrCreateDeviceID(file, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 16 {
		t.Fatalf("generated id length = %d, want 16", len(id))
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != id {
		t.Fatalf("persisted id = %q, want %q", strings.TrimSpace(string(b)), id)
	}
}
