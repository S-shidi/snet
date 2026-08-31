package server

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "snet.db")

	s, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := s.Join(created.NetworkID, created.PairingCode, "rQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2t=", "dev-joiner")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetEndpoint(joined.Token, "203.0.113.7:51821", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// "restart": reopen the same database
	s2, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	// network + owner + joiner survive
	peersResp, err := s2.ListPeers(created.Token)
	if err != nil {
		t.Fatalf("owner peers after restart: %v", err)
	}
	if len(peersResp.Peers) != 1 || peersResp.Peers[0].PublicKey != "rQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2t=" || peersResp.Peers[0].Endpoint != "203.0.113.7:51821" {
		t.Fatalf("owner peers wrong after restart: %+v", peersResp.Peers)
	}

	// pairing code still usable after restart
	j2, err := s2.Join(created.NetworkID, created.PairingCode, "sRw3bI7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2u=", "dev-joiner-2")
	if err != nil {
		t.Fatalf("join after restart failed: %v", err)
	}
	if j2.IP != "10.88.0.3" {
		t.Fatalf("ipam not restored, got %q want 10.88.0.3", j2.IP)
	}

	// remove works after restart
	if err := s2.RemoveNode(joined.Token); err != nil {
		t.Fatal(err)
	}
	peersResp, err = s2.ListPeers(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range peersResp.Peers {
		if p.ID == joined.NodeID {
			t.Fatalf("removed node still present: %+v", p)
		}
	}
}

func TestManagedNetworkPersists(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "snet.db")

	s, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.AdminCreateNetwork("永久网络", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !s.networks[created.NetworkID].n.Managed {
		t.Fatal("managed flag not set in memory")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	ns := s2.networks[created.NetworkID]
	if ns == nil {
		t.Fatal("managed network lost after restart")
	}
	if !ns.n.Managed {
		t.Fatal("managed flag not restored after restart")
	}
	// unlimited, non-expiring code survives restart
	if ns.pairing.remaining != codeUnlimited || !ns.pairing.expiresAt.IsZero() {
		t.Fatalf("long-lived code not restored: %+v", ns.pairing)
	}
}

func TestTokenStoredHashed(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "snet.db")
	s, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := bbolt.Open(dbPath, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.View(func(tx *bbolt.Tx) error {
		tkb := tx.Bucket(bktTokens)
		if tkb == nil {
			t.Fatal("tokens bucket missing")
		}
		return tkb.ForEach(func(k, v []byte) error {
			if string(k) == created.Token {
				t.Fatalf("plaintext token found in db")
			}
			if string(k) != hashToken(created.Token) {
				t.Fatalf("token key not hashed: %q", k)
			}
			var te tokenEntry
			if err := json.Unmarshal(v, &te); err != nil {
				t.Fatal(err)
			}
			if te.NodeID != created.NodeID {
				t.Fatalf("token entry mismatch: %+v", te)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPairingCodeStoredHashed(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "snet.db")
	s, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := bbolt.Open(dbPath, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.View(func(tx *bbolt.Tx) error {
		nb := tx.Bucket(bktNetworks)
		if nb == nil {
			t.Fatal("networks bucket missing")
		}
		v := nb.Get([]byte(created.NetworkID))
		if v == nil {
			t.Fatal("network row missing")
		}
		var r networkRecord
		if err := json.Unmarshal(v, &r); err != nil {
			t.Fatal(err)
		}
		if r.CodeHash != hashCode(created.PairingCode) {
			t.Fatalf("code hash mismatch")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLastSeenTracking(t *testing.T) {
	s := NewStore()
	created, _ := s.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)

	if err := s.SetEndpoint(created.Token, "203.0.113.1:51820", ""); err != nil {
		t.Fatal(err)
	}
	peersResp, err := s.ListPeers(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if len(peersResp.Peers) != 0 {
		t.Fatalf("expected no peers for sole member, got %+v", peersResp.Peers)
	}
	if created.Token == "" {
		t.Fatal("empty token")
	}
	// owner lastSeen updated by ListPeers; verify via store internals
	time.Sleep(5 * time.Millisecond)
	_ = peersResp
	// direct check
	ns := s.networks[created.NetworkID]
	me := ns.nodes[created.NodeID]
	if me.LastSeen == 0 {
		t.Fatal("lastSeen not updated by ListPeers")
	}
}
