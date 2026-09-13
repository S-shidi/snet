package server

import (
	"encoding/json"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"snet/internal/protocol"
)

// Relay forwards UDP packets among the active endpoints of a network port,
// letting WireGuard traverse symmetric NAT on both sides. Each network is
// assigned one port; every endpoint that sends a keepalive on that port is
// remembered, and a data packet from one endpoint is forwarded to all the
// others (fan-out), so networks with more than two members still reach each
// other through the relay.
//
// Phase 3 adds per-node unicast routing: each node gets its own relay port
// (EnsureNode). A frame arriving at a node port carries the recipient's
// identity (it was addressed to relayHost:<recipient's port>), and the relay
// forwards it from the SENDER's socket instead of fanned out to everyone. The
// client's WireGuard socket targets relayHost:<peer's port> for each peer, so
// the recipient's NAT has an inbound mapping for exactly that source port —
// the frame crosses NAT successfully with zero fan-out amplification.
// Legacy clients (RelayPort==0) keep using the shared broadcast port and the
// old fan-out path, so mixed networks stay interoperable.
//
// The relay is address-based and never decodes WireGuard, so the address it
// sees for an endpoint IS that node's current NAT-mapped WireGuard endpoint.
// Control packets (protocol.RelayCtrlPrefix) are therefore handled in-band:
// "whoami" echoes the observed source endpoint and "group" lists every other
// live endpoint on the port, giving clients the exact punching candidates
// with no extra protocol surface.
type Relay struct {
	base  int
	count int

	mu         sync.Mutex
	conns      map[int]*relayPair
	onActivity func(port int)
	// onFlow, when set, is invoked (from a pair's read loop) with every real
	// WireGuard packet's source mapping so the store can attribute flows to
	// nodes for direct-punch hints and unicast routing.
	onFlow func(port int, addr string)
	// onNodeRoute, when set, is invoked (only from per-node port read loops)
	// with every non-control packet. It decides the unicast destination via
	// the store's node/flow bookkeeping and returns true when the packet was
	// handled; returning false falls back to broadcast fan-out on the pair.
	onNodeRoute func(port int, sender string, data []byte) bool
}

// SetFlowHook registers the callback invoked with each real relay packet's
// source endpoint (port, "ip:port"). Set before the first Ensure.
func (r *Relay) SetFlowHook(fn func(port int, addr string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onFlow = fn
}

// SetNodeRoute registers the callback invoked for every non-control packet on
// a per-node unicast port (EnsureNode ports). It returns true when the packet
// was unicast-routed (or intentionally dropped); false falls back to the
// broadcast fan-out. Set before the first EnsureNode.
func (r *Relay) SetNodeRoute(fn func(port int, sender string, data []byte) bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onNodeRoute = fn
}

type relayPair struct {
	conn     *net.UDPConn
	port     int
	hook     func(port int)
	flowHook func(port int, addr string)
	lastHook time.Time
	mu       sync.Mutex
	// route, when non-nil, handles every non-control packet on this port via
	// unicast routing (per-node ports, Phase 3) and replaces the broadcast
	// fan-out when it returns true.
	route func(port int, sender string, data []byte) bool
	// isNode marks a per-node unicast relay port (Phase 3).
	isNode bool
	// seen tracks the last traffic time per relay endpoint ("ip:port") so
	// stale NAT mappings can be evicted and current ones retained.
	seen map[string]time.Time
	// ctrlOnly marks endpoints whose only contact with the relay has been a
	// control packet (e.g. a client's one-shot whoami/group probing socket).
	// They are excluded from fan-out targets and from group listings so WG
	// traffic is never sprayed at a socket that will not answer it.
	ctrlOnly map[string]bool
	// wireguard control frames (handshake init/response, cookie, keepalive)
	// are dampened before fan-out. Fan-out of a control frame roams every
	// receiving peer's endpoint back to the relay; the peers then answer via
	// the relay, whose next fan-out re-triggers the same peers, forming a
	// self-sustaining amplification loop. A short per-source gate plus exact
	// payload de-duplication breaks that loop without delaying real
	// handshakes (WireGuard retries handshakes at 1s/2s/4s...).
	ctrlGate map[string]time.Time // "src|class" -> last fan-out time
	frameDup map[string]time.Time // exact control-frame payload -> last fan-out time

	// Async fan-out: send queue for non-blocking writes.
	sendQueue chan sendJob
	sendBuf   sync.Pool // buffer pool for zero-allocation sends
}

// sendJob represents one packet to send asynchronously.
type sendJob struct {
	data []byte
	dst  *net.UDPAddr
}

// bufferSize is the maximum packet size for buffer pool allocation.
const bufferSize = 65535

// NewRelay builds a relay covering the port range [base, base+count).
func NewRelay(base, count int) *Relay {
	return &Relay{base: base, count: count, conns: make(map[int]*relayPair)}
}

// SetActivityHook registers a callback invoked (throttled) whenever a port
// receives traffic, so callers can keep networks alive even when their peers
// never poll the coordination API.
func (r *Relay) SetActivityHook(fn func(port int)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onActivity = fn
}

// Ensure lazily binds a single relay port if it is not already bound and
// spawns its read loop. Use in production instead of Start(): only ports of
// networks that actually need relaying are ever bound, so a large port pool
// costs no sockets and no boot time. Returns nil when the port is already
// listening. This is for the NETWORK broadcast port (legacy); per-node ports
// use EnsureNode which sets the unicast route callback.
func (r *Relay) Ensure(port int) error {
	return r.ensure(port, false)
}

// EnsureNode binds a per-node unicast relay port (Phase 3). A frame arriving
// on this port is handled by the onNodeRoute callback (set via SetNodeRoute)
// instead of broadcast fan-out, so the relay delivers unicast to the
// recipient's inbound mapping on the sender's socket.
func (r *Relay) EnsureNode(port int) error {
	return r.ensure(port, true)
}

func (r *Relay) ensure(port int, node bool) error {
	r.mu.Lock()
	if _, ok := r.conns[port]; ok {
		r.mu.Unlock()
		return nil
	}
	hook := r.onActivity
	route := r.onNodeRoute
	r.mu.Unlock()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv6unspecified, Port: port})
	if err != nil {
		return err
	}
	p := &relayPair{
		conn: conn, port: port, hook: hook, flowHook: r.onFlow, route: route, isNode: node,
		seen: make(map[string]time.Time), ctrlOnly: make(map[string]bool),
		ctrlGate: make(map[string]time.Time), frameDup: make(map[string]time.Time),
		sendQueue: make(chan sendJob, 64), // Buffer 64 packets per port
		sendBuf: sync.Pool{
			New: func() any {
				buf := make([]byte, bufferSize)
				return &buf
			},
		},
	}

	r.mu.Lock()
	// Another goroutine may have bound the same port while we were listening.
	if existing, ok := r.conns[port]; ok {
		r.mu.Unlock()
		conn.Close()
		_ = existing
		return nil
	}
	r.conns[port] = p
	r.mu.Unlock()

	go p.serve()
	go p.sendLoop() // Async sender goroutine
	kind := "broadcast"
	if node {
		kind = "node"
	}
	log.Printf("relay: listening on udp :%d (%s)", port, kind)
	return nil
}

// Start binds every port in the range and spawns a read loop per port.
func (r *Relay) Start() error {
	for i := 0; i < r.count; i++ {
		port := r.base + i
		if err := r.Ensure(port); err != nil {
			r.Close()
			return err
		}
	}
	return nil
}

// Ports returns the relay ports actually bound.
func (r *Relay) Ports() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int, 0, len(r.conns))
	for p := range r.conns {
		out = append(out, p)
	}
	return out
}

// Close releases all relay sockets.
func (r *Relay) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.conns {
		p.conn.Close()
	}
	return nil
}

// hostNorm normalizes a host for comparison: strips IPv6 brackets and any
// link-local zone ("fe80::1%en0") and lowercases.
func hostNorm(h string) string {
	h = strings.Trim(strings.ToLower(h), "[]")
	if i := strings.Index(h, "%"); i >= 0 {
		h = h[:i]
	}
	return h
}

func (p *relayPair) serve() {
	buf := make([]byte, 65535)
	for {
		n, addr, err := p.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		p.handle(addr.String(), buf[:n])
	}
}

// sendLoop drains the sendQueue and writes packets asynchronously,
// preventing slow receivers from blocking the relay read loop.
func (p *relayPair) sendLoop() {
	for job := range p.sendQueue {
		if _, err := p.conn.WriteToUDP(job.data, job.dst); err != nil {
			p.mu.Lock()
			delete(p.seen, job.dst.String())
			p.mu.Unlock()
			log.Printf("relay: async write to %s: %v", job.dst, err)
		}
		// Return buffer to pool
		if cap(job.data) == bufferSize {
			buf := job.data[:bufferSize]
			p.sendBuf.Put(&buf)
		}
	}
}

func (p *relayPair) handle(addr string, data []byte) {
	now := time.Now()
	p.mu.Lock()

	if p.hook != nil && now.Sub(p.lastHook) >= 5*time.Second {
		p.lastHook = now
		p.hook(p.port)
	}

	p.seen[addr] = now

	// Evict endpoints idle for over a minute so a re-mapped NAT address stops
	// receiving stale forwards and new members get a slot.
	const stale = 60 * time.Second
	for ep, t := range p.seen {
		if now.Sub(t) >= stale {
			delete(p.seen, ep)
		}
	}

	// Control packets are answered in-band and never forwarded: they leak
	// neither tunnel plaintext nor identity, and old relays just fan them out
	// to peers, where they are dropped as non-WireGuard noise.
	if len(data) >= len(protocol.RelayCtrlPrefix) &&
		strings.HasPrefix(string(data), protocol.RelayCtrlPrefix) {
		p.ctrlOnly[addr] = true
		p.handleControl(addr, data[len(protocol.RelayCtrlPrefix):])
		p.mu.Unlock()
		return
	}
	p.ctrlOnly[addr] = false
	// Dampen WireGuard control frames before fan-out (see relayPair docs).
	dampened := p.dampen(addr, data)
	p.mu.Unlock()
	if dampened {
		return
	}

	// Attribution and unicast routing happen WITHOUT p.mu: the store takes
	// s.mu and, for per-node ports, other pairs' mutexes to look up flows.
	// Holding p.mu here would invert the s.mu -> otherPair.mu ordering used
	// by the route callback and deadlock against another pair's handle.
	if p.flowHook != nil {
		p.flowHook(p.port, addr)
	}
	if p.route != nil && p.route(p.port, addr, data) {
		return
	}

	// Fan out to every other live endpoint asynchronously.
	p.mu.Lock()
	var jobs []sendJob
	for ep := range p.seen {
		if ep == addr || p.ctrlOnly[ep] {
			continue
		}
		dst, err := net.ResolveUDPAddr("udp", ep)
		if err != nil {
			delete(p.seen, ep)
			continue
		}
		// Get buffer from pool and copy data
		bufPtr := p.sendBuf.Get().(*[]byte)
		copied := copy(*bufPtr, data)
		jobs = append(jobs, sendJob{data: (*bufPtr)[:copied], dst: dst})
	}
	p.mu.Unlock()

	// Non-blocking send to queue; if full, drop packet to avoid deadlock.
	for _, job := range jobs {
		select {
		case p.sendQueue <- job:
		default:
			// Queue full, drop packet (better than blocking the read loop)
			log.Printf("relay: send queue full on port %d, dropping packet", p.port)
		}
	}
}

// FlowsByHost returns the live relay flows ("ip:port") seen on the socket
// bound to port whose host part equals host. Used by unicast routing to find
// the recipient's inbound mapping on the sender's socket.
func (r *Relay) FlowsByHost(port int, host string) []string {
	r.mu.Lock()
	p := r.conns[port]
	r.mu.Unlock()
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for ep := range p.seen {
		h, _, err := net.SplitHostPort(ep)
		if err == nil && hostNorm(h) == hostNorm(host) {
			out = append(out, ep)
		}
	}
	return out
}

// SendFrom writes data from the socket bound to port to the given address.
// Used by unicast routing to forward frames with the sender's source port.
func (r *Relay) SendFrom(port int, to string, data []byte) bool {
	r.mu.Lock()
	p := r.conns[port]
	r.mu.Unlock()
	if p == nil {
		return false
	}
	dst, err := net.ResolveUDPAddr("udp", to)
	if err != nil {
		return false
	}
	if _, err := p.conn.WriteToUDP(data, dst); err != nil {
		log.Printf("relay: SendFrom :%d to %s: %v", port, to, err)
		return false
	}
	return true
}

// wgFrameKind classifies a WireGuard datagram by type/length so the relay can
// dampen control-plane chatter without decoding any crypto. Handshake init is
// always 148 bytes (type 1), response 92 bytes (type 2), cookie 64 (type 3);
// a 32-byte type-4 frame is an empty transport message (keepalive). Anything
// else is carried as data and never dampened.
type wgFrameKind int

const (
	wgData      wgFrameKind = iota
	wgInit                  // 148B handshake initiation
	wgResponse              // 92B handshake response
	wgCookie                // 64B cookie reply
	wgKeepalive             // 32B empty transport keepalive
)

func classifyWG(data []byte) wgFrameKind {
	if len(data) == 148 && data[0] == 1 {
		return wgInit
	}
	if len(data) == 92 && data[0] == 2 {
		return wgResponse
	}
	if len(data) == 64 && data[0] == 3 {
		return wgCookie
	}
	if len(data) == 32 && data[0] == 4 {
		return wgKeepalive
	}
	return wgData
}

func (p *relayPair) dampen(addr string, data []byte) bool {
	kind := classifyWG(data)
	if kind == wgData {
		return false
	}
	now := time.Now()

	// Exact-payload de-duplication stops the amplification: the same 32-byte
	// keepalive (or identical re-transmitted init) re-enters the relay from
	// several peers that roamed back to it, and each copy would otherwise be
	// fanned out again. Handshake retries are safe because WireGuard backs
	// off to >=1s between re-announcements, well above the window.
	if t, ok := p.frameDup[string(data)]; ok && now.Sub(t) < 800*time.Millisecond {
		return true
	}
	p.frameDup[string(data)] = now

	// Per-source, per-kind minimum spacing keeps one busy peer from spraying
	// the whole network with its control frames.
	key := addr + "|" + strconv.Itoa(int(kind))
	gap := 150 * time.Millisecond
	if kind == wgInit || kind == wgResponse || kind == wgCookie {
		gap = 400 * time.Millisecond
	}
	if t, ok := p.ctrlGate[key]; ok && now.Sub(t) < gap {
		return true
	}
	p.ctrlGate[key] = now

	// Bound the dampening state: sweep anything idle for a few seconds.
	if len(p.frameDup)+len(p.ctrlGate) > 1024 {
		for k, t := range p.frameDup {
			if now.Sub(t) >= 5*time.Second {
				delete(p.frameDup, k)
			}
		}
		for k, t := range p.ctrlGate {
			if now.Sub(t) >= 5*time.Second {
				delete(p.ctrlGate, k)
			}
		}
	}
	return false
}

// handleControl answers a relay control request from addr. Callers hold p.mu.
// whoami replies with the observed source endpoint (the requester's current
// NAT mapping); group replies with every other live endpoint on the port.
func (p *relayPair) handleControl(addr string, payload []byte) {
	var req struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(payload, &req); err != nil || req.Op == "" {
		return
	}
	switch req.Op {
	case protocol.RelayCtrlWhoami:
		resp, _ := json.Marshal(map[string]string{"op": protocol.RelayCtrlWhoami, "endpoint": addr})
		p.writeCtrl(addr, resp)
	case protocol.RelayCtrlGroup:
		peers := make([]string, 0, len(p.seen))
		for ep := range p.seen {
			if ep == addr || p.ctrlOnly[ep] {
				continue
			}
			peers = append(peers, ep)
		}
		resp, _ := json.Marshal(map[string]interface{}{"op": protocol.RelayCtrlGroup, "peers": peers})
		p.writeCtrl(addr, resp)
	}
}

// writeCtrl sends a relay control reply to addr, framing it with the control
// prefix so receivers can separate control replies from forwarded traffic.
func (p *relayPair) writeCtrl(addr string, resp []byte) {
	framed := make([]byte, 0, len(protocol.RelayCtrlPrefix)+len(resp))
	framed = append(framed, protocol.RelayCtrlPrefix...)
	framed = append(framed, resp...)
	dst, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return
	}
	if _, err := p.conn.WriteToUDP(framed, dst); err != nil {
		log.Printf("relay ctrl reply to %s: %v", addr, err)
	}
}
