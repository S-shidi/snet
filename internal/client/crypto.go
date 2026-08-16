package client

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"

	"golang.org/x/crypto/curve25519"
)

// GenerateKeyPair returns base64-encoded private and public X25519 keys
// suitable for WireGuard.
func GenerateKeyPair() (priv, pub string, err error) {
	key := make([]byte, curve25519.ScalarSize)
	if _, err := rand.Read(key); err != nil {
		return "", "", err
	}
	point, err := curve25519.X25519(key, curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(key), base64.StdEncoding.EncodeToString(point), nil
}

func DecodePublicKey(b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	if len(raw) != 32 {
		return nil, errors.New("public key must be 32 bytes")
	}
	return raw, nil
}

// b64ToHex converts a base64-encoded raw key to hex (wireguard-go UAPI format).
func b64ToHex(b64 string) string {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return b64
	}
	return hex.EncodeToString(raw)
}

// pubKeyB64FromPrivHex derives the public key from a hex private key and
// returns it base64-encoded.
func pubKeyB64FromPrivHex(privHex string) string {
	raw, err := hex.DecodeString(privHex)
	if err != nil {
		return ""
	}
	point, err := curve25519.X25519(raw, curve25519.Basepoint)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(point)
}
