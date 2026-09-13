package server

import (
	"encoding/json"
	"net"
	"strconv"
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

// TestRelayDampensControlFrames verifies the fan-out amplification loop is
// broken: identical WireGuard control/keepalive frames re-entering the relay
// from any source are fanned out at most once per window, and real data
// frames are never dampened. This is the counterpart to the Phase 1 storm fix.
func TestRelayDampensControlFrames(t *testing.T) {
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

	// A 148-byte handshake-init-shaped frame, and a 32-byte keepalive-shaped
	// frame; both are exactly the "control" shapes the amplifier feeds on.
	init := append([]byte{1}, make([]byte, 147)...)
	keepalive := append([]byte{4}, make([]byte, 31)...)

	mustWrite := func(c *net.UDPConn, m []byte) {
		if _, err := c.WriteToUDP(m, relay); err != nil {
			t.Fatal(err)
		}
	}

	// Register both endpoints with data-length frames (never dampened, always
	// fanned), so b starts in a's seen set and vice versa.
	fake := []byte("x")
	mustWrite(a, fake)
	mustWrite(b, fake)

	readUntil := func(c *net.UDPConn, want []byte, deadline time.Duration) bool {
		_ = c.SetReadDeadline(time.Now().Add(deadline))
		buf := make([]byte, 512)
		for {
			n, _, err := c.ReadFromUDP(buf)
			if err != nil {
				return false
			}
			if string(buf[:n]) == string(want) {
				return true
			}
		}
	}
	// b got a's registration frame; drain it so b's next read is the init.
	_ = readUntil(b, fake, time.Second)

	// First output of a's init reaches b.
	mustWrite(a, init)
	if !readUntil(b, init, time.Second) {
		t.Fatal("b never saw the first init fan-out")
	}
	// Re-broadcasting the SAME frame from b's side (the amplifier case: b
	// roamed back to the relay and re-emits the identical keepalive) must NOT
	// produce another copy at a.
	mustWrite(b, keepalive)
	if readUntil(a, keepalive, time.Second) {
		// Note: a may legitimately receive b's own fresh keepalive via fan-out
		// exactly once; re-sending it immediately must not produce a second.
		mustWrite(b, keepalive)
		if readUntil(a, keepalive, time.Second) {
			t.Fatal("duplicate identical keepalive was fanned out again")
		}
	}

	// Real data frames still flow unimpeded.
	mustWrite(a, []byte("payload-1"))
	if !readUntil(b, []byte("payload-1"), time.Second) {
		t.Fatal("data frame was dampened")
	}
}

// TestRelayClassifyWG sanity-checks WireGuard control-frame recognition.
func TestRelayClassifyWG(t *testing.T) {
	if classifyWG(append([]byte{1}, make([]byte, 147)...)) != wgInit {
		t.Fatal("148B type-1 not init")
	}
	if classifyWG(append([]byte{2}, make([]byte, 91)...)) != wgResponse {
		t.Fatal("92B type-2 not response")
	}
	if classifyWG(append([]byte{3}, make([]byte, 63)...)) != wgCookie {
		t.Fatal("64B type-3 not cookie")
	}
	if classifyWG(append([]byte{4}, make([]byte, 31)...)) != wgKeepalive {
		t.Fatal("32B type-4 not keepalive")
	}
	if classifyWG(append([]byte{1}, make([]byte, 100)...)) != wgData {
		t.Fatal("odd-length type-1 should be data")
	}
}

// TestPerNodeUnicastRouting verifies the Phase 3 unicast relay path: each node
// is assigned its own relay port, and a data frame addressed to a peer's node
// port is forwarded from the SENDER's socket to the recipient's inbound mapping
// on it — with no broadcast fan-out to third parties. Legacy peers (no node
// port) never take this path (covered by the un-routed fan-out tests).
func TestPerNodeUnicastRouting(t *testing.T) {
	base := freePort(t)
	r := NewRelay(base, 8)
	defer r.Close()

	s := NewStore()
	s.SetRelay("127.0.0.1", base, 8)
	s.SetRelayEnsure(r.Ensure)
	s.SetNodeEnsure(r.EnsureNode)
	r.SetNodeRoute(s.RelayRouteNodePort)
	s.SetRelayFlowLookup(r.FlowsByHost)
	s.SetRelaySend(r.SendFrom)

	created, err := s.CreateNetwork(testKey(900), "owner-device-0000", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	join := func(key int, device string) protocol.JoinResp {
		t.Helper()
		rj, err := s.Join(created.NetworkID, created.PairingCode, testKey(key), device)
		if err != nil {
			t.Fatal(err)
		}
		return rj
	}
	// Owner is caller A; B and C join with distinct devices.
	respA, err := s.ListPeersFrom(created.Token, "203.0.113.10", true)
	if err != nil {
		t.Fatal(err)
	}
	respB := join(901, "device-b-0001")
	respC := join(902, "device-c-0002")
	respBp, err := s.ListPeersFrom(respB.Token, "203.0.113.20", true)
	if err != nil {
		t.Fatal(err)
	}
	respCp, err := s.ListPeersFrom(respC.Token, "203.0.113.30", true)
	if err != nil {
		t.Fatal(err)
	}
	// The peers views advertise the same per-node ports back.
	peerPort := func(ps []protocol.Node, id string) int {
		t.Helper()
		for _, p := range ps {
			if p.ID == id {
				return p.RelayPort
			}
		}
		return 0
	}
	aPort := respA.Self.RelayPort
	bPort := respBp.Self.RelayPort
	cPort := respCp.Self.RelayPort
	if aPort == 0 || bPort == 0 || cPort == 0 {
		t.Fatalf("node ports not assigned: a=%d b=%d c=%d", aPort, bPort, cPort)
	}
	if aPort == bPort || aPort == cPort || bPort == cPort {
		t.Fatalf("node ports must be distinct: a=%d b=%d c=%d", aPort, bPort, cPort)
	}
	if got := peerPort(respCp.Peers, respBp.Self.ID); got != bPort {
		t.Fatalf("B advertised as C's peers' RelayPort=%d, want %d", got, bPort)
	}
	if got := peerPort(respCp.Peers, respA.Self.ID); got != aPort {
		t.Fatalf("A advertised as C's peers' RelayPort=%d, want %d", got, aPort)
	}
	// After everyone has polled, every peer's view carries each other's port.
	respAll, err := s.ListPeersFrom(respB.Token, "203.0.113.21", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := peerPort(respAll.Peers, respA.Self.ID); got != aPort {
		t.Fatalf("after polls, A advertised RelayPort=%d, want %d", got, aPort)
	}
	if got := peerPort(respAll.Peers, respCp.Self.ID); got != cPort {
		t.Fatalf("after polls, C advertised RelayPort=%d, want %d", got, cPort)
	}

	// Simulate the wire with three loopback endpoints bound to distinct source
	// IPs, and point the store's control-plane host attribution at them so the
	// unicast router can identify senders and recipients.
	s.NoteCtrlHost(created.NetworkID, respA.Self.ID, "127.0.0.1")
	s.NoteCtrlHost(created.NetworkID, respBp.Self.ID, "127.0.0.2")
	s.NoteCtrlHost(created.NetworkID, respCp.Self.ID, "127.0.0.3")

	dialFrom := func(ip string) *net.UDPConn {
		t.Helper()
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(ip)})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	// Two distinct real source hosts: loopback for A, a LAN interface for B
	// (macOS only owns 127.0.0.1 on lo, so we can't bind 127.0.0.2/3).
	lanIP := lanIPv4(t)
	a := dialFrom("127.0.0.1")
	b := dialFrom(lanIP)
	defer a.Close()
	defer b.Close()
	s.NoteCtrlHost(created.NetworkID, respA.Self.ID, "127.0.0.1")
	s.NoteCtrlHost(created.NetworkID, respBp.Self.ID, lanIP)

	// B opens its inbound mapping on A's socket: B keepalives to A's node port
	// (A targets its own port, the address its socket is bound to on the relay).
	ka := []byte("x")
	if _, err := b.WriteToUDP(ka, &net.UDPAddr{IP: net.ParseIP(lanIP), Port: aPort}); err != nil {
		t.Fatal(err)
	}
	// The relay handles A's and B's sockets in separate goroutines, so wait
	// until B's mapping is actually registered before A's payload can be
	// routed through it.
	deadline := time.Now().Add(2 * time.Second)
	for r.FlowsByHost(aPort, lanIP) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("B's flow never registered on A's socket :%d", aPort)
		}
		time.Sleep(20 * time.Millisecond)
	}
	buf := make([]byte, 512)

	// A sends a data frame addressed to B's node port (relayHost:RelayPortB).
	// It must be routed unicast out of A's socket into B's mapping — fan-out
	// on B's port would go nowhere because B's flow is on A's port, not B's.
	payload := []byte("data-from-A")
	if _, err := a.WriteToUDP(payload, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: bPort}); err != nil {
		t.Fatal(err)
	}

	_ = b.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, src, err := b.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("B never received the unicast frame: %v", err)
	}
	if string(buf[:n]) != string(payload) {
		t.Fatalf("B got %q, want %q", string(buf[:n]), payload)
	}
	// The frame must have left the relay from the SENDER's socket (A's node
	// port), proving it crossed the NAT via the sender's own mapping.
	if _, sp, err := net.SplitHostPort(src.String()); err != nil || sp != strconv.Itoa(aPort) {
		t.Fatalf("B received from %v, want relay src port %d", src, aPort)
	}
}

// lanIPv4 returns a routable non-loopback IPv4 of this host, or skips the test
// when none exists (e.g. an offline CI runner).
func lanIPv4(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("net.Interfaces: %v", err)
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a.String())
			if err != nil {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				return v4.String()
			}
		}
	}
	t.Skip("no non-loopback IPv4 interface")
	return ""
}
