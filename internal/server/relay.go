package server

import (
	"log"
	"net"
	"sync"
	"time"
)

// Relay pairs UDP endpoints per port and forwards packets between the two,
// allowing WireGuard to traverse symmetric NAT on both sides. Each network is
// assigned one port; the two peers that send keepalives on that port are
// linked and every packet from one is relayed to the other.
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
	seen     map[string]time.Time
	a        string // first known endpoint ("ip:port")
	b        string // second known endpoint
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

	switch addr {
	case p.a:
		p.seen[p.a] = now
	case p.b:
		p.seen[p.b] = now
	default:
		if p.a == "" {
			p.a = addr
		} else if p.b == "" {
			p.b = addr
		} else {
			// both slots taken: evict the stale endpoint so a re-mapped
			// NAT address (new keepalive source) can take over.
			if p.seen[p.a].After(p.seen[p.b]) {
				delete(p.seen, p.b)
				p.b = addr
			} else {
				delete(p.seen, p.a)
				p.a = addr
			}
		}
		p.seen[addr] = now
	}

	var to string
	if addr == p.a {
		to = p.b
	} else {
		to = p.a
	}
	if to == "" {
		return
	}
	dst, err := net.ResolveUDPAddr("udp", to)
	if err != nil {
		return
	}
	if _, err := p.conn.WriteToUDP(data, dst); err != nil {
		log.Printf("relay: write to %s: %v", to, err)
	}
}
