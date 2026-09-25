package server

import (
	"time"

	"snet/internal/protocol"
)

// Test-only seams removed from the production Store. They exist so unit tests
// can seed store state (control-plane host attribution, device tokens, pending
// joins) without going through the HTTP handlers. Keeping them in a _test.go
// file means the shipped server binary carries only the same methods the HTTP
// layer actually calls.

// NoteCtrlHost records the public IP a node's control-plane calls come from.
// Production attributes this inside the HTTP handlers (ListPeersFrom and
// friends); tests use this seam to seed attribution directly.
func (s *Store) NoteCtrlHost(nid, nodeID, host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ns := s.networks[nid]; ns != nil {
		ns.ctrlHost[nodeID] = host
	}
}

// ListPeers returns the peer view for token without capturing a control-plane
// host. Production always passes the caller's remote address (ListPeersFrom).
func (s *Store) ListPeers(token string) (protocol.PeersResp, error) {
	return s.listPeersFrom(token, "", false)
}

// PendingStatus reports the state of a pending join request. Production serves
// the same shape through the pending HTTP route; tests call it directly.
func (s *Store) PendingStatus(pendingID string) (protocol.PendingStatusResp, error) {
	if pendingID == "" {
		return protocol.PendingStatusResp{}, ErrUnauthorized
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pruneExpiredPendingLocked(s, time.Now())
	for _, ns := range s.networks {
		p := ns.pending[pendingID]
		if p == nil {
			continue
		}
		switch p.Status {
		case "approved":
			peers := make([]protocol.Node, 0, len(ns.nodes)-1)
			for id, n := range ns.nodes {
				if id != p.NodeID {
					peers = append(peers, *n)
				}
			}
			return protocol.PendingStatusResp{
				Status:    "approved",
				NetworkID: ns.n.ID,
				Name:      ns.n.Name,
				NodeID:    p.NodeID,
				IP:        p.IP,
				Token:     p.Token,
				Subnet:    p.Subnet,
				RelayPort: p.RelayPort,
				Peers:     peers,
			}, nil
		case "denied":
			return protocol.PendingStatusResp{Status: "denied", NetworkID: ns.n.ID, Name: ns.n.Name}, nil
		default:
			return protocol.PendingStatusResp{Status: "pending", NetworkID: ns.n.ID, Name: ns.n.Name}, nil
		}
	}
	return protocol.PendingStatusResp{Status: "gone"}, nil
}

// ConsumePending deletes an approved pending request once the client has
// picked up its credentials. Production consumes via PendingClaim in the same
// critical section that hands out the one-shot credentials.
func (s *Store) ConsumePending(pendingID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ns := range s.networks {
		if ns.pending[pendingID] != nil {
			delete(ns.pending, pendingID)
			_ = s.deletePending(pendingID)
			return
		}
	}
}

// AdminDevices lists all registered device identities. Production serves the
// paged view (AdminDevicesPage) through the admin HTTP routes.
func (s *Store) AdminDevices() []protocol.Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]protocol.Device, 0, len(s.devices))
	for _, d := range s.devices {
		out = append(out, protocol.Device{
			ID:        d.ID,
			PublicKey: d.PublicKey,
			CreatedAt: d.CreatedAt,
			LastSeen:  d.LastSeen,
			Name:      d.Name,
		})
	}
	return out
}

// GenerateDeviceToken creates (or rotates) a device-level bearer token that
// the device uses to enroll without re-entering its authorization code.
func (s *Store) GenerateDeviceToken(deviceID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.devices[deviceID]
	if d == nil {
		return "", ErrNotFound
	}
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	d.DeviceToken = tok
	if err := s.persistDevice(d); err != nil {
		return "", err
	}
	return tok, nil
}

// RequireDeviceAuth reports whether the enrollment gate is on.
func (s *Store) RequireDeviceAuth() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.requireDeviceAuth
}

// CheckDeviceAuth checks if a device is bound to a valid (non-expired) auth
// code. Production performs this check inside CreateNetwork/Join/Bind under
// the enrollment gate; tests call it directly.
func (s *Store) CheckDeviceAuth(deviceID string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.requireDeviceAuth {
		return nil
	}

	return s.checkDeviceAuthLocked(deviceID)
}

// Start binds every port in the relay's assignable range up front. Production
// uses lazy binding (Ensure per port on first use); tests use Start to stand
// up the whole pool eagerly.
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