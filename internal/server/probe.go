package server

import (
	"encoding/json"
	"log"
	"net"
	"sync"
)

// ProbeServer is a lightweight UDP echo used by clients to discover their
// public (NAT-observed) IP. It replies to any packet with the source address
// it observed, as {"endpoint":"ip:port"}. Stateless: no store access.
type ProbeServer struct {
	conn *net.UDPConn
	done chan struct{}
	wg   sync.WaitGroup
}

// StartProbeServer listens for UDP probes on addr and replies with the
// observed source endpoint. addr like "0.0.0.0:8091".
func StartProbeServer(addr string) (*ProbeServer, error) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	ps := &ProbeServer{conn: pc.(*net.UDPConn), done: make(chan struct{})}
	ps.wg.Add(1)
	go ps.loop()
	log.Printf("vnet probe server on %s (udp)", addr)
	return ps, nil
}

func (ps *ProbeServer) loop() {
	defer ps.wg.Done()
	buf := make([]byte, 4096)
	for {
		n, addr, err := ps.conn.ReadFrom(buf)
		if err != nil {
			select {
			case <-ps.done:
				return
			default:
				log.Printf("probe read: %v", err)
				continue
			}
		}
		_ = n
		resp, _ := json.Marshal(map[string]string{"endpoint": addr.String()})
		_, _ = ps.conn.WriteTo(resp, addr)
	}
}

func (ps *ProbeServer) Close() error {
	close(ps.done)
	err := ps.conn.Close()
	ps.wg.Wait()
	return err
}
