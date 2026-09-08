package server

import (
	"log"
	"net"
	"sync"
	"time"
)

// Relay forwards UDP packets among the active endpoints of a network port,
// letting WireGuard traverse symmetric NAT on both sides. Each network is
// assigned one port; every endpoint that sends a keepalive on that port is
// remembered, and a data packet from one endpoint is forwarded to all the
// others (fan-out), so networks with more than two members still reach each
// other through the relay.
type Relay struct {
	base  int
	count int

	mu         sync.Mutex
	conns      map[int]*relayPair
	onActivity func(port int)
}

type relayPair struct {
	conn     *net.UDPConn
	port     int
	hook     func(port int)
	lastHook time.Time
	mu       sync.Mutex
	// seen tracks the last traffic time per relay endpoint ("ip:port") so
	// stale NAT mappings can be evicted and current ones retained.
	seen map[string]time.Time
}

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
// listening.
func (r *Relay) Ensure(port int) error {
	r.mu.Lock()
	if _, ok := r.conns[port]; ok {
		r.mu.Unlock()
		return nil
	}
	hook := r.onActivity
	r.mu.Unlock()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
	if err != nil {
		return err
	}
	p := &relayPair{conn: conn, port: port, hook: hook, seen: make(map[string]time.Time)}

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
	log.Printf("relay: listening on udp :%d (lazy)", port)
	return nil
}

// Start binds every port in the range and spawns a read loop per port.
func (r *Relay) Start() error {
	for i := 0; i < r.count; i++ {
		port := r.base + i
		conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
		if err != nil {
			r.Close()
			return err
		}
		r.mu.Lock()
		p := &relayPair{conn: conn, port: port, hook: r.onActivity, seen: make(map[string]time.Time)}
		r.conns[port] = p
		r.mu.Unlock()
		go p.serve()
		log.Printf("relay: listening on udp :%d", port)
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

func (p *relayPair) handle(addr string, data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
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

	// Fan out to every other live endpoint.
	for ep := range p.seen {
		if ep == addr {
			continue
		}
		dst, err := net.ResolveUDPAddr("udp", ep)
		if err != nil {
			delete(p.seen, ep)
			continue
		}
		if _, err := p.conn.WriteToUDP(data, dst); err != nil {
			delete(p.seen, ep)
			log.Printf("relay: write to %s: %v", ep, err)
		}
	}
}
