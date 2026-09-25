package server

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
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

// TestRelayGroupIncludesDataFlows verifies that a group request returns the
// registered WireGuard data flows (e.g. a phone's keepalive flow) even when
// the requester probed from a one-shot control socket, while excluding the
// requester's own socket and any ctrlOnly endpoints. This mirrors the client
// flow of a phone that registers its data flow then asks the relay which
// peers to connect to.
func TestRelayGroupIncludesDataFlows(t *testing.T) {
	port := freePort(t)
	r := NewRelay(port, 1)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	relay := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}

	// phoneWG is the phone's persistent WireGuard data flow (it sends WG
	// keepalives, so the relay sees it as a data endpoint, not ctrlOnly).
	phoneWG := dialUDP(t, port)
	defer phoneWG.Close()
	// peer is another node's data flow.
	peer := dialUDP(t, port)
	defer peer.Close()

	// Register both data flows with a WG keepalive (type-4, 32 bytes).
	keep := make([]byte, 32)
	keep[0] = 4
	if _, err := phoneWG.WriteToUDP(keep, relay); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.WriteToUDP(keep, relay); err != nil {
		t.Fatal(err)
	}

	// phoneCtrl is a one-shot control probe from the phone: it only ever
	// speaks the SNET1 control protocol, so the relay must mark it ctrlOnly
	// and never list it as a peer.
	phoneCtrl := dialUDP(t, port)
	defer phoneCtrl.Close()

	req := append([]byte("\xfeSNET1"), []byte(`{"op":"group"}`)...)
	if _, err := phoneCtrl.WriteToUDP(req, relay); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 4096)
	_ = phoneCtrl.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := phoneCtrl.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read group reply: %v", err)
	}
	var g struct {
		Op    string   `json:"op"`
		Peers []string `json:"peers"`
	}
	body := buf[len(protocol.RelayCtrlPrefix):n]
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("group reply not JSON: %s (%v)", buf[:n], err)
	}
	if g.Op != "group" {
		t.Fatalf("group op = %q", g.Op)
	}

	want := map[string]bool{
		portOf(t, phoneWG): true,
		portOf(t, peer):    true,
	}
	if len(g.Peers) != len(want) {
		t.Fatalf("group peers = %v, want exactly %v", g.Peers, want)
	}
	for _, p := range g.Peers {
		if !want[portOfPeer(t, p)] {
			t.Fatalf("unexpected peer %q (want %v)", p, want)
		}
	}
	if _, ok := want[portOf(t, phoneCtrl)]; ok {
		t.Fatalf("ctrl socket leaked into group peers")
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

// portOf returns the source port of a local UDP conn ("[::]:59247" -> 59247).
func portOf(t *testing.T, c *net.UDPConn) string {
	t.Helper()
	_, p, err := net.SplitHostPort(c.LocalAddr().String())
	if err != nil {
		t.Fatalf("local addr %v: %v", c.LocalAddr(), err)
	}
	return p
}

// portOfPeer returns the port part of a relay-listed peer endpoint string.
func portOfPeer(t *testing.T, ep string) string {
	t.Helper()
	_, p, err := net.SplitHostPort(ep)
	if err != nil {
		t.Fatalf("peer endpoint %q: %v", ep, err)
	}
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
	s.SetRelayAllFlows(r.Flows)
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

// testNetwork3 returns a store with three nodes A/B/C each owning a distinct
// node port, with ctrlHost attribution set like the live deployment:
// A = mobile (control 223.160.209.21), B = Mac (112.10.250.51),
// C = docker on the relay host (127.0.0.1). The recorder tap and per-port flow
// listings are wired to the store's fake relay sink.
type testNetwork3 struct {
	store   *Store
	rec     *relayRecorder
	flows   map[int][]string
	a, b, c protocol.Node
	aPort   int
	bPort   int
	cPort   int
}

func buildNetwork3(t *testing.T) *testNetwork3 {
	t.Helper()
	base := freePort(t)
	r := NewRelay(base, 8)
	t.Cleanup(func() { r.Close() })

	s := NewStore()
	s.SetRelay("127.0.0.1", base, 8)
	s.SetRelayEnsure(r.Ensure)
	s.SetNodeEnsure(r.EnsureNode)
	s.SetRelayFlowLookup(func(port int, host string) []string { return nil })
	r.SetNodeRoute(s.RelayRouteNodePort)

	net3 := &testNetwork3{store: s, flows: map[int][]string{}}
	net3.rec = &relayRecorder{}
	s.SetRelaySend(func(port int, to string, data []byte) bool {
		net3.rec.sends = append(net3.rec.sends, sendRec{port, to, string(data)})
		return true
	})
	s.SetRelayAllFlows(func(port int) []string {
		return net3.flows[port]
	})

	created, err := s.CreateNetwork(testKey(950), "owner-device-9500", "", "", false)
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
	la, err := s.ListPeersFrom(created.Token, "223.160.209.21", true)
	if err != nil {
		t.Fatal(err)
	}
	net3.a = *la.Self
	rb := join(951, "device-b-9501")
	rc := join(952, "device-c-9502")
	lb, err := s.ListPeersFrom(rb.Token, "112.10.250.51", true)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := s.ListPeersFrom(rc.Token, "66.187.6.46", true)
	if err != nil {
		t.Fatal(err)
	}
	net3.b = *lb.Self
	net3.c = *lc.Self
	net3.aPort, net3.bPort, net3.cPort = net3.a.RelayPort, net3.b.RelayPort, net3.c.RelayPort
	if net3.aPort == 0 || net3.bPort == 0 || net3.cPort == 0 ||
		net3.aPort == net3.bPort || net3.aPort == net3.cPort || net3.bPort == net3.cPort {
		t.Fatalf("distinct node ports required: a=%d b=%d c=%d", net3.aPort, net3.bPort, net3.cPort)
	}
	return net3
}

type sendRec struct {
	port int
	to   string
	data string
}
type relayRecorder struct {
	sends []sendRec
}

func (rr *relayRecorder) contains(to, data string) bool {
	for _, s := range rr.sends {
		if s.to == to && s.data == data {
			return true
		}
	}
	return false
}

// TestNeighborDataPlaneAttribution verifies that a data-plane flow whose host
// differs from the node's control-plane host is still attributed by /23-/16
// operator-pool adjacency: the phone (control 223.160.209.21) reaches the Mac
// through the same /23 as its control egress (223.160.208.21).
func TestNeighborDataPlaneAttribution(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	// The phone keepalives toward Mac's node port from its data-plane NAT host,
	// which matches no stored control/IP address exactly.
	s.OnRelayFlow(net3.bPort, "223.160.208.21:39802")

	ns := s.networks[net3.a.NetworkID]
	if ns == nil {
		t.Fatal("no network state")
	}
	if got := s.exclusiveHostOwnerLocked(ns, "223.160.208.21"); got != net3.a.ID {
		t.Fatalf("data-plane host 223.160.208.21 owner=%q, want A=%q", got, net3.a.ID)
	}
}

// TestSharedHostNeverRecipient verifies the mis-route guard: when the reverse
// index maps one host to more than one node (112.10.250.51 -> {Mac, phone}, a
// CGNAT/shared-egress collision), a phone-bound frame must never be forwarded
// to the Mac's flow because the shared host is not a dependable recipient
// target.
func TestSharedHostNeverRecipient(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	// Poison the reverse index to model the live bug: 112.10.250.51 is both
	// Mac (truth) and phone (stale).
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	s.learnNodeHostLocked(ns, net3.a.ID, "112.10.250.51")
	s.mu.Unlock()

	// Docker (C, the sender) has two inbound mappings on its node port: the
	// phone's data-plane flow 223.160.208.21:39802 (same /23 as the phone's
	// control host, attributed on the wire) and Mac's flow under the shared
	// host 112.10.250.51:12831. The receiver port order matters: the phone's
	// flow must win, Mac's must be skipped.
	s.OnRelayFlow(net3.cPort, "223.160.208.21:39802")
	s.mu.Lock()
	ns = s.networks[net3.a.NetworkID]
	if got := s.exclusiveHostOwnerLocked(ns, "223.160.208.21"); got != net3.a.ID {
		s.mu.Unlock()
		t.Fatalf("222.160.208.21 data-plane attribution owner=%q, want phone=%q", got, net3.a.ID)
	}
	s.mu.Unlock()
	net3.flows[net3.cPort] = []string{"112.10.250.51:12831", "223.160.208.21:39802"}

	payload := []byte("for-phone")

	// Docker sends to the phone's node port: Mac's flow under the shared host
	// must be skipped, and the payload must arrive on the phone's own flow.
	net3.store.RelayRouteNodePort(net3.aPort, "66.187.6.46:51900", payload)

	for _, snd := range net3.rec.sends {
		if snd.to == "112.10.250.51:12831" && snd.data == string(payload) {
			t.Fatalf("phone-bound frame routed to Mac's flow on shared host 112.10.250.51: %+v", net3.rec.sends)
		}
	}
	if !net3.rec.contains("223.160.208.21:39802", string(payload)) {
		t.Fatalf("phone-bound frame never reached phone flow; sends=%v", net3.rec.sends)
	}
}

// TestFlowFallbackActiveNode verifies the period before the recipient opens a
// flow on the sender's socket: a frame is still delivered via the recipient's
// authoritative flow (docker on the relay host accepting any server source).
func TestFlowFallbackActiveNode(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	// Mac (B) sends a data frame to docker (C) before docker has opened any
	// flow on Mac's socket. Docker's live flow (66.187.6.46:51900) must still
	// receive it.
	s.OnRelayFlow(net3.cPort, "66.187.6.46:51900")
	payload := []byte("mac-to-docker")
	net3.store.RelayRouteNodePort(net3.cPort, "112.10.250.51:12831", payload)

	if !net3.rec.contains("66.187.6.46:51900", string(payload)) {
		t.Fatalf("docker never got the frame via authoritative flow; sends=%v", net3.rec.sends)
	}
}

// TestMultiEgressFanOut verifies the CGNAT multi-egress case: a subscriber
// holds two live public mappings (the operator rotates egress per flow), so
// the docker node's socket shows two phone flows under distinct hosts
// (223.160.208.29, 223.160.209.29). A docker->phone frame must be delivered to
// BOTH flows, not just the first match — sending one mapping only loses every
// alternate egress (observed as ~50% loss).
func TestMultiEgressFanOut(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	// The phone is seen using both egress hosts (each exclusively its own).
	s.learnNodeHostLocked(ns, net3.a.ID, "223.160.208.29")
	s.learnNodeHostLocked(ns, net3.a.ID, "223.160.209.29")
	s.mu.Unlock()

	// Docker (C, sender) has two inbound mappings on its node port: one per
	// phone egress. Both must receive the data frame.
	net3.flows[net3.cPort] = []string{"223.160.208.29:39801", "223.160.209.29:39802"}

	payload := []byte("docker-to-phone")
	net3.store.RelayRouteNodePort(net3.aPort, "66.187.6.46:51900", payload)

	if !net3.rec.contains("223.160.208.29:39801", string(payload)) {
		t.Fatalf("payload not delivered to first phone egress; sends=%v", net3.rec.sends)
	}
	if !net3.rec.contains("223.160.209.29:39802", string(payload)) {
		t.Fatalf("payload not delivered to second phone egress; sends=%v", net3.rec.sends)
	}
}

// TestStaleDataPlaneHostPruned verifies that a data-plane host which goes
// idle past the TTL is removed from the reverse index: a CGNAT pool the
// operator recycled for another subscriber must not keep steering frames
// toward the old owner's flows. A still-live hold (existing relayFlowByNode
// mapping or ctrlHost) keeps the host alive.
func TestStaleDataPlaneHostPruned(t *testing.T) {
	s := NewStore()
	created, err := s.CreateNetwork(testKey(960), "device-960", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := s.Join(created.NetworkID, created.PairingCode, testKey(961), "device-c-961")
	if err != nil {
		t.Fatal(err)
	}
	ns := s.networks[created.NetworkID]
	if ns == nil {
		t.Fatal("no network state")
	}

	now := time.Now()
	s.mu.Lock()
	// Phone learns a data-plane host (e.g. CGNAT egress 223.160.208.29) that
	// later goes idle.
	s.learnNodeHostLocked(ns, rc.NodeID, "223.160.208.29")
	ns.hostSeen["223.160.208.29"] = now.Add(-10 * time.Minute)
	if _, ok := ns.nodeByHost["223.160.208.29"]; !ok {
		s.mu.Unlock()
		t.Fatal("host not in reverse index after learn")
	}
	old := len(ns.hostSeen)
	s.pruneStaleHostsLocked(ns, now)
	if _, ok := ns.nodeByHost["223.160.208.29"]; ok {
		s.mu.Unlock()
		t.Fatalf("stale host still in reverse index after prune")
	}
	if len(ns.hostSeen) >= old {
		s.mu.Unlock()
		t.Fatalf("hostSeen not pruned (was %d, now %d)", old, len(ns.hostSeen))
	}

	// A fresh ctrlHost reference must keep the host alive even when old.
	s.learnNodeHostLocked(ns, rc.NodeID, "223.160.209.21")
	ns.hostSeen["223.160.209.21"] = now.Add(-30 * time.Second)
	ns.ctrlHost[rc.NodeID] = "223.160.209.21"
	ns.hostSeen["223.160.209.21"] = now.Add(-10 * time.Minute)
	before := len(ns.nodeByHost)
	s.pruneStaleHostsLocked(ns, now)
	if _, ok := ns.nodeByHost["223.160.209.21"]; !ok {
		s.mu.Unlock()
		t.Fatalf("live ctrlHost pruned from reverse index; nodeByHost=%v", ns.nodeByHost)
	}
	if len(ns.nodeByHost) != before {
		s.mu.Unlock()
		t.Fatalf("prune removed a live entry")
	}
	s.mu.Unlock()
}

// TestAttribBySelfAdvertisedEndpoint verifies the NAS regression: a node whose
// control plane rides a v2ray proxy on the relay host (ctrlHost=66.187.6.46)
// but whose data plane egresses from its real public address, which it
// self-advertises as its endpoint (39.180.139.10:51820). A data frame from
// that data-plane host matches neither the control-plane host nor any learned
// reverse-index host, so it must be attributed from node.Endpoint or it is
// silently dropped (observed NAS<->everyone loss).
func TestAttribBySelfAdvertisedEndpoint(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	// NAS (C): control plane is the relay-host proxy, data plane is its real
	// public address.
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.nodes[net3.c.ID].Endpoint = "39.180.139.10:51820"
	s.mu.Unlock()

	// Mac (B) keeps its inbound mapping on NAS's socket.
	s.OnRelayFlow(net3.cPort, "112.10.250.51:12831")
	net3.flows[net3.cPort] = []string{"112.10.250.51:12831"}

	payload := []byte("nas-to-mac")

	// NAS sends a data frame to Mac's node port from its direct public
	// address; ctrlHost and reverse-index matches both miss.
	s.RelayRouteNodePort(net3.bPort, "39.180.139.10:4390", payload)

	if !net3.rec.contains("112.10.250.51:12831", string(payload)) {
		t.Fatalf("NAS data-plane frame not delivered to Mac; sends=%v", net3.rec.sends)
	}

	// The self-advertised endpoint must seed the reverse index.
	s.mu.Lock()
	ns = s.networks[net3.a.NetworkID]
	if !ns.nodeByHost["39.180.139.10"][net3.c.ID] {
		s.mu.Unlock()
		t.Fatalf("self endpoint host 39.180.139.10 not learned to NAS; nodeByHost=%v", ns.nodeByHost)
	}
	s.mu.Unlock()

	ns.lastFlowWrite.Store(0)
	s.OnRelayFlow(net3.bPort, "39.180.139.10:4390")
	s.mu.Lock()
	ns = s.networks[net3.a.NetworkID]
	if got := ns.relayFlowByNode[net3.c.ID]; got != "39.180.139.10:4390" {
		s.mu.Unlock()
		t.Fatalf("relay flow attribution for NAS = %q, want 39.180.139.10:4390", got)
	}
	s.mu.Unlock()

	// Two nodes advertising the same *fresh* self endpoint is ambiguous and
	// must not attribute (prevents a reused/stolen address hijacking a flow).
	// The host must be one the reverse index has not already learned.
	s.mu.Lock()
	ns = s.networks[net3.a.NetworkID]
	ns.nodes[net3.c.ID].Endpoint = "139.180.139.10:51820"
	ns.nodes[net3.b.ID].Endpoint = "139.180.139.10:51820"
	s.mu.Unlock()
	s.RelayRouteNodePort(net3.bPort, "139.180.139.10:4391", []byte("ambiguous"))
	if net3.rec.contains("112.10.250.51:12831", "ambiguous") {
		t.Fatalf("ambiguous self endpoint attributed and delivered; sends=%v", net3.rec.sends)
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

// TestMultiRelayEndpointsAdvertised verifies that when alternate relay hosts
// are configured, ListPeersFrom advertises every candidate on the network's
// relay port (primary first, deduplicated by host).
func TestMultiRelayEndpointsAdvertised(t *testing.T) {
	s := NewStore()
	base := freePort(t)
	s.SetRelay("relay-a.example", base, 8)
	s.SetRelayAlternates([]string{"relay-b.example", "relay-c.example", "relay-a.example"})
	created, err := s.CreateNetwork(testKey(960), "dev-relay-multi", "", "", false)
	if err != nil {
		t.Fatal(err)
	}

	la, err := s.ListPeersFrom(created.Token, "223.160.209.21", true)
	if err != nil {
		t.Fatal(err)
	}
	if la.RelayEndpoint == "" {
		t.Fatal("primary RelayEndpoint empty")
	}
	if len(la.RelayEndpoints) != 3 {
		t.Fatalf("want 3 candidates, got %v", la.RelayEndpoints)
	}
	if la.RelayEndpoints[0] != la.RelayEndpoint {
		t.Fatalf("primary not first: %v vs %q", la.RelayEndpoints, la.RelayEndpoint)
	}
	if la.RelayEndpoints[1] != "relay-b.example:"+fmt.Sprint(created.RelayPort) {
		t.Fatalf("alternate B wrong: %v (port %d)", la.RelayEndpoints, created.RelayPort)
	}
	if la.RelayEndpoints[2] != "relay-c.example:"+fmt.Sprint(created.RelayPort) {
		t.Fatalf("alternate C wrong: %v", la.RelayEndpoints)
	}
	// All candidates share the network's relay port.
	for _, ep := range la.RelayEndpoints {
		_, port, err := net.SplitHostPort(ep)
		if err != nil {
			t.Fatalf("bad candidate %q: %v", ep, err)
		}
		if port != fmt.Sprint(created.RelayPort) {
			t.Fatalf("candidate %q uses wrong port (want %d)", ep, created.RelayPort)
		}
	}

	// Single-relay mode must keep the field unset for backward compatibility.
	s2 := NewStore()
	base2 := freePort(t)
	s2.SetRelay("relay-a.example", base2, 8)
	created2, err := s2.CreateNetwork(testKey(961), "dev-relay-single", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	la2, err := s2.ListPeersFrom(created2.Token, "223.160.209.21", true)
	if err != nil {
		t.Fatal(err)
	}
	if la2.RelayEndpoint == "" {
		t.Fatal("single relay endpoint empty")
	}
	if len(la2.RelayEndpoints) != 0 {
		t.Fatalf("single-relay mode should not set RelayEndpoints, got %v", la2.RelayEndpoints)
	}
}

// TestRelayTopoPersistedAcrossRestart verifies that the per-node relay
// topology (assigned unicast port and serving instance host) survives a store
// restart: the same port is re-advertised and the RelayHostOverride is
// preserved, so peers' cached endpoints stay valid and cross-instance routing
// keeps working.
func TestRelayTopoPersistedAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topo.db")

	newStore := func() *Store {
		t.Helper()
		s, err := NewStoreAt(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		base := freePort(t)
		// Single-instance store: no alternates, so every node collapses onto
		// the primary host and RelayHostOverride stays empty on the wire.
		s.SetRelay("127.0.0.1", base, 16)
		return s
	}

	s := newStore()
	created, err := s.CreateNetwork(testKey(970), "dev-topo-persist", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	join := func(key int, device string) string {
		t.Helper()
		rj, err := s.Join(created.NetworkID, created.PairingCode, testKey(key), device)
		if err != nil {
			t.Fatal(err)
		}
		return rj.Token
	}
	tokA := created.Token
	tokB := join(971, "dev-topo-b")

	// Both callers poll with per-node ports enabled so ports get allocated.
	la, err := s.ListPeersFrom(tokA, "203.0.113.41", true)
	if err != nil {
		t.Fatal(err)
	}
	lb, err := s.ListPeersFrom(tokB, "203.0.113.42", true)
	if err != nil {
		t.Fatal(err)
	}
	aPort := la.Self.RelayPort
	bPort := lb.Self.RelayPort
	if aPort == 0 || bPort == 0 {
		t.Fatalf("node ports not assigned: a=%d b=%d", aPort, bPort)
	}
	if aPort == bPort {
		t.Fatalf("node ports must differ: a=%d b=%d", aPort, bPort)
	}
	hostOf := func(ps []protocol.Node, id string) string {
		t.Helper()
		for _, p := range ps {
			if p.ID == id {
				return p.RelayHostOverride
			}
		}
		return ""
	}
	// Single instance: no override anywhere.
	if got := hostOf(la.Peers, lb.Self.ID); got != "" {
		t.Fatalf("single-instance B RelayHostOverride=%q, want empty", got)
	}
	if la.Self.RelayHostOverride != "" {
		t.Fatalf("single-instance self RelayHostOverride=%q, want empty", la.Self.RelayHostOverride)
	}

	// Restart the store against the same DB file.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := newStore()

	// The persisted topology must restore the exact same ports and no drift.
	la2, err := s2.ListPeersFrom(tokA, "203.0.113.41", true)
	if err != nil {
		t.Fatal(err)
	}
	lb2, err := s2.ListPeersFrom(tokB, "203.0.113.42", true)
	if err != nil {
		t.Fatal(err)
	}
	if la2.Self.RelayPort != aPort {
		t.Fatalf("A port after restart=%d, want persisted %d", la2.Self.RelayPort, aPort)
	}
	if lb2.Self.RelayPort != bPort {
		t.Fatalf("B port after restart=%d, want persisted %d", lb2.Self.RelayPort, bPort)
	}
	if got := hostOf(la2.Peers, lb2.Self.ID); got != "" {
		t.Fatalf("after restart B RelayHostOverride=%q, want empty", got)
	}
}

// TestRelayTopoMultiInstanceAssignment verifies that when alternates are
// configured, each node is assigned a fixed serving instance by stable hash
// and peers receive that instance's host as RelayHostOverride: repeated calls
// pick the same instance, and it is never the primary for primary-assigned
// nodes.
func TestRelayTopoMultiInstanceAssignment(t *testing.T) {
	s := NewStore()
	base := freePort(t)
	s.SetRelay("10.0.0.1", base, 32)
	s.SetRelayAlternates([]string{"10.0.0.2", "10.0.0.3"})
	created, err := s.CreateNetwork(testKey(972), "dev-topo-multi", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	join := func(key int, device string) string {
		t.Helper()
		rj, err := s.Join(created.NetworkID, created.PairingCode, testKey(key), device)
		if err != nil {
			t.Fatal(err)
		}
		return rj.Token
	}
	tokA := created.Token
	tokB := join(973, "dev-topo-multi-b")
	tokC := join(974, "dev-topo-multi-c")

	assign := func(tok, host string) *protocol.Node {
		t.Helper()
		resp, err := s.ListPeersFrom(tok, host, true)
		if err != nil {
			t.Fatal(err)
		}
		return resp.Self
	}
	a1 := assign(tokA, "203.0.113.51")
	b1 := assign(tokB, "203.0.113.52")
	c1 := assign(tokC, "203.0.113.53")

	// Hosts are normalized downwards; primary host collapses to "".
	norm := func(h string) string {
		if h == "" {
			return "10.0.0.1"
		}
		return h
	}
	seen := map[string]bool{norm(a1.RelayHostOverride): true, norm(b1.RelayHostOverride): true, norm(c1.RelayHostOverride): true}
	if len(seen) == 1 {
		t.Fatalf("expected nodes spread across instances, all on %v", seen)
	}

	// Stable: repeated polls return the same instance and the same port.
	a2 := assign(tokA, "203.0.113.51")
	if a2.RelayHostOverride != a1.RelayHostOverride {
		t.Fatalf("A instance changed across polls: %q -> %q", a1.RelayHostOverride, a2.RelayHostOverride)
	}
	if a2.RelayPort != a1.RelayPort {
		t.Fatalf("A port changed across polls: %d -> %d", a1.RelayPort, a2.RelayPort)
	}

	// Peers advertise the serving instance for each other.
	la, err := s.ListPeersFrom(tokA, "203.0.113.51", true)
	if err != nil {
		t.Fatal(err)
	}
	hostOf := func(ps []protocol.Node, id string) string {
		t.Helper()
		for _, p := range ps {
			if p.ID == id {
				return p.RelayHostOverride
			}
		}
		return ""
	}
	if got := hostOf(la.Peers, b1.ID); got != b1.RelayHostOverride {
		t.Fatalf("peer B override=%q, want %q", got, b1.RelayHostOverride)
	}
	if got := hostOf(la.Peers, c1.ID); got != c1.RelayHostOverride {
		t.Fatalf("peer C override=%q, want %q", got, c1.RelayHostOverride)
	}
}
