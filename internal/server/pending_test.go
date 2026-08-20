package server

import (
	"strings"
	"testing"
)

// TestJoinApprovalFlow covers the full approve/deny lifecycle of a
// join-approval-required network.
func TestJoinApprovalFlow(t *testing.T) {
	s := NewStore()
	created, err := s.CreateNetwork("AAA==", "dev-owner", "office", "192.168.50.0/24", true)
	if err != nil {
		t.Fatal(err)
	}
	if !created.ApprovalRequired {
		t.Fatal("network should require approval")
	}

	// join without approval returns a pending status, no credentials
	joined, err := s.Join(created.NetworkID, created.PairingCode, "BBB==", "dev-joiner")
	if err != nil {
		t.Fatal(err)
	}
	if joined.Status != "pending" || joined.PendingID == "" {
		t.Fatalf("join = %+v, want pending", joined)
	}
	if joined.IP != "" || joined.Token != "" || joined.NodeID != "" {
		t.Fatalf("pending join must not issue credentials: %+v", joined)
	}

	// the pending request is visible to the owner via NetworkInfo
	info, err := s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Pending) != 1 || info.PendingCount != 1 {
		t.Fatalf("pending not visible to owner: %+v", info.Pending)
	}
	if info.Pending[0].PublicKey != "BBB==" {
		t.Fatalf("pending pubkey = %q", info.Pending[0].PublicKey)
	}

	// the joiner polls and still sees pending
	st, err := s.PendingStatus(joined.PendingID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != "pending" {
		t.Fatalf("status = %q, want pending", st.Status)
	}

	// a non-owner cannot approve
	intruder, err := s.Join(created.NetworkID, created.PairingCode, "CCC==", "dev-intruder")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.OwnerApprove(intruder.Token, joined.PendingID); err == nil {
		t.Fatal("non-owner approve should fail")
	}
	// a non-owner cannot deny
	if err := s.OwnerDeny(intruder.Token, joined.PendingID); err == nil {
		t.Fatal("non-owner deny should fail")
	}

	// owner approves
	approveResp, err := s.OwnerApprove(created.Token, joined.PendingID)
	if err != nil {
		t.Fatal(err)
	}
	if approveResp.Status != "approved" || approveResp.NodeID == "" || approveResp.IP == "" || approveResp.Token == "" {
		t.Fatalf("approve = %+v", approveResp)
	}
	if approveResp.IP != "192.168.50.2" {
		t.Fatalf("approved IP = %q, want 192.168.50.2", approveResp.IP)
	}

	// joiner polls again: gets full credentials (the HTTP handler consumes
	// the record after handing them out)
	st, err = s.PendingStatus(joined.PendingID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != "approved" || st.Token != approveResp.Token {
		t.Fatalf("status after approve = %+v", st)
	}
	s.ConsumePending(joined.PendingID)
	st, err = s.PendingStatus(joined.PendingID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != "gone" {
		t.Fatalf("consumed pending should report gone, got %q", st.Status)
	}

	// the approved node can fetch peers like any member
	peers, err := s.ListPeers(approveResp.Token)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers.Peers) != 1 {
		t.Fatalf("owner should see the approved node, got %d", len(peers.Peers))
	}
}

// TestJoinDenyFlow verifies a denied request is surfaced to the joiner.
func TestJoinDenyFlow(t *testing.T) {
	s := NewStore()
	created, err := s.CreateNetwork("AAA==", "dev-owner", "office", "192.168.51.0/24", true)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := s.Join(created.NetworkID, created.PairingCode, "BBB==", "dev-joiner")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.OwnerDeny(created.Token, joined.PendingID); err != nil {
		t.Fatal(err)
	}
	st, err := s.PendingStatus(joined.PendingID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != "denied" {
		t.Fatalf("status = %q, want denied", st.Status)
	}

	// an already-approved request cannot be denied
	joined2, err := s.Join(created.NetworkID, created.PairingCode, "CCC==", "dev-joiner-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.OwnerApprove(created.Token, joined2.PendingID); err != nil {
		t.Fatal(err)
	}
	if err := s.OwnerDeny(created.Token, joined2.PendingID); err == nil {
		t.Fatal("deny after approve should fail")
	}
}

// TestJoinNoApprovalStillImmediate verifies approval-free networks join
// immediately even when the setting field is present.
func TestJoinNoApprovalStillImmediate(t *testing.T) {
	s := NewStore()
	created, err := s.CreateNetwork("AAA==", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := s.Join(created.NetworkID, created.PairingCode, "BBB==", "dev-joiner")
	if err != nil {
		t.Fatal(err)
	}
	if joined.Status != "" || joined.IP == "" {
		t.Fatalf("no-approval join must be immediate: %+v", joined)
	}
}

// TestSubnetReassign verifies the owner can change a network's subnet and
// every node is re-allocated, with the self IP reflecting the change.
func TestSubnetReassign(t *testing.T) {
	s := NewStore()
	created, err := s.CreateNetwork("AAA==", "dev-owner", "home", "10.88.0.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := s.Join(created.NetworkID, created.PairingCode, "BBB==", "dev-joiner")
	if err != nil {
		t.Fatal(err)
	}
	if joined.IP != "10.88.0.2" {
		t.Fatalf("joiner IP = %q", joined.IP)
	}

	// non-owner cannot change settings
	if err := s.UpdateNetworkSettings(joined.Token, "hax", "", nil); err == nil {
		t.Fatal("non-owner settings change should fail")
	}
	// invalid subnet rejected
	if err := s.UpdateNetworkSettings(created.Token, "", "999.0.0.0/24", nil); err == nil {
		t.Fatal("invalid subnet should be rejected")
	}
	// overlapping subnet is now allowed (networks are independent)
	other, err := s.CreateNetwork("CCC==", "dev-other", "", "10.88.1.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = other
	if err := s.UpdateNetworkSettings(created.Token, "", "10.88.1.0/24", nil); err != nil {
		t.Fatalf("overlapping subnet should be allowed: %v", err)
	}
	// unchanged subnet is a no-op (no error, nothing changes)
	if err := s.UpdateNetworkSettings(created.Token, "", "10.88.0.0/24", nil); err != nil {
		t.Fatalf("unchanged subnet should be a no-op: %v", err)
	}

	// owner changes subnet: both nodes get new IPs
	if err := s.UpdateNetworkSettings(created.Token, "home-renamed", "192.168.77.0/24", nil); err != nil {
		t.Fatal(err)
	}
	info, err := s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if info.Subnet != "192.168.77.0/24" {
		t.Fatalf("subnet = %q", info.Subnet)
	}
	if info.Name != "home-renamed" {
		t.Fatalf("name = %q", info.Name)
	}
	if len(info.Nodes) != 2 {
		t.Fatalf("node count = %d", len(info.Nodes))
	}
	for _, n := range info.Nodes {
		if !strings.HasPrefix(n.IP, "192.168.77.") {
			t.Fatalf("node IP %q outside new subnet", n.IP)
		}
	}

	// the joiner's own poll sees the new IP via self
	st, err := s.ListPeers(joined.Token)
	if err != nil {
		t.Fatal(err)
	}
	if st.Self == nil || st.Self.IP == "10.88.0.2" || !strings.HasPrefix(st.Self.IP, "192.168.77.") {
		t.Fatalf("self after reassign = %+v", st.Self)
	}
	if st.Subnet != "192.168.77.0/24" {
		t.Fatalf("reported subnet = %q", st.Subnet)
	}

	// ipam cursor was reset: a new joiner gets the next free address
	j3, err := s.Join(created.NetworkID, created.PairingCode, "DDD==", "dev-joiner-3")
	if err != nil {
		t.Fatal(err)
	}
	if j3.IP != "192.168.77.3" {
		t.Fatalf("joiner after reassign = %q, want 192.168.77.3", j3.IP)
	}
}
