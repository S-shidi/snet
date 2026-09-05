package server

import (
	"net/http"
	"testing"

	"snet/internal/protocol"
)

// TestShareMetadataPersistence verifies description/tags/visibility set at
// create time survive and are surfaced through NetworkInfo.
func TestShareMetadataPersistence(t *testing.T) {
	s := NewStore()
	desc := "家庭共享网络"
	vis := "shareable"
	created, err := s.CreateNetwork(testKey(1), "dev-owner", "家庭NAS", "192.168.60.0/24", false,
		networkShareOpts{description: &desc, tags: []string{"family", "nas"}, visibility: &vis})
	if err != nil {
		t.Fatal(err)
	}

	info, err := s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if info.Visibility != "shareable" {
		t.Fatalf("visibility = %q, want shareable", info.Visibility)
	}
	if info.Description != "家庭共享网络" {
		t.Fatalf("description = %q", info.Description)
	}
	if len(info.Tags) != 2 || info.Tags[0] != "family" || info.Tags[1] != "nas" {
		t.Fatalf("tags = %v", info.Tags)
	}
}

// TestShareMetadataValidation rejects bad visibility, oversized descriptions
// and too many/too-long tags at both create and update time.
func TestShareMetadataValidation(t *testing.T) {
	s := NewStore()

	bad := "weird"
	if _, err := s.CreateNetwork(testKey(1), "dev-share-1", "", "", false, networkShareOpts{visibility: &bad}); err == nil {
		t.Fatal("invalid visibility should be rejected")
	}
	longDesc := make([]byte, 501)
	for i := range longDesc {
		longDesc[i] = 'a'
	}
	ld := string(longDesc)
	if _, err := s.CreateNetwork(testKey(2), "dev-share-2", "", "", false, networkShareOpts{description: &ld}); err == nil {
		t.Fatal("description > 500 should be rejected")
	}
	tooMany := make([]string, 9)
	for i := range tooMany {
		tooMany[i] = "t"
	}
	if _, err := s.CreateNetwork(testKey(3), "dev-share-3", "", "", false, networkShareOpts{tags: tooMany}); err == nil {
		t.Fatal("> 8 tags should be rejected")
	}
	if _, err := s.CreateNetwork(testKey(4), "dev-share-4", "", "", false, networkShareOpts{tags: []string{"012345678901234567890"}}); err == nil {
		t.Fatal("tag > 20 chars should be rejected")
	}

	// admin-managed networks enforce the same share validation
	if _, err := s.AdminCreateNetwork("admin-net", "", false, networkShareOpts{tags: []string{"012345678901234567890"}}); err == nil {
		t.Fatal("admin create tag > 20 chars should be rejected")
	}
	adbadv := "weird"
	if _, err := s.AdminCreateNetwork("admin-net2", "", false, networkShareOpts{visibility: &adbadv}); err == nil {
		t.Fatal("admin create invalid visibility should be rejected")
	}

	// update path enforces the same rules
	created, err := s.CreateNetwork(testKey(5), "dev-share-5", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateNetworkSettings(created.Token, "", "", nil, networkShareOpts{visibility: &bad}); err == nil {
		t.Fatal("update invalid visibility should be rejected")
	}
	if err := s.UpdateNetworkSettings(created.Token, "", "", nil, networkShareOpts{description: &ld}); err == nil {
		t.Fatal("update oversized description should be rejected")
	}
	if err := s.UpdateNetworkSettings(created.Token, "", "", nil, networkShareOpts{tags: tooMany}); err == nil {
		t.Fatal("update too many tags should be rejected")
	}
}

// TestSetNodeRole covers the role-assignment permission and value rules.
func TestSetNodeRole(t *testing.T) {
	s := NewStore()
	created, err := s.CreateNetwork(testKey(1), "dev-owner", "office", "192.168.61.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	joiner, err := s.Join(created.NetworkID, created.PairingCode, testKey(2), "dev-joiner")
	if err != nil {
		t.Fatal(err)
	}

	// invalid role rejected
	if err := s.SetNodeRole(created.Token, joiner.NodeID, "superuser"); err == nil {
		t.Fatal("invalid role should be rejected")
	}
	// owner cannot change its own role / target the owner node
	if err := s.SetNodeRole(created.Token, created.NodeID, "member"); err == nil {
		t.Fatal("changing owner role should be rejected")
	}
	// unknown node
	if err := s.SetNodeRole(created.Token, "no-such-node", "admin"); err == nil {
		t.Fatal("unknown node should be rejected")
	}
	// non-owner cannot set roles
	if err := s.SetNodeRole(joiner.Token, created.NodeID, "admin"); err == nil {
		t.Fatal("non-owner role change should be rejected")
	}
	// non-owner cannot promote another peer either
	third, err := s.Join(created.NetworkID, created.PairingCode, testKey(3), "dev-third")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeRole(joiner.Token, third.NodeID, "admin"); err == nil {
		t.Fatal("non-owner promoting a peer should be rejected")
	}

	// owner promotes the joiner to admin
	if err := s.SetNodeRole(created.Token, joiner.NodeID, "admin"); err != nil {
		t.Fatal(err)
	}
	info, err := s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	got := ""
	for _, n := range info.Nodes {
		if n.ID == joiner.NodeID {
			got = n.Role
		}
	}
	if got != "admin" {
		t.Fatalf("joiner role = %q, want admin", got)
	}

	// demote back to member
	if err := s.SetNodeRole(created.Token, joiner.NodeID, "member"); err != nil {
		t.Fatal(err)
	}
	info, err = s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range info.Nodes {
		if n.ID == joiner.NodeID && n.Role != "member" {
			t.Fatalf("joiner role after demote = %q", n.Role)
		}
	}
}

// TestSetNodeRoleHTTP verifies the owner-only role endpoint over HTTP,
// including that a non-owner is rejected with a forbidden status.
func TestSetNodeRoleHTTP(t *testing.T) {
	ts, _ := newTestServer(t)

	var created protocol.CreateNetworkResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		protocol.CreateNetworkReq{PublicKey: testKey(10), DeviceID: "dev-owner-role", Name: "office", Subnet: "192.168.62.0/24"}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}

	var joined protocol.JoinResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		protocol.JoinReq{Code: created.PairingCode, PublicKey: testKey(11)}, &joined)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("join status = %d", resp.StatusCode)
	}

	// owner sets role -> 204
	resp = doJSON(t, http.MethodPut, ts.URL+"/api/v1/networks/"+created.NetworkID+"/nodes/"+joined.NodeID+"/role",
		created.Token, protocol.SetNodeRoleReq{Role: "admin"}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("owner set-role status = %d", resp.StatusCode)
	}

	// non-owner is unauthorized (owner-gated, same as other owner ops)
	resp = doJSON(t, http.MethodPut, ts.URL+"/api/v1/networks/"+created.NetworkID+"/nodes/"+joined.NodeID+"/role",
		joined.Token, protocol.SetNodeRoleReq{Role: "admin"}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("non-owner set-role status = %d, want 401", resp.StatusCode)
	}
}

// TestListPeersRoleScoping verifies members never see peer deviceId/role/
// subnets (publicKey is kept so members can build WireGuard tunnels), while
// owners and admins see full peer details.
func TestListPeersRoleScoping(t *testing.T) {
	s := NewStore()
	created, err := s.CreateNetwork(testKey(1), "dev-owner", "office", "192.168.63.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	joiner, err := s.Join(created.NetworkID, created.PairingCode, testKey(2), "dev-joiner")
	if err != nil {
		t.Fatal(err)
	}
	// promote joiner to admin, then add a plain member
	if err := s.SetNodeRole(created.Token, joiner.NodeID, "admin"); err != nil {
		t.Fatal(err)
	}
	member, err := s.Join(created.NetworkID, created.PairingCode, testKey(3), "dev-member")
	if err != nil {
		t.Fatal(err)
	}

	// plain member's view of peers: deviceId/role/subnets scrubbed, but the
	// WireGuard publicKey must remain so the member can build tunnels (hidden
	// keys would make every peer unreachable by WG).
	peers, err := s.ListPeers(member.Token)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range peers.Peers {
		if p.DeviceID != "" || p.Role != "" || len(p.AllowedSubnets) != 0 {
			t.Fatalf("member leaky peer view: %+v", p)
		}
		if p.PublicKey == "" {
			t.Fatalf("member peer missing publicKey (must be able to build tunnels): %+v", p)
		}
	}

	// admin view shows full details
	peers, err = s.ListPeers(joiner.Token)
	if err != nil {
		t.Fatal(err)
	}
	full := false
	for _, p := range peers.Peers {
		if p.DeviceID != "" && p.PublicKey != "" {
			full = true
		}
	}
	if !full {
		t.Fatalf("admin should see full peer details: %+v", peers.Peers)
	}

	// owner view shows full details too
	peers, err = s.ListPeers(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range peers.Peers {
		if p.DeviceID == "" || p.PublicKey == "" {
			t.Fatalf("owner peer missing deviceId/publicKey: %+v", p)
		}
	}
}
