package server

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"snet/internal/protocol"
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

// TestRelayFanout verifies that a third member of a network receives packets
// relayed from either of the other two (a network with >2 members). The old
// pairwise relay could not deliver to a 3rd endpoint at all.
func TestRelayFanout(t *testing.T) {
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
	c := dialUDP(t, port)
	defer a.Close()
	defer b.Close()
	defer c.Close()

	relay := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	// Register all three endpoints (first packet identifies the sender).
	mustWrite := func(c *net.UDPConn, msg string) {
		if _, err := c.WriteToUDP([]byte(msg), relay); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(a, "reg-a")
	mustWrite(b, "reg-b")
	mustWrite(c, "reg-c")

	// Now a real data packet from a must reach both b and c.
	mustWrite(a, "fanout-abc")

	_ = a.SetReadDeadline(time.Now().Add(2 * time.Second))
	_ = b.SetReadDeadline(time.Now().Add(2 * time.Second))
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))

	buf := make([]byte, 256)
	gotB := ""
	for {
		n, _, err := b.ReadFromUDP(buf)
		if err != nil {
			break
		}
		gotB = string(buf[:n])
		if gotB == "fanout-abc" {
			break
		}
	}
	if gotB != "fanout-abc" {
		t.Fatalf("b got %q, want fanout-abc", gotB)
	}
	gotC := ""
	for {
		n, _, err := c.ReadFromUDP(buf)
		if err != nil {
			break
		}
		gotC = string(buf[:n])
		if gotC == "fanout-abc" {
			break
		}
	}
	if gotC != "fanout-abc" {
		t.Fatalf("c got %q, want fanout-abc", gotC)
	}
}

// TestRelayEnsureLazy verifies that a relay created without Start (the lazy
// production path) binds its advertised port only when Ensure is first called,
// and that Ensure is a cheap no-op on repeat calls.
func TestRelayEnsureLazy(t *testing.T) {
	// grab a free port
	lc, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := lc.LocalAddr().(*net.UDPAddr).Port
	lc.Close()

	r := NewRelay(port, 1)
	// No Start(): the port must be free until Ensure is invoked.
	if err := assertPortFree(t, port); err != nil {
		t.Fatalf("relay bound eagerly: %v", err)
	}
	if err := r.Ensure(port); err != nil {
		t.Fatalf("Ensure(advertised) failed: %v", err)
	}
	if err := r.Ensure(port); err != nil {
		t.Fatalf("second Ensure failed (should be no-op): %v", err)
	}
	defer r.Close()

	a := dialUDP(t, port)
	b := dialUDP(t, port)
	defer a.Close()
	defer b.Close()

	relay := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	// register both, then cross-deliver like TestRelayPairing
	if _, err := a.WriteToUDP([]byte("reg-a"), relay); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteToUDP([]byte("reg-b"), relay); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteToUDP([]byte("from-a2"), relay); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteToUDP([]byte("from-b2"), relay); err != nil {
		t.Fatal(err)
	}

	_ = a.SetReadDeadline(time.Now().Add(2 * time.Second))
	_ = b.SetReadDeadline(time.Now().Add(2 * time.Second))

	buf := make([]byte, 256)
	var gotB string
	for {
		n, _, err := b.ReadFromUDP(buf)
		if err != nil {
			break
		}
		gotB = string(buf[:n])
		if gotB == "from-a2" {
			break
		}
	}
	if gotB != "from-a2" {
		t.Fatalf("b got %q, want from-a2", gotB)
	}
}

// assertPortFree reports whether a UDP listener can bind the given port on
// 127.0.0.1 (i.e. the relay has not bound it eagerly).
func assertPortFree(t *testing.T, port int) error {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

func dialUDP(t *testing.T, relayPort int) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestRelayWhoami verifies the relay answers a whoami control request with
// the observed source endpoint (the requester's NAT mapping).
func TestRelayWhoami(t *testing.T) {
	port := freePort(t)
	r := NewRelay(port, 1)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	c := dialUDP(t, port)
	defer c.Close()
	relay := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}

	req := append([]byte("\xfeSNET1"), []byte(`{"op":"whoami"}`)...)
	if _, err := c.WriteToUDP(req, relay); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	n, _, err := c.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read whoami reply: %v", err)
	}
	got := string(buf[:n])
	if !strings.HasPrefix(got, "\xfeSNET1") {
		t.Fatalf("reply missing control prefix: %q", got)
	}
	body := buf[len(protocol.RelayCtrlPrefix):n]
	var w struct {
		Op       string `json:"op"`
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		t.Fatalf("whoami reply not JSON: %s (%v)", got, err)
	}
	if w.Op != "whoami" {
		t.Fatalf("whoami op = %q", w.Op)
	}
	_, wantPort, _ := net.SplitHostPort(c.LocalAddr().(*net.UDPAddr).String())
	_, gotHost, _ := net.SplitHostPort(w.Endpoint) // unused; keep simple
	if _, gotPort, err := net.SplitHostPort(w.Endpoint); err != nil || gotPort != wantPort {
		t.Fatalf("whoami endpoint = %q, want port %s", w.Endpoint, wantPort)
	}
	_ = gotHost
}

// TestRelayGroup verifies a group request lists every other live endpoint but
// not the requester, and that control packets are never fanned out.
func TestRelayGroup(t *testing.T) {
	port := freePort(t)
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

	// register both endpoints
	if _, err := a.WriteToUDP([]byte("ping-a"), relay); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteToUDP([]byte("ping-b"), relay); err != nil {
		t.Fatal(err)
	}

	// b's registration causes the relay to fan "ping-b" back to a; drain it so
	// the group reply below is the first packet a reads after the request.
	_ = a.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	if _, _, err := a.ReadFromUDP(buf); err != nil {
		t.Fatalf("drain forwarded ping-b: %v", err)
	}

	req := append([]byte("\xfeSNET1"), []byte(`{"op":"group"}`)...)
	if _, err := a.WriteToUDP(req, relay); err != nil {
		t.Fatal(err)
	}
	n, _, err := a.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read group reply: %v", err)
	}
	got := string(buf[:n])
	if !strings.HasPrefix(got, "\xfeSNET1") {
		t.Fatalf("reply missing control prefix: %q", got)
	}
	var g struct {
		Op    string   `json:"op"`
		Peers []string `json:"peers"`
	}
	if err := json.Unmarshal(buf[len(protocol.RelayCtrlPrefix):n], &g); err != nil {
		t.Fatalf("group reply not JSON: %s (%v)", got, err)
	}
	if g.Op != "group" || len(g.Peers) != 1 {
		t.Fatalf("group reply = %+v", g)
	}
	_, wantPort, _ := net.SplitHostPort(b.LocalAddr().(*net.UDPAddr).String())
	if _, gotPort, err := net.SplitHostPort(g.Peers[0]); err != nil || gotPort != wantPort {
		t.Fatalf("group peers[0] = %q, want port %s", g.Peers[0], wantPort)
	}
	// b must not receive the control packet (it is not fanned out)
	_ = b.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, _, err := b.ReadFromUDP(buf); err == nil {
		t.Fatal("b received a control packet that should never be forwarded")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	lc, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	p := lc.LocalAddr().(*net.UDPAddr).Port
	lc.Close()
	return p
}
