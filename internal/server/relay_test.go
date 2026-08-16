package server

import (
	"net"
	"testing"
	"time"
)

// TestRelayPairing verifies two UDP clients talking to the same relay port
// receive each other's packets (the WireGuard relay scenario).
func TestRelayPairing(t *testing.T) {
	// grab a free port
	lc, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := lc.LocalAddr().(*net.UDPAddr).Port
	lc.Close()

	r := NewRelay(port, 1)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	a := dialUDP(t, port)
	b := dialUDP(t, port)
	defer a.Close()
	defer b.Close()

	relay := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	// first round registers both endpoints (packets may be dropped while the
	// partner is unknown — WireGuard keepalives retry, so this is harmless)
	if _, err := a.WriteToUDP([]byte("from-a"), relay); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteToUDP([]byte("from-b"), relay); err != nil {
		t.Fatal(err)
	}

	// second round must be cross-delivered
	if _, err := a.WriteToUDP([]byte("from-a2"), relay); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteToUDP([]byte("from-b2"), relay); err != nil {
		t.Fatal(err)
	}

	_ = a.SetReadDeadline(time.Now().Add(2 * time.Second))
	_ = b.SetReadDeadline(time.Now().Add(2 * time.Second))

	buf := make([]byte, 256)
	n, _, err := b.ReadFromUDP(buf)
	if err != nil || string(buf[:n]) != "from-a2" {
		t.Fatalf("b got %q err=%v", string(buf[:n]), err)
	}
	// a receives round-1 "from-b" then round-2 "from-b2" (FIFO); assert the
	// final forwarded packet
	var gotA string
	for i := 0; i < 2; i++ {
		n, _, err = a.ReadFromUDP(buf)
		if err != nil {
			t.Fatal(err)
		}
		gotA = string(buf[:n])
	}
	if gotA != "from-b2" {
		t.Fatalf("a got %q", gotA)
	}
}

func dialUDP(t *testing.T, relayPort int) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
